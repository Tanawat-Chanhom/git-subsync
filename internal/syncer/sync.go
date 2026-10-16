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
	"sync"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

// Options control one sync run.
type Options struct {
	Mode     Mode
	Force    bool
	DryRun   bool      // print the plan without changing anything
	Now      time.Time // local time; names conflict copies
	Jobs     int       // files transferred, and Drive folders listed, at once; 0 means 1
	Out      io.Writer // one line per action
	Progress Progress  // optional
}

// Progress follows a sync while it runs, e.g. to draw a progress bar. Sync
// calls it from several goroutines at once.
type Progress interface {
	// Scanned reports how many files the scans have found so far.
	Scanned(local, remote int)
	// Planned reports the transfers the sync will make and their total
	// size. Scanning is over.
	Planned(transfers int, bytes int64)
	// Start begins the transfer of a file of size bytes; up is true for an
	// upload.
	Start(path string, size int64, up bool) Transfer
}

// Transfer follows one file's download or upload.
type Transfer interface {
	// Add reports n more bytes transferred. n is negative when an upload
	// starts over.
	Add(n int64)
	// Done ends the transfer, whether it worked or not.
	Done()
}

type noProgress struct{}

func (noProgress) Scanned(int, int)                   {}
func (noProgress) Planned(int, int64)                 {}
func (noProgress) Start(string, int64, bool) Transfer { return noProgress{} }
func (noProgress) Add(int64)                          {}
func (noProgress) Done()                              {}

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
// updates st as files are synced and saves it with save as it goes (at most
// once a second) and at the end, so an interrupted run can be repeated.
// Failed actions are reported and counted in the result; the error is for
// failures that stop the whole run.
func Sync(ctx context.Context, d gdrive.Drive, dir string, st *state.State, save func(*state.State) error, opts Options) (Result, error) {
	prog := opts.Progress
	if prog == nil {
		prog = noProgress{}
	}
	jobs := max(opts.Jobs, 1)

	// Scan both sides at once.
	var (
		mu              sync.Mutex
		nLocal, nRemote int
	)
	scanned := func(local, remote int) {
		mu.Lock()
		defer mu.Unlock()
		nLocal, nRemote = nLocal+local, nRemote+remote
		prog.Scanned(nLocal, nRemote)
	}
	var (
		local    Local
		localErr error
		scanDone = make(chan struct{})
	)
	go func() {
		defer close(scanDone)
		local, localErr = ScanLocal(dir, st.Files, func() { scanned(1, 0) })
	}()
	remote, err := ScanRemote(ctx, d, st.Folder, jobs, func() { scanned(0, 1) })
	<-scanDone
	if localErr != nil {
		return Result{}, fmt.Errorf("scanning %s: %w", dir, localErr)
	}
	if err != nil {
		return Result{}, err
	}
	actions := Plan(local, remote, st.Files, opts.Mode, opts.Force, opts.Now)

	e := &executor{ctx: ctx, d: d, dir: dir, st: st, save: save, remote: remote, opts: opts, jobs: jobs, prog: prog, res: Result{DryRun: opts.DryRun}}
	if opts.DryRun {
		prog.Planned(0, 0)
		e.dryRun(actions)
		return e.res, nil
	}
	prog.Planned(transfers(actions))
	return e.res, e.run(local, actions)
}

// transfers counts the downloads and uploads actions will make, and their
// total size.
func transfers(actions []Action) (n int, bytes int64) {
	for _, a := range actions {
		switch {
		case a.Kind == KindDownload:
			n, bytes = n+1, bytes+a.Remote.Size
		case a.Kind == KindUpload:
			n, bytes = n+1, bytes+a.Local.Size
		case a.Kind != KindConflict:
		case a.Resolution == KeepBoth:
			n, bytes = n+2, bytes+a.Remote.Size+a.Local.Size
		case a.Resolution == KeepLocalCopy:
			n, bytes = n+1, bytes+a.Remote.Size
		case a.Resolution == KeepDriveCopy:
			n, bytes = n+1, bytes+a.Local.Size
		}
	}
	return n, bytes
}

type executor struct {
	ctx  context.Context
	d    gdrive.Drive
	dir  string
	save func(*state.State) error
	opts Options
	jobs int
	prog Progress

	// mu guards these while transfers run in parallel. Methods that print
	// or count expect it held.
	mu       sync.Mutex
	st       *state.State
	res      Result
	dirty    bool      // st has changes that aren't saved
	lastSave time.Time // when st was last saved
	renamed  map[string]bool

	// folderMu serializes creating Drive folders, so two uploads into a
	// new folder don't create it twice. It guards remote.
	folderMu sync.Mutex
	remote   Remote // Dirs grows as folders are created on Drive
}

// changedError means a file changed between the scan and the action; the
// next sync will pick it up.
type changedError struct{ msg string }

func (e *changedError) Error() string { return e.msg }

