package syncer

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive/drivetest"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

// env is a local folder and a Drive folder synced through a real state
// store.
type env struct {
	t     *testing.T
	d     *drivetest.Fake
	root  string // Drive folder ID
	dir   string
	store *state.Store
	now   time.Time // of the last run
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := drivetest.New()
	tmp := t.TempDir()
	return &env{
		t:     t,
		d:     d,
		root:  d.AddFolder("", "Shared"),
		dir:   filepath.Join(tmp, "work"),
		store: state.NewStore(filepath.Join(tmp, "state"), "work"),
	}
}

func (e *env) sync(mode Mode, force, dryRun bool) (Result, string) {
	e.t.Helper()
	st, err := e.store.Load()
	if err != nil {
		e.t.Fatal(err)
	}
	if st == nil {
		st = state.New(e.root)
	}
	var out bytes.Buffer
	e.now = time.Now()
	res, err := Sync(context.Background(), e.d, e.dir, st, e.store.Save, Options{Mode: mode, Force: force, DryRun: dryRun, Now: e.now, Out: &out})
	if err != nil {
		e.t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	return res, out.String()
}

// copyName is the conflict copy name the last run used for p.
func (e *env) copyName(p string) string { return CopyName(p, e.now, nil) }

func (e *env) write(p, content string) {
	e.t.Helper()
	write(e.t, filepath.Join(e.dir, filepath.FromSlash(p)), content)
}

func (e *env) remove(p string) {
	e.t.Helper()
	if err := os.Remove(filepath.Join(e.dir, filepath.FromSlash(p))); err != nil {
		e.t.Fatal(err)
	}
}

// localTree returns path → content of every local file, folders as
// "path/" → "".
func (e *env) localTree() map[string]string {
	e.t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(e.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == e.dir {
			return err
		}
		rel, _ := filepath.Rel(e.dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			tree[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		tree[rel] = string(data)
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.t.Fatal(err)
	}
	return tree
}

func (e *env) find(p string) string {
	e.t.Helper()
	id, ok := e.d.Find(e.root, p)
	if !ok {
		e.t.Fatalf("%s not on Drive", p)
	}
	return id
}

func assertTree(t *testing.T, side string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s tree:\n got  %v\n want %v", side, got, want)
	}
}

// assertNoop checks that another run changes nothing and says so.
func (e *env) assertNoop(mode Mode) {
	e.t.Helper()
	writes, local := e.d.Writes(), e.localTree()
	res, out := e.sync(mode, false, false)
	if e.d.Writes() != writes || !reflect.DeepEqual(e.localTree(), local) {
		e.t.Fatalf("second run changed something:\n%s", out)
	}
	if res.Downloaded+res.Uploaded+res.Conflicts+res.Failed+len(res.Unresolved) != 0 {
		e.t.Fatalf("second run: %+v\n%s", res, out)
	}
}

func TestSyncFirstDownload(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "a.txt", "a", "Somchai")
	docs := e.d.AddFolder(e.root, "docs")
	e.d.AddFile(docs, "b.txt", "b", "")
	e.d.AddGoogleDoc(docs, "Plan")

	res, out := e.sync(Both, false, false)
	want := "↓ download  a.txt\n↓ download  docs/b.txt\n  skip      docs/Plan — Google Docs file\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant\n%s", out, want)
	}
	if res.Summary() != "2 downloaded, 1 skipped" {
		t.Fatalf("summary = %q", res.Summary())
	}
	assertTree(t, "local", e.localTree(), map[string]string{"a.txt": "a", "docs/": "", "docs/b.txt": "b"})
	if e.d.Writes() != 0 {
		t.Fatalf("Drive writes = %d", e.d.Writes())
	}

	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Files["docs/b.txt"].MD5 == "" || st.Folders["docs"] != docs || st.LastSync.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	e.assertNoop(Both)
}

