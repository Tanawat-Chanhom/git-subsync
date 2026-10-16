package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/repo"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
	"github.com/Tanawat-Chanhom/git-subsync/internal/syncer"
)

// workspace is the Git repo a command runs in, found from the current
// directory.
type workspace struct {
	root     string
	entries  []repo.Entry
	stateDir string
}

func openWorkspace(ctx context.Context) (*workspace, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := repo.Root(ctx, wd)
	if err != nil {
		return nil, err
	}
	entries, err := repo.LoadConfig(ctx, root)
	if err != nil {
		return nil, err
	}
	stateDir, err := repo.GitPath(ctx, root, "subsync")
	if err != nil {
		return nil, err
	}
	return &workspace{root: root, entries: entries, stateDir: stateDir}, nil
}

func (w *workspace) store(p string) *state.Store { return state.NewStore(w.stateDir, p) }

func (w *workspace) dir(p string) string { return filepath.Join(w.root, filepath.FromSlash(p)) }

// entry returns the entry for a <path> argument.
func (w *workspace) entry(arg string) (repo.Entry, error) {
	p, err := repo.CleanPath(w.root, arg)
	if err != nil {
		return repo.Entry{}, err
	}
	e, ok := repo.FindEntry(w.entries, p)
	if !ok {
		return repo.Entry{}, fmt.Errorf("%s is not a git-subsync folder (see `git subsync list`)", p)
	}
	return e, nil
}

const noEntries = "No folders linked yet. Add one with `git subsync add <drive-folder-url> <path>`."

// driveFolder returns the Drive folder with the given ID, with messages
// that say what is wrong with it.
func driveFolder(ctx context.Context, d gdrive.Drive, user gdrive.User, id string) (gdrive.File, error) {
	f, err := d.Get(ctx, id)
	switch {
	case errors.Is(err, gdrive.ErrNoAccess):
		return f, fmt.Errorf("Drive folder %s doesn't exist, or %s has no access to it", id, user.Email)
	case err != nil:
		return f, err
	case !f.IsFolder():
		return f, fmt.Errorf("%s (%q) is a file, not a folder", id, f.Name)
	case f.Trashed:
		return f, fmt.Errorf("Drive folder %q is in the trash", f.Name)
	}
	return f, nil
}

type syncOptions struct {
	force  bool
	dryRun bool
	now    time.Time
}

// syncFolder syncs one entry, prints its actions, summary and any hint,
// and returns its exit code. The error is for problems that stop the sync
// of this entry before it starts.
func syncFolder(cmd *cobra.Command, w *workspace, d gdrive.Drive, user gdrive.User, e repo.Entry, opts syncOptions) (int, error) {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	mode, err := syncer.ParseMode(e.Mode)
	if err != nil {
		return 1, fmt.Errorf("%s in %s: %w", e.Path, repo.ConfigFile, err)
	}
	if opts.force && mode != syncer.Upload {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: --force has no effect in %s mode (%s)\n", mode, e.Path)
	}

	store := w.store(e.Path)
	if !opts.dryRun {
		unlock, err := store.Lock()
		if err != nil {
			return 1, err
		}
		defer unlock()
	}
	st, err := store.Load()
	if err != nil {
		return 1, err
	}
	if st == nil || st.Folder != e.Folder {
		st = state.New(e.Folder) // never synced, or linked to another folder since
	}

	folder, err := driveFolder(ctx, d, user, e.Folder)
	if err != nil {
		return 1, err
	}
	st.FolderName = folder.Name

	dir := w.dir(e.Path)
	switch fi, err := os.Lstat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		if !opts.dryRun {
			fmt.Fprintf(cmd.ErrOrStderr(), "Creating %s/\n", e.Path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return 1, err
			}
		}
	case err != nil:
		return 1, err
	case !fi.IsDir():
		return 1, fmt.Errorf("%s exists but is not a folder", e.Path)
	}

	res, err := syncer.Sync(ctx, d, dir, st, store.Save, syncer.Options{
		Mode: mode, Force: opts.force, DryRun: opts.dryRun, Now: opts.now, Out: out,
	})
	if err != nil {
		return 1, err
	}
	fmt.Fprintln(out, res.Summary())
	if n := len(res.Unresolved); n > 0 {
		fmt.Fprintf(out, "To upload your version, run: git subsync sync %s --force\n", shellQuote(e.Path))
		if n == 1 {
			fmt.Fprintf(out, "(the Drive version will be kept as %s)\n", syncer.CopyPattern(res.Unresolved[0]))
		} else {
			fmt.Fprintln(out, "(the Drive versions will be kept as <name>_copy_<YYYYMMDD-HHMMSS><ext>)")
		}
	}

	switch {
	case res.Failed > 0:
		return 1, nil
	case len(res.Unresolved) > 0 && !opts.dryRun:
		return 2, nil
	}
	return 0, nil
}

// worseExit combines exit codes: a failure (1) outranks unresolved
// conflicts (2), which outrank success.
func worseExit(a, b int) int {
	if a == 1 || b == 1 {
		return 1
	}
	return max(a, b)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._/@%+=:,-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printErr prints an error that doesn't stop the command, the way main
// prints the one that does.
func printErr(w io.Writer, prefix string, err error) {
	fmt.Fprintf(w, "git-subsync: %s: %v\n", prefix, withHint(err))
}
