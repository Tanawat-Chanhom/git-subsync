package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/repo"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
	"github.com/Tanawat-Chanhom/git-subsync/internal/syncer"
)

func newAddCmd() *cobra.Command {
	var (
		mode   string
		noSync bool
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "add <drive-folder-url> <path>",
		Short: "Link a folder in this repo to a Google Drive folder and sync it",
		Long: `Link the folder at <path> (relative to the repo root) to a Google Drive
folder, add it to .gitsubsync and .gitignore, and sync it.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			folderID, err := gdrive.ParseFolderID(args[0])
			if err != nil {
				return err
			}
			m, err := syncer.ParseMode(mode)
			if err != nil {
				return err
			}
			w, err := openWorkspace(ctx)
			if err != nil {
				return err
			}
			p, err := repo.CleanPath(w.root, args[1])
			if err != nil {
				return err
			}
			if e, ok := repo.Overlapping(w.entries, p); ok {
				if e.Path == p {
					return fmt.Errorf("%s is already linked to Drive folder %s", p, e.Folder)
				}
				return fmt.Errorf("%s overlaps %s, which is already linked", p, e.Path)
			}
			if err := repo.CheckNoSymlinks(w.root, p); err != nil {
				return err
			}
			tracked, err := repo.IsTracked(ctx, w.root, p)
			if err != nil {
				return err
			}
			if tracked {
				return fmt.Errorf("%s is tracked by Git; synced files must not be in Git (untrack them with `git rm -r --cached %s` first)", p, shellQuote(p))
			}
			dir := w.dir(p)
			switch fi, err := os.Lstat(dir); {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return err
			case !fi.IsDir():
				return fmt.Errorf("%s exists but is not a folder", p)
			default:
				names, err := os.ReadDir(dir)
				if err != nil {
					return err
				}
				if len(names) > 0 && !force {
					return fmt.Errorf("%s is not empty; use --force to sync into it (files that differ from Drive are kept as _copy files)", p)
				}
			}

			d, user, err := openDrive(ctx, cmd)
			if err != nil {
				return err
			}
			folder, err := driveFolder(ctx, d, user, folderID)
			if err != nil {
				return err
			}
			if m != syncer.Download && !folder.CanAddChildren {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s can't add files to %q, so uploads will fail; use --mode download to only download\n", user.Email, folder.Name)
			}

			entry := repo.Entry{Name: p, Path: p, Folder: folderID, Mode: string(m)}
			if err := repo.AddEntry(ctx, w.root, entry); err != nil {
				return err
			}
			if err := repo.AddIgnore(w.root, p); err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			st := state.New(folderID)
			st.FolderName = folder.Name
			if err := w.store(p).Save(st); err != nil {
				return err
			}
			w.entries = append(w.entries, entry)
			fmt.Fprintf(out, "Added %s → Drive folder %q (mode: %s)\n", p, folder.Name, m)

			code := 0
			if noSync {
				fmt.Fprintf(out, "Run `git subsync sync %s` to sync it.\n", shellQuote(p))
			} else {
				code, err = syncFolder(cmd, w, d, user, entry, syncOptions{now: time.Now()})
				if err != nil {
					printErr(cmd.ErrOrStderr(), p, err)
					code = 1
				}
			}
			fmt.Fprintf(out, "Commit %s and .gitignore so others can sync it with `git subsync sync`.\n", repo.ConfigFile)
			if code != 0 {
				return &exitError{code: code}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", repo.DefaultMode, "sync direction: both, download or upload")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "only link the folder; don't sync it yet")
	cmd.Flags().BoolVar(&force, "force", false, "allow a folder that already has files in it")
	return cmd
}