func TestSyncFirstUploadCreatesFolders(t *testing.T) {
	e := newEnv(t)
	e.write("a.txt", "a")
	e.write("x/y/z.txt", "z")

	res, out := e.sync(Both, false, false)
	if res.Uploaded != 2 || out != "↑ upload    a.txt\n↑ upload    x/y/z.txt\n" {
		t.Fatalf("res = %+v\n%s", res, out)
	}
	assertTree(t, "Drive", e.d.Tree(e.root), map[string]string{"a.txt": "a", "x/": "", "x/y/": "", "x/y/z.txt": "z"})
	e.assertNoop(Both)
}

func TestSyncBothModeEdits(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "remote.txt", "r1", "")
	e.write("local.txt", "l1")
	e.sync(Both, false, false)

	e.d.SetContent(e.find("remote.txt"), "r2", "Somchai")
	e.write("local.txt", "l2")
	idBefore := e.find("local.txt")
	res, out := e.sync(Both, false, false)
	if res.Downloaded != 1 || res.Uploaded != 1 {
		t.Fatalf("res = %+v\n%s", res, out)
	}
	assertTree(t, "local", e.localTree(), map[string]string{"remote.txt": "r2", "local.txt": "l2"})
	assertTree(t, "Drive", e.d.Tree(e.root), map[string]string{"remote.txt": "r2", "local.txt": "l2"})
	if e.find("local.txt") != idBefore {
		t.Fatal("changed file was uploaded as a new Drive file instead of a new version")
	}
	e.assertNoop(Both)
}

func TestSyncBothModeConflict(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "file1.txt", "base", "")
	e.sync(Both, false, false)

	e.write("file1.txt", "mine")
	e.d.SetContent(e.find("file1.txt"), "theirs", "Somchai")
	res, out := e.sync(Both, false, false)
	c := e.copyName("file1.txt")
	want := "⚠ conflict  file1.txt → " + c + " (changed locally and on Drive; local version kept as the copy)\n"
	if out != want || res.Summary() != "1 conflict" {
		t.Fatalf("output:\n%s\nwant\n%s\nsummary %q", out, want, res.Summary())
	}
	both := map[string]string{"file1.txt": "theirs", c: "mine"}
	assertTree(t, "local", e.localTree(), both)
	assertTree(t, "Drive", e.d.Tree(e.root), both)
	e.assertNoop(Both)
}

func TestSyncDeletesAreNotPropagated(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "gone-locally.txt", "1", "")
	e.d.AddFile(e.root, "gone-on-drive.txt", "2", "")
	e.d.AddFile(e.root, "gone-both.txt", "3", "")
	e.sync(Both, false, false)

	e.remove("gone-locally.txt")
	e.remove("gone-both.txt")
	e.d.Trash(e.find("gone-on-drive.txt"))
	e.d.Trash(e.find("gone-both.txt"))
	_, out := e.sync(Both, false, false)
	want := "↓ download  gone-locally.txt (deleted locally; deletes aren't synced)\n" +
		"↑ upload    gone-on-drive.txt (deleted on Drive; deletes aren't synced)\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant\n%s", out, want)
	}
	tree := map[string]string{"gone-locally.txt": "1", "gone-on-drive.txt": "2"}
	assertTree(t, "local", e.localTree(), tree)
	assertTree(t, "Drive", e.d.Tree(e.root), tree)
	st, _ := e.store.Load()
	if _, ok := st.Files["gone-both.txt"]; ok {
		t.Fatal("file gone from both sides is still in the state")
	}
	e.assertNoop(Both)
}