func (e *executor) dryRun(actions []Action) {
	ph := phases(actions)
	conflict := func(a Action) {
		e.printConflict(a)
		if a.Resolution == Unresolved {
			e.res.Unresolved = append(e.res.Unresolved, a.Path)
		} else {
			e.res.Conflicts++
		}
	}
	for _, a := range ph.local {
		if a.Kind == KindDownload {
			e.res.Downloaded++
			e.printDownload(a)
		} else {
			conflict(a)
		}
	}
	for _, a := range ph.drive {
		switch {
		case a.Kind == KindUpload:
			e.res.Uploaded++
			e.printUpload(a)
		case a.Resolution == KeepDriveCopy: // KeepBoth was listed with the local phase
			conflict(a)
		}
	}
	for _, a := range ph.unresolved {
		conflict(a)
	}
	for _, a := range ph.skips {
		e.skip(a.Path, a.Note)
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
	if err := e.saveNow(); err != nil {
		return err
	}

	// Downloads and other local writes first, then writes to Drive; each
	// phase transfers up to e.jobs files at once.
	ph := phases(actions)
	e.renamed = map[string]bool{} // conflicts whose local file became the copy
	e.parallel(ph.local, e.runLocal)
	if e.ctx.Err() == nil {
		e.parallel(ph.drive, e.runDrive)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.ctx.Err()
	if err == nil {
		for _, a := range ph.unresolved {
			e.res.Unresolved = append(e.res.Unresolved, a.Path)
			e.printConflict(a)
		}
		for _, a := range ph.skips {
			e.skip(a.Path, a.Note)
		}
		e.st.LastSync = e.opts.Now
	}
	if serr := e.saveNow(); err == nil {
		err = serr
	}
	return err
}

// runLocal runs a phase 1 action: a download, or the local half of a
// conflict.
func (e *executor) runLocal(a Action) {
	if a.Kind == KindDownload {
		err := e.download(a.Path, *a.Remote, a.Local)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.report(a.Path, err) {
			e.res.Downloaded++
			e.printDownload(a)
		}
		return
	}
	ok, err := e.keepLocalCopy(a)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.renamed[a.Path] = ok
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

// runDrive runs a phase 2 action: an upload, or the Drive half of a
// conflict.
func (e *executor) runDrive(a Action) {
	switch {
	case a.Kind == KindUpload:
		err := e.upload(a.Path, a.Remote)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.report(a.Path, err) {
			e.res.Uploaded++
			e.printUpload(a)
		}
	case a.Resolution == KeepBoth:
		e.mu.Lock()
		renamed := e.renamed[a.Path]
		e.mu.Unlock()
		if renamed {
			err := e.upload(a.Copy, nil)
			e.mu.Lock()
			defer e.mu.Unlock()
			e.report(a.Copy, err)
		}
	case a.Resolution == KeepDriveCopy:
		err := e.keepDriveCopy(a)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.report(a.Path, err) {
			e.res.Conflicts++
			e.printConflict(a)
		}
	}
}

// parallel calls fn for each action, up to e.jobs at a time, and waits for
// them. Once the context is canceled, it starts no more.
func (e *executor) parallel(actions []Action, fn func(Action)) {
	sem := make(chan struct{}, e.jobs)
	var wg sync.WaitGroup
	for _, a := range actions {
		sem <- struct{}{}
		if e.ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(a)
		}()
	}
	wg.Wait()
}

// actionPhases splits a plan in the order a run executes it.
type actionPhases struct {
	local      []Action // downloads and the local half of conflicts
	drive      []Action // uploads and the Drive half of conflicts
	unresolved []Action // conflicts left alone
	skips      []Action
}

func phases(actions []Action) actionPhases {
	var ph actionPhases
	for _, a := range actions {
		switch {
		case a.Kind == KindDownload:
			ph.local = append(ph.local, a)
		case a.Kind == KindUpload:
			ph.drive = append(ph.drive, a)
		case a.Kind == KindSkip:
			ph.skips = append(ph.skips, a)
		case a.Kind != KindConflict:
		case a.Resolution == Unresolved:
			ph.unresolved = append(ph.unresolved, a)
		case a.Resolution == KeepBoth:
			ph.local = append(ph.local, a)
			ph.drive = append(ph.drive, a)
		case a.Resolution == KeepLocalCopy:
			ph.local = append(ph.local, a)
		case a.Resolution == KeepDriveCopy:
			ph.drive = append(ph.drive, a)
		}
	}
	return ph
}

// report prints a failure or a skip for err, and reports whether err is
// nil.
func (e *executor) report(p string, err error) bool {
	var changed *changedError
	switch {
	case err == nil:
		return true
	case e.ctx.Err() != nil && errors.Is(err, e.ctx.Err()):
		// Interrupted: not a failure of this file. The run reports it.
	case errors.As(err, &changed):
		e.skip(p, changed.msg+"; run sync again")
	default:
		e.res.Failed++
		fmt.Fprintf(e.opts.Out, "❌ %-9s %s: %v\n", "failed", p, err)
	}
	return false
}

func (e *executor) skip(p, why string) {
	e.res.Skipped++
	fmt.Fprintf(e.opts.Out, "⏩ %-9s %s — %s\n", "skip", p, why)
}

func (e *executor) printDownload(a Action) {
	fmt.Fprintf(e.opts.Out, "📥 %-9s %s%s\n", "download", a.Path, paren(a.Note))
}

func (e *executor) printUpload(a Action) {
	fmt.Fprintf(e.opts.Out, "📤 %-9s %s%s\n", "upload", a.Path, paren(a.Note))
}

func (e *executor) printConflict(a Action) {
	switch a.Resolution {
	case Unresolved:
		r := a.Remote
		when := r.ModifiedTime.In(e.opts.Now.Location()).Format("2006-01-02 15:04")
		if r.ModifiedBy != "" {
			when = "by " + r.ModifiedBy + ", " + when
		}
		fmt.Fprintf(e.opts.Out, "🛑 %-9s %s — %s (%s). Not uploaded.\n", "conflict", a.Path, a.Note, when)
	case KeepDriveCopy:
		fmt.Fprintf(e.opts.Out, "🔀 %-9s %s → %s (%s; Drive version kept as the copy)\n", "conflict", a.Path, path.Base(a.Copy), a.Note)
	default:
		fmt.Fprintf(e.opts.Out, "🔀 %-9s %s → %s (%s; local version kept as the copy)\n", "conflict", a.Path, path.Base(a.Copy), a.Note)
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
	t := e.prog.Start(p, r.Size, false)
	defer t.Done()
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
	if _, err := io.Copy(io.MultiWriter(tmp, h, progressWriter{t}), body); err != nil {
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
	return e.record(p, r.ID, r.MD5, fi.Size(), fi.ModTime())
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

	t := e.prog.Start(p, fi.Size(), true)
	defer t.Done()
	content := &hashReader{f: f, h: md5.New(), prog: t}
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
	return e.record(p, res.ID, res.MD5, fi.Size(), fi.ModTime())
}

// racyWindow is how much older than the start of the run a file's mtime
// must be for the state to remember it. A file changed within the file
// system's timestamp granularity of being hashed or written could keep its
// mtime; leaving the mtime out makes the next scan hash it again.
const racyWindow = 2 * time.Second

// setFile records the synced version of p; e.mu must be held while
// transfers run.
func (e *executor) setFile(p, id, sum string, size int64, mtime time.Time) {
	if !mtime.Before(e.opts.Now.Add(-racyWindow)) {
		mtime = time.Time{}
	}
	e.st.Files[p] = state.File{ID: id, MD5: sum, Size: size, MTime: mtime}
}

// saveEvery limits how often the state file is written while transfers
// run. A run interrupted before saving is still safe to repeat: files that
// already match on both sides are only recorded.
const saveEvery = time.Second

// record notes the synced version of p and saves the state if the last
// save was long enough ago.
func (e *executor) record(p, id, sum string, size int64, mtime time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setFile(p, id, sum, size, mtime)
	e.dirty = true
	if time.Since(e.lastSave) < saveEvery {
		return nil
	}
	return e.saveNow()
}

// saveNow writes the state; e.mu must be held while transfers run.
func (e *executor) saveNow() error {
	if err := e.save(e.st); err != nil {
		return err
	}
	e.dirty, e.lastSave = false, time.Now()
	return nil
}

// folderID returns the ID of the Drive folder at the slash-separated path
// dir, creating it and its parents if needed.
func (e *executor) folderID(dir string) (string, error) {
	e.folderMu.Lock()
	defer e.folderMu.Unlock()
	return e.folderIDLocked(dir)
}

func (e *executor) folderIDLocked(dir string) (string, error) {
	if dir == "." || dir == "" {
		return e.st.Folder, nil
	}
	if id, ok := e.remote.Dirs[dir]; ok {
		return id, nil
	}
	parent, err := e.folderIDLocked(path.Dir(dir))
	if err != nil {
		return "", err
	}
	f, err := e.d.CreateFolder(e.ctx, parent, path.Base(dir))
	if err != nil {
		return "", err
	}
	e.remote.Dirs[dir] = f.ID
	e.mu.Lock()
	e.st.Folders[dir] = f.ID
	e.dirty = true
	e.mu.Unlock()
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

// hashReader hashes what is read from f and reports it as progress.
// Rewinding it, as the Drive client does before retrying an upload, starts
// over.
type hashReader struct {
	f    *os.File
	h    hash.Hash
	prog Transfer
	read int64
}

func (r *hashReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	r.h.Write(p[:n])
	r.read += int64(n)
	r.prog.Add(int64(n))
	return n, err
}

func (r *hashReader) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errors.New("upload content can only be rewound to the start")
	}
	r.h.Reset()
	r.prog.Add(-r.read)
	r.read = 0
	return r.f.Seek(0, io.SeekStart)
}

// progressWriter reports what is written to it as progress.
type progressWriter struct{ prog Transfer }

func (w progressWriter) Write(p []byte) (int, error) {
	w.prog.Add(int64(len(p)))
	return len(p), nil
}

func (r *hashReader) sum() string { return hex.EncodeToString(r.h.Sum(nil)) }
