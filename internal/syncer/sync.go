package syncer

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

// Options control one sync run.
type Options struct {
	Mode   Mode
	Force  bool
	DryRun bool      // print the plan without changing anything
	Now    time.Time // local time; names conflict copies
	Out    io.Writer // one line per action
}

// Result counts what a run did, or would do in a dry run.
type Result struct {
	DryRun     bool
	Downloaded int
	Uploaded   int
	Conflicts  int      // resolved
	Unresolved []string // paths of unresolved conflicts (upload mode without --force)
	Skipped    int
	Failed     int
}

// Summary returns a one-line summary, e.g. "3 downloaded, 1 uploaded,
// 1 conflict, 2 skipped".
func (r Result) Summary() string {
	var parts []string
	add := func(n int, s string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if r.DryRun {
		add(r.Downloaded, "to download")
		add(r.Uploaded, "to upload")
	} else {
		add(r.Downloaded, "downloaded")
		add(r.Uploaded, "uploaded")
	}
	add(r.Conflicts, plural(r.Conflicts, "conflict"))
	add(len(r.Unresolved), plural(len(r.Unresolved), "conflict")+" unresolved")
	add(r.Failed, "failed")
	s := strings.Join(parts, ", ")
	switch {
	case s == "" && r.DryRun:
		s = "up to date"
	case s == "":
		s = "Up to date"
	}
	if r.Skipped > 0 {
		s += fmt.Sprintf(", %d skipped", r.Skipped)
	}
	if r.DryRun {
		s = "Dry run: " + s
	}
	return s
}

func plural(n int, s string) string {
	if n == 1 {
		return s
	}
	return s + "s"
}

// Sync syncs the local folder dir with the Drive folder st.Folder. It
// updates st as files are synced and calls save after every change, so an
// interrupted run can be repeated. Failed actions are reported and counted
// in the result; the error is for failures that stop the whole run.
func Sync(ctx context.Context, d gdrive.Drive, dir string, st *state.State, save func(*state.State) error, opts Options) (Result, error) {
	local, err := ScanLocal(dir, st.Files)
	if err != nil {
		return Result{}, fmt.Errorf("scanning %s: %w", dir, err)
	}
	remote, err := ScanRemote(ctx, d, st.Folder)
	if err != nil {
		return Result{}, err
	}
	actions := Plan(local, remote, st.Files, opts.Mode, opts.Force, opts.Now)

	e := &executor{ctx: ctx, d: d, dir: dir, st: st, save: save, remote: remote, opts: opts, res: Result{DryRun: opts.DryRun}}
	if opts.DryRun {
		e.dryRun(actions)
		return e.res, nil
	}
	return e.res, e.run(local, actions)
}

type executor struct {
	ctx    context.Context
	d      gdrive.Drive
	dir    string
	st     *state.State
	save   func(*state.State) error
	remote Remote // Dirs grows as folders are created on Drive
	opts   Options
	res    Result
}

// changedError means a file changed between the scan and the action; the
// next sync will pick it up.
type changedError struct{ msg string }

func (e *changedError) Error() string { return e.msg }

func (e *executor) dryRun(actions []Action) {
	for _, a := range e.ordered(actions) {
		switch a.Kind {
		case KindDownload:
			e.res.Downloaded++
			e.printDownload(a)
		case KindUpload:
			e.res.Uploaded++
			e.printUpload(a)
		case KindConflict:
			e.printConflict(a)
			if a.Resolution == Unresolved {
				e.res.Unresolved = append(e.res.Unresolved, a.Path)
			} else {
				e.res.Conflicts++
			}
		case KindSkip:
			e.skip(a.Path, a.Note)
		}
	}
}

func (e *executor) run(local Local, actions []Action) error {
	for _, t := range local.Temps {
		os.Remove(e.path(t))
	}

	// State-only actions first, saved once.
	e.st.Folders = maps.Clone(e.remote.Dirs)
	for _, a := range actions {
		switch a.Kind {
		case KindRecord:
			e.setFile(a.Path, a.Remote.ID, a.Local.MD5, a.Local.Size, a.Local.MTime)
		case KindForget:
			delete(e.st.Files, a.Path)
		}
	}
	if err := e.save(e.st); err != nil {
		return err
	}

	// Phase 1 writes local files; phase 2 writes to Drive.
	renamed := map[string]bool{} // conflicts whose local file became the copy
	for _, a := range actions {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		switch {
		case a.Kind == KindDownload:
			if e.report(a.Path, e.download(a.Path, *a.Remote, a.Local)) {
				e.res.Downloaded++
				e.printDownload(a)
			}
		case a.Kind == KindConflict && (a.Resolution == KeepBoth || a.Resolution == KeepLocalCopy):
			ok, err := e.keepLocalCopy(a)
			renamed[a.Path] = ok
			if ok {
				e.res.Conflicts++
				e.printConflict(a)
			}
			if err != nil {
				if ok {
					err = fmt.Errorf("%w (your version is in %s)", err, path.Base(a.Copy))
				}
				e.report(a.Path, err)
			}
		}
	}
	for _, a := range actions {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		switch {
		case a.Kind == KindUpload:
			if e.report(a.Path, e.upload(a.Path, a.Remote)) {
				e.res.Uploaded++
				e.printUpload(a)
			}
		case a.Kind == KindConflict && a.Resolution == KeepBoth && renamed[a.Path]:
			e.report(a.Copy, e.upload(a.Copy, nil))
		case a.Kind == KindConflict && a.Resolution == KeepDriveCopy:
			if e.report(a.Path, e.keepDriveCopy(a)) {
				e.res.Conflicts++
				e.printConflict(a)
			}
		}
	}

	for _, a := range actions {
		if a.Kind == KindConflict && a.Resolution == Unresolved {
			e.res.Unresolved = append(e.res.Unresolved, a.Path)
			e.printConflict(a)
		}
	}
	for _, a := range actions {
		if a.Kind == KindSkip {
			e.skip(a.Path, a.Note)
		}
	}

	e.st.LastSync = e.opts.Now
	return e.save(e.st)
}

// ordered returns actions in the order a real run executes them.
func (e *executor) ordered(actions []Action) []Action {
	var phases [4][]Action
	for _, a := range actions {
		switch {
		case a.Kind == KindDownload, a.Kind == KindConflict && (a.Resolution == KeepBoth || a.Resolution == KeepLocalCopy):
			phases[0] = append(phases[0], a)
		case a.Kind == KindUpload, a.Kind == KindConflict && a.Resolution == KeepDriveCopy:
			phases[1] = append(phases[1], a)
		case a.Kind == KindConflict:
			phases[2] = append(phases[2], a)
		case a.Kind == KindSkip:
			phases[3] = append(phases[3], a)
		}
	}
	return append(append(append(phases[0], phases[1]...), phases[2]...), phases[3]...)
}

// report prints a failure or a skip for err, and reports whether err is
// nil.
func (e *executor) report(p string, err error) bool {
	var changed *changedError
	switch {
	case err == nil:
		return true
	case errors.As(err, &changed):
		e.skip(p, changed.msg+"; run sync again")
	default:
		e.res.Failed++
		fmt.Fprintf(e.opts.Out, "✗ %-9s %s: %v\n", "failed", p, err)
	}
	return false
}

func (e *executor) skip(p, why string) {
	e.res.Skipped++
	fmt.Fprintf(e.opts.Out, "  %-9s %s — %s\n", "skip", p, why)
}

func (e *executor) printDownload(a Action) {
	fmt.Fprintf(e.opts.Out, "↓ %-9s %s%s\n", "download", a.Path, paren(a.Note))
}

func (e *executor) printUpload(a Action) {
	fmt.Fprintf(e.opts.Out, "↑ %-9s %s%s\n", "upload", a.Path, paren(a.Note))
}

func (e *executor) printConflict(a Action) {
	switch a.Resolution {
	case Unresolved:
		r := a.Remote
		when := r.ModifiedTime.In(e.opts.Now.Location()).Format("2006-01-02 15:04")
		if r.ModifiedBy != "" {
			when = "by " + r.ModifiedBy + ", " + when
		}
		fmt.Fprintf(e.opts.Out, "⚠ %-9s %s — %s (%s). Not uploaded.\n", "conflict", a.Path, a.Note, when)
	case KeepDriveCopy:
		fmt.Fprintf(e.opts.Out, "⚠ %-9s %s → %s (%s; Drive version kept as the copy)\n", "conflict", a.Path, path.Base(a.Copy), a.Note)
	default:
		fmt.Fprintf(e.opts.Out, "⚠ %-9s %s → %s (%s; local version kept as the copy)\n", "conflict", a.Path, path.Base(a.Copy), a.Note)
	}
}

func paren(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func (e *executor) path(p string) string {
	return filepath.Join(e.dir, filepath.FromSlash(p))
}

// checkLocal returns a changedError unless the local file at p is still as
// scanned (want), or still missing if want is nil.
func (e *executor) checkLocal(p string, want *LocalFile) error {
	fi, err := os.Lstat(e.path(p))
	switch {
	case want == nil && errors.Is(err, fs.ErrNotExist):
		return nil
	case want == nil && err == nil:
		return &changedError{"appeared locally during the sync"}
	case err != nil:
		if errors.Is(err, fs.ErrNotExist) {
			return &changedError{"deleted locally during the sync"}
		}
		return err
	case !fi.Mode().IsRegular() || fi.Size() != want.Size || !fi.ModTime().Equal(want.MTime):
		return &changedError{"changed locally during the sync"}
	}
	return nil
}

// checkRemote returns a changedError unless the Drive file is still as
// scanned.
func (e *executor) checkRemote(want RemoteFile) error {
	cur, err := e.d.Get(e.ctx, want.ID)
	if errors.Is(err, gdrive.ErrNoAccess) || err == nil && cur.Trashed {
		return &changedError{"deleted on Drive during the sync"}
	}
	if err != nil {
		return err
	}
	if cur.MD5 != want.MD5 {
		return &changedError{"changed on Drive during the sync"}
	}
	return nil
}

// download writes the Drive file r to p through a temporary file in the same
// folder, checking its MD5 before renaming it into place. want is the local
// file it replaces, or nil.
func (e *executor) download(p string, r RemoteFile, want *LocalFile) (err error) {
	if err := e.checkLocal(p, want); err != nil {
		return err
	}
	dst := e.path(p)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if want != nil {
		if fi, err := os.Stat(dst); err == nil {
			perm = fi.Mode().Perm()
		}
	}

	body, err := e.d.Download(e.ctx, r.ID)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), tempPrefix+"*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	h := md5.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), body); err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != r.MD5 {
		return fmt.Errorf("downloaded content doesn't match Drive's checksum (got %s, want %s)", sum, r.MD5)
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := e.checkLocal(p, want); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	fi, err := os.Lstat(dst)
	if err != nil {
		return err
	}
	e.setFile(p, r.ID, r.MD5, fi.Size(), fi.ModTime())
	return e.save(e.st)
}

// upload sends the local file at p to Drive: as a new version of r, or as a
// new file if r is nil. It checks the MD5 Drive reports against the bytes
// sent.
func (e *executor) upload(p string, r *RemoteFile) error {
	if r != nil {
		if err := e.checkRemote(*r); err != nil {
			return err
		}
	}
	f, err := os.Open(e.path(p))
	if errors.Is(err, fs.ErrNotExist) {
		return &changedError{"deleted locally during the sync"}
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return &changedError{"no longer a regular file"}
	}

	content := &hashReader{f: f, h: md5.New()}
	var res gdrive.File
	if r != nil {
		res, err = e.d.Update(e.ctx, r.ID, content)
	} else {
		var parent string
		if parent, err = e.folderID(path.Dir(p)); err == nil {
			res, err = e.d.Create(e.ctx, parent, path.Base(p), content)
		}
	}
	if err != nil {
		return err
	}
	if sum := content.sum(); res.MD5 != sum {
		return fmt.Errorf("Drive's checksum after upload doesn't match the file (got %s, want %s)", res.MD5, sum)
	}
	e.setFile(p, res.ID, res.MD5, fi.Size(), fi.ModTime())
	return e.save(e.st)
}

// racyWindow is how much older than the start of the run a file's mtime
// must be for the state to remember it. A file changed within the file
// system's timestamp granularity of being hashed or written could keep its
// mtime; leaving the mtime out makes the next scan hash it again.
const racyWindow = 2 * time.Second

// setFile records the synced version of p.
func (e *executor) setFile(p, id, sum string, size int64, mtime time.Time) {
	if !mtime.Before(e.opts.Now.Add(-racyWindow)) {
		mtime = time.Time{}
	}
	e.st.Files[p] = state.File{ID: id, MD5: sum, Size: size, MTime: mtime}
}

// folderID returns the ID of the Drive folder at the slash-separated path
// dir, creating it and its parents if needed.
func (e *executor) folderID(dir string) (string, error) {
	if dir == "." || dir == "" {
		return e.st.Folder, nil
	}
	if id, ok := e.remote.Dirs[dir]; ok {
		return id, nil
	}
	parent, err := e.folderID(path.Dir(dir))
	if err != nil {
		return "", err
	}
	f, err := e.d.CreateFolder(e.ctx, parent, path.Base(dir))
	if err != nil {
		return "", err
	}
	e.remote.Dirs[dir] = f.ID
	e.st.Folders[dir] = f.ID
	return f.ID, nil
}

// keepLocalCopy renames the local file to the conflict copy, then downloads
// the Drive version under the original name. renamed reports whether the
// rename happened, even if the download then failed.
func (e *executor) keepLocalCopy(a Action) (renamed bool, err error) {
	if err := e.checkLocal(a.Path, a.Local); err != nil {
		return false, err
	}
	if _, err := os.Lstat(e.path(a.Copy)); !errors.Is(err, fs.ErrNotExist) {
		return false, &changedError{path.Base(a.Copy) + " appeared locally during the sync"}
	}
	if err := os.Rename(e.path(a.Path), e.path(a.Copy)); err != nil {
		return false, err
	}
	return true, e.download(a.Path, *a.Remote, nil)
}

// keepDriveCopy renames the Drive file to the conflict copy, then uploads
// the local file under the original name.
func (e *executor) keepDriveCopy(a Action) error {
	if err := e.checkRemote(*a.Remote); err != nil {
		return err
	}
	if _, err := e.d.Rename(e.ctx, a.Remote.ID, path.Base(a.Copy)); err != nil {
		return err
	}
	return e.upload(a.Path, nil)
}

// hashReader hashes what is read from f. Rewinding it, as the Drive client
// does before retrying an upload, starts the hash over.
type hashReader struct {
	f *os.File
	h hash.Hash
}

func (r *hashReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	r.h.Write(p[:n])
	return n, err
}

func (r *hashReader) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errors.New("upload content can only be rewound to the start")
	}
	r.h.Reset()
	return r.f.Seek(0, io.SeekStart)
}

func (r *hashReader) sum() string { return hex.EncodeToString(r.h.Sum(nil)) }