func TestSyncDownloadModeNeverWritesDrive(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "shared.txt", "base", "")
	e.d.AddFile(e.root, "conflict.txt", "base", "")
	e.d.AddFile(e.root, "deleted-on-drive.txt", "x", "")
	e.sync(Download, false, false)

	e.d.SetContent(e.find("shared.txt"), "drive edit", "Somchai")
	e.write("conflict.txt", "mine")
	e.d.SetContent(e.find("conflict.txt"), "theirs", "Somchai")
	e.d.Trash(e.find("deleted-on-drive.txt"))
	e.write("new-local.txt", "new")
	e.d.AddFile(e.root, "new-remote.txt", "nr", "")

	res, out := e.sync(Download, false, false)
	c := e.copyName("conflict.txt")
	want := "⚠ conflict  conflict.txt → " + c + " (changed locally and on Drive; local version kept as the copy)\n" +
		"↓ download  new-remote.txt\n" +
		"↓ download  shared.txt\n" +
		"  skip      deleted-on-drive.txt — deleted on Drive; deletes aren't synced\n" +
		"  skip      new-local.txt — local only; download mode doesn't upload\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant\n%s", out, want)
	}
	if res.Summary() != "2 downloaded, 1 conflict, 2 skipped" {
		t.Fatalf("summary = %q", res.Summary())
	}
	if e.d.Writes() != 0 {
		t.Fatalf("download mode wrote to Drive %d times", e.d.Writes())
	}
	assertTree(t, "local", e.localTree(), map[string]string{
		"shared.txt": "drive edit", "conflict.txt": "theirs", c: "mine",
		"deleted-on-drive.txt": "x", "new-local.txt": "new", "new-remote.txt": "nr",
	})

	// The local-only files stay skipped; nothing else happens.
	res, _ = e.sync(Download, true, false)
	if res.Skipped != 3 || res.Downloaded+res.Conflicts != 0 || e.d.Writes() != 0 {
		t.Fatalf("second run: %+v", res)
	}
}

func TestSyncUploadModeNeverWritesLocal(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "shared.txt", "base", "")
	docs := e.d.AddFolder(e.root, "docs")
	e.d.AddFile(docs, "file1.txt", "base", "")
	e.write("shared.txt", "base")
	e.write("docs/file1.txt", "base")
	e.write("deleted-locally.txt", "d")
	e.sync(Upload, false, false)
	if e.d.Tree(e.root)["deleted-locally.txt"] != "d" {
		t.Fatal("first upload-mode sync did not upload")
	}

	e.write("shared.txt", "local edit")
	e.write("docs/file1.txt", "mine")
	e.d.SetContent(e.find("docs/file1.txt"), "theirs", "Somchai")
	e.remove("deleted-locally.txt")
	e.d.AddFile(e.root, "new-remote.txt", "nr", "")
	e.write("new-local.txt", "nl")
	localBefore := e.localTree()

	res, out := e.sync(Upload, false, false)
	when := e.d.File(e.find("docs/file1.txt")).ModifiedTime.In(e.now.Location()).Format("2006-01-02 15:04")
	want := "↑ upload    new-local.txt\n" +
		"↑ upload    shared.txt\n" +
		"⚠ conflict  docs/file1.txt — changed locally and on Drive (by Somchai, " + when + "). Not uploaded.\n" +
		"  skip      deleted-locally.txt — deleted locally; deletes aren't synced\n" +
		"  skip      new-remote.txt — only on Drive; upload mode doesn't download\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant\n%s", out, want)
	}
	if res.Summary() != "2 uploaded, 1 conflict unresolved, 2 skipped" || !reflect.DeepEqual(res.Unresolved, []string{"docs/file1.txt"}) {
		t.Fatalf("summary = %q, unresolved = %v", res.Summary(), res.Unresolved)
	}
	assertTree(t, "local", e.localTree(), localBefore)
	if got := e.d.Tree(e.root)["docs/file1.txt"]; got != "theirs" {
		t.Fatalf("conflicting file was uploaded: Drive has %q", got)
	}

	// The conflict stays until --force; the state is unchanged meanwhile.
	res, _ = e.sync(Upload, false, false)
	if len(res.Unresolved) != 1 || res.Uploaded != 0 {
		t.Fatalf("rerun: %+v", res)
	}

	res, out = e.sync(Upload, true, false)
	c := e.copyName("docs/file1.txt")
	want = "⚠ conflict  docs/file1.txt → " + filepath.Base(c) + " (changed locally and on Drive; Drive version kept as the copy)\n" +
		"  skip      deleted-locally.txt — deleted locally; deletes aren't synced\n" +
		"  skip      new-remote.txt — only on Drive; upload mode doesn't download\n"
	if out != want || len(res.Unresolved) != 0 || res.Conflicts != 1 {
		t.Fatalf("--force output:\n%s\nwant\n%s", out, want)
	}
	drive := e.d.Tree(e.root)
	if drive["docs/file1.txt"] != "mine" || drive[c] != "theirs" {
		t.Fatalf("Drive after --force: %v", drive)
	}
	assertTree(t, "local", e.localTree(), localBefore)
}

// Making the local file match Drive resolves an upload-mode conflict.
func TestSyncUploadModeManualResolution(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "a.txt", "base", "")
	e.write("a.txt", "base")
	e.sync(Upload, false, false)
	e.write("a.txt", "mine")
	e.d.SetContent(e.find("a.txt"), "theirs", "Somchai")
	if res, _ := e.sync(Upload, false, false); len(res.Unresolved) != 1 {
		t.Fatalf("want a conflict, got %+v", res)
	}

	e.write("a.txt", "theirs")
	writes := e.d.Writes()
	res, out := e.sync(Upload, false, false)
	if len(res.Unresolved) != 0 || out != "" || e.d.Writes() != writes {
		t.Fatalf("res = %+v\n%s", res, out)
	}
	e.assertNoop(Upload)
}

func TestSyncFirstSyncWithDifferentFiles(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "same.txt", "same", "")
	e.d.AddFile(e.root, "diff.txt", "drive", "Somchai")
	e.write("same.txt", "same")
	e.write("diff.txt", "local")

	res, out := e.sync(Both, false, false)
	c := e.copyName("diff.txt")
	if res.Conflicts != 1 || !strings.Contains(out, "diff.txt → "+c+" (local and Drive versions differ;") {
		t.Fatalf("res = %+v\n%s", res, out)
	}
	both := map[string]string{"same.txt": "same", "diff.txt": "drive", c: "local"}
	assertTree(t, "local", e.localTree(), both)
	assertTree(t, "Drive", e.d.Tree(e.root), both)
	e.assertNoop(Both)
}

func TestSyncDryRunChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "remote.txt", "r", "")
	e.d.AddFile(e.root, "c.txt", "theirs", "")
	e.write("local.txt", "l")
	e.write("c.txt", "mine")

	res, out := e.sync(Both, false, true)
	c := e.copyName("c.txt")
	want := "⚠ conflict  c.txt → " + c + " (local and Drive versions differ; local version kept as the copy)\n" +
		"↓ download  remote.txt\n" +
		"↑ upload    local.txt\n"
	if out != want || res.Summary() != "Dry run: 1 to download, 1 to upload, 1 conflict" {
		t.Fatalf("output:\n%s\nwant\n%s\nsummary %q", out, want, res.Summary())
	}
	if e.d.Writes() != 0 || len(e.localTree()) != 2 {
		t.Fatalf("dry run changed something: writes %d, local %v", e.d.Writes(), e.localTree())
	}
	if st, _ := e.store.Load(); st != nil {
		t.Fatal("dry run saved state")
	}
}

func TestSyncRejectsCorruptDownload(t *testing.T) {
	e := newEnv(t)
	id := e.d.AddFile(e.root, "a.txt", "content", "")
	e.d.SetMD5(id, "0123456789abcdef0123456789abcdef")

	res, out := e.sync(Both, false, false)
	if res.Failed != 1 || !strings.Contains(out, "✗ failed    a.txt: downloaded content doesn't match Drive's checksum") {
		t.Fatalf("res = %+v\n%s", res, out)
	}
	if tree := e.localTree(); len(tree) != 0 {
		t.Fatalf("local = %v, want nothing (no partial or temp files)", tree)
	}
}

// A run that fails halfway can be repeated and finishes the job.
func TestSyncResumesAfterFailure(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"1.txt", "2.txt", "3.txt"} {
		e.d.AddFile(e.root, n, n, "")
	}
	fail := true
	e.d.Hook = func(op, id string) error {
		if op == "download" && fail && id == e.find("2.txt") {
			return errors.New("network down")
		}
		return nil
	}
	res, out := e.sync(Both, false, false)
	if res.Downloaded != 2 || res.Failed != 1 || !strings.Contains(out, "✗ failed    2.txt: network down") {
		t.Fatalf("res = %+v\n%s", res, out)
	}

	fail = false
	res, out = e.sync(Both, false, false)
	if res.Downloaded != 1 || res.Failed != 0 || out != "↓ download  2.txt\n" {
		t.Fatalf("rerun: %+v\n%s", res, out)
	}
	e.assertNoop(Both)
}

// Files that change between the scan and the transfer are skipped, never
// overwritten.
func TestSyncSkipsFilesChangedDuringSync(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "down.txt", "base", "")
	e.d.AddFile(e.root, "up.txt", "base", "")
	e.sync(Both, false, false)

	e.d.SetContent(e.find("down.txt"), "drive edit", "")
	e.write("up.txt", "local edit")
	e.d.Hook = func(op, id string) error {
		switch {
		case op == "download":
			e.write("down.txt", "local edit meanwhile")
		case op == "get" && id == e.find("up.txt"):
			e.d.SetContent(id, "drive edit meanwhile", "")
		}
		return nil
	}
	res, out := e.sync(Both, false, false)
	want := "  skip      down.txt — changed locally during the sync; run sync again\n" +
		"  skip      up.txt — changed on Drive during the sync; run sync again\n"
	if out != want || res.Skipped != 2 || res.Failed != 0 {
		t.Fatalf("output:\n%s\nwant\n%s", out, want)
	}
	if got := e.localTree()["down.txt"]; got != "local edit meanwhile" {
		t.Fatalf("down.txt = %q", got)
	}
	if got := e.d.Tree(e.root)["up.txt"]; got != "drive edit meanwhile" {
		t.Fatalf("Drive up.txt = %q", got)
	}

	// Both are now real conflicts, resolved without losing anything.
	e.d.Hook = nil
	if res, _ := e.sync(Both, false, false); res.Conflicts != 2 {
		t.Fatalf("rerun: %+v", res)
	}
}

func TestSyncRemovesLeftoverTempFiles(t *testing.T) {
	e := newEnv(t)
	e.write("docs/"+tempPrefix+"abc", "partial")
	e.write("docs/a.txt", "a")
	e.sync(Both, false, false)
	if _, ok := e.localTree()["docs/"+tempPrefix+"abc"]; ok {
		t.Fatal("temp file not removed")
	}
	if _, ok := e.d.Tree(e.root)["docs/"+tempPrefix+"abc"]; ok {
		t.Fatal("temp file uploaded")
	}
}

func TestSyncMissingLocalFolder(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "a.txt", "a", "")
	e.sync(Both, false, false)
	if err := os.RemoveAll(e.dir); err != nil {
		t.Fatal(err)
	}
	res, _ := e.sync(Both, false, false)
	if res.Downloaded != 1 || e.localTree()["a.txt"] != "a" {
		t.Fatalf("res = %+v, local = %v", res, e.localTree())
	}
}

// A file edited right after a sync keeps the same size, and on some file
// systems the same mtime. The next sync must still notice.
func TestSyncNoticesEditRightAfterSync(t *testing.T) {
	e := newEnv(t)
	e.d.AddFile(e.root, "a.txt", "aaaa", "")
	e.sync(Both, false, false)

	p := filepath.Join(e.dir, "a.txt")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	e.write("a.txt", "bbbb")
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil { // same mtime, as on a coarse clock
		t.Fatal(err)
	}
	res, _ := e.sync(Both, false, false)
	if res.Uploaded != 1 || e.d.Tree(e.root)["a.txt"] != "bbbb" {
		t.Fatalf("edit missed: %+v", res)
	}
}

func TestSummary(t *testing.T) {
	for _, tc := range []struct {
		r    Result
		want string
	}{
		{Result{}, "Up to date"},
		{Result{Skipped: 2}, "Up to date, 2 skipped"},
		{Result{Downloaded: 3, Uploaded: 1, Conflicts: 1, Skipped: 2}, "3 downloaded, 1 uploaded, 1 conflict, 2 skipped"},
		{Result{Uploaded: 3, Unresolved: []string{"a"}}, "3 uploaded, 1 conflict unresolved"},
		{Result{Conflicts: 2, Unresolved: []string{"a", "b"}, Failed: 1}, "2 conflicts, 2 conflicts unresolved, 1 failed"},
		{Result{DryRun: true}, "Dry run: up to date"},
	} {
		if got := tc.r.Summary(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.r, got, tc.want)
		}
	}
}
