package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive/drivetest"
)

const folderURL = "https://drive.google.com/drive/folders/"

// testRepo is a Git repo with git-subsync wired to a fake Drive.
type testRepo struct {
	t      *testing.T
	root   string
	d      *drivetest.Fake
	folder string // the Drive folder "Design"
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	root := strings.TrimSpace(git(t, dir, "rev-parse", "--show-toplevel"))
	t.Chdir(root)
	t.Setenv(auth.EnvConfigDir, t.TempDir())

	d := drivetest.New()
	r := &testRepo{t: t, root: root, d: d, folder: d.AddFolder("", "Design")}
	origDrive, origInteractive := openDrive, interactive
	openDrive = func(context.Context, *cobra.Command) (gdrive.Drive, gdrive.User, error) {
		return d, gdrive.User{DisplayName: "Me", Email: "me@example.com"}, nil
	}
	interactive = func() bool { return false }
	t.Cleanup(func() { openDrive, interactive = origDrive, origInteractive })
	return r
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// cli runs git-subsync and returns stdout, stderr (with the final error
// printed as main prints it) and the exit code.
func cli(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return cliWithInput(t, "", args...)
}

// cliWithInput is cli with stdin.
func cliWithInput(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	root := NewRootCmd()
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := run(context.Background(), root)
	if err != nil && err.Error() != "" {
		stderr.WriteString("git-subsync: " + err.Error() + "\n")
	}
	return stdout.String(), stderr.String(), ExitCode(err)
}

// mustCLI runs git-subsync and fails the test unless it exits with code.
func (r *testRepo) mustCLI(code int, args ...string) string {
	r.t.Helper()
	stdout, stderr, got := cli(r.t, args...)
	if got != code {
		r.t.Fatalf("git subsync %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), got, code, stdout, stderr)
	}
	return stdout
}

func (r *testRepo) write(p, content string) {
	r.t.Helper()
	full := filepath.Join(r.root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) read(p string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(p)))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

// files returns path → content of the files under the repo-relative dir.
func (r *testRepo) files(dir string) map[string]string {
	r.t.Helper()
	base := filepath.Join(r.root, filepath.FromSlash(dir))
	tree := map[string]string{}
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		data, err := os.ReadFile(p)
		tree[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return tree
}

// driveFiles returns the files in the Drive folder, without folders.
func (r *testRepo) driveFiles() map[string]string {
	tree := r.d.Tree(r.folder)
	for k := range tree {
		if strings.HasSuffix(k, "/") {
			delete(tree, k)
		}
	}
	return tree
}

func (r *testRepo) driveID(p string) string {
	r.t.Helper()
	id, ok := r.d.Find(r.folder, p)
	if !ok {
		r.t.Fatalf("%s not on Drive", p)
	}
	return id
}

// copyOf returns the one file in files whose name starts with stem_copy_.
func copyOf(t *testing.T, files map[string]string, stem string) string {
	t.Helper()
	var found []string
	for k := range files {
		if strings.HasPrefix(k, stem+"_copy_") {
			found = append(found, k)
		}
	}
	if len(found) != 1 {
		t.Fatalf("copies of %s: %v (files %v)", stem, found, files)
	}
	return found[0]
}

func TestAddSyncBothMode(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "readme.txt", "hello", "Somchai")
	docs := r.d.AddFolder(r.folder, "docs")
	r.d.AddFile(docs, "file1.txt", "base", "Somchai")

	// Commands work from any directory in the repo; paths are relative to
	// the root.
	r.write("src/main.go", "package main")
	t.Chdir(filepath.Join(r.root, "src"))

	out := r.mustCLI(0, "add", folderURL+r.folder+"?usp=sharing", "test-git-sync")
	want := "Added test-git-sync → Drive folder \"Design\" (mode: both)\n" +
		"↓ download  docs/file1.txt\n" +
		"↓ download  readme.txt\n" +
		"2 downloaded\n" +
		"Commit .gitsubsync and .gitignore so others can sync it with `git subsync sync`.\n"
	if out != want {
		t.Fatalf("add output:\n%s\nwant\n%s", out, want)
	}
	if got := r.files("test-git-sync"); !reflect.DeepEqual(got, map[string]string{"readme.txt": "hello", "docs/file1.txt": "base"}) {
		t.Fatalf("local files = %v", got)
	}
	if got, want := r.read(".gitsubsync"), "[subsync \"test-git-sync\"]\n\tpath = test-git-sync\n\tfolder = "+r.folder+"\n\tmode = both\n"; got != want {
		t.Fatalf(".gitsubsync =\n%s\nwant\n%s", got, want)
	}
	if got := r.read(".gitignore"); got != "# git-subsync\n/test-git-sync/\n" {
		t.Fatalf(".gitignore = %q", got)
	}
	if got := git(t, r.root, "status", "--porcelain", "--untracked-files=all"); got != "?? .gitignore\n?? .gitsubsync\n?? src/main.go\n" {
		t.Fatalf("git status:\n%s", got)
	}

	// Edit both sides.
	r.write("test-git-sync/readme.txt", "hello from local")
	r.d.SetContent(r.driveID("docs/file1.txt"), "edited on Drive", "Somchai")
	r.d.AddFile(r.folder, "new-on-drive.txt", "new", "Somchai")
	r.write("test-git-sync/new-local.txt", "local")
	out = r.mustCLI(0, "sync")
	want = "↓ download  docs/file1.txt\n↓ download  new-on-drive.txt\n↑ upload    new-local.txt\n↑ upload    readme.txt\n2 downloaded, 2 uploaded\n"
	if out != want {
		t.Fatalf("sync output:\n%s\nwant\n%s", out, want)
	}
	synced := map[string]string{
		"readme.txt": "hello from local", "docs/file1.txt": "edited on Drive",
		"new-on-drive.txt": "new", "new-local.txt": "local",
	}
	if got := r.files("test-git-sync"); !reflect.DeepEqual(got, synced) {
		t.Fatalf("local = %v", got)
	}
	if got := r.driveFiles(); !reflect.DeepEqual(got, synced) {
		t.Fatalf("Drive = %v", got)
	}

	// Nothing changed: nothing happens.
	writes := r.d.Writes()
	if out := r.mustCLI(0, "sync", "test-git-sync"); out != "Up to date\n" || r.d.Writes() != writes {
		t.Fatalf("second sync: %q, %d writes", out, r.d.Writes()-writes)
	}

	// A conflict leaves both versions on both sides.
	r.write("test-git-sync/docs/file1.txt", "mine")
	r.d.SetContent(r.driveID("docs/file1.txt"), "theirs", "Somchai")
	out = r.mustCLI(0, "sync")
	if !strings.HasPrefix(out, "⚠ conflict  docs/file1.txt → file1_copy_") || !strings.HasSuffix(out, "1 conflict\n") {
		t.Fatalf("conflict output:\n%s", out)
	}
	local := r.files("test-git-sync")
	c := copyOf(t, local, "docs/file1")
	if local["docs/file1.txt"] != "theirs" || local[c] != "mine" {
		t.Fatalf("local after conflict: %v", local)
	}
	if got := r.driveFiles(); !reflect.DeepEqual(got, local) {
		t.Fatalf("Drive = %v\nlocal = %v", got, local)
	}
	if out := r.mustCLI(0, "sync"); out != "Up to date\n" {
		t.Fatalf("sync after conflict: %q", out)
	}

	out = r.mustCLI(0, "list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "PATH ") || !strings.Contains(lines[1], "test-git-sync  both  Design") || strings.Contains(lines[1], "never") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestUploadModeConflict(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "file1.txt", "base", "Somchai")
	r.d.AddFile(r.folder, "drive-only.txt", "d", "Somchai")
	r.write("test-git-sync/file1.txt", "base")
	r.write("test-git-sync/other.txt", "o")

	out := r.mustCLI(0, "add", "--mode", "upload", "--force", r.folder, "test-git-sync")
	if !strings.Contains(out, "↑ upload    other.txt\n") || !strings.Contains(out, "skip      drive-only.txt — only on Drive; upload mode doesn't download\n") {
		t.Fatalf("add output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(r.root, "test-git-sync", "drive-only.txt")); !os.IsNotExist(err) {
		t.Fatal("upload mode downloaded a file")
	}

	r.write("test-git-sync/file1.txt", "mine")
	r.write("test-git-sync/other.txt", "o2")
	r.d.SetContent(r.driveID("file1.txt"), "theirs", "Somchai")
	when := r.d.File(r.driveID("file1.txt")).ModifiedTime.Local().Format("2006-01-02 15:04")
	localBefore := r.files("test-git-sync")

	out = r.mustCLI(2, "sync", "test-git-sync")
	want := "↑ upload    other.txt\n" +
		"⚠ conflict  file1.txt — changed locally and on Drive (by Somchai, " + when + "). Not uploaded.\n" +
		"  skip      drive-only.txt — only on Drive; upload mode doesn't download\n" +
		"1 uploaded, 1 conflict unresolved, 1 skipped\n" +
		"To upload your version, run: git subsync sync test-git-sync --force\n" +
		"(the Drive version will be kept as file1_copy_<YYYYMMDD-HHMMSS>.txt)\n"
	if out != want {
		t.Fatalf("sync output:\n%s\nwant\n%s", out, want)
	}
	if got := r.driveFiles()["file1.txt"]; got != "theirs" {
		t.Fatalf("conflicting file uploaded: Drive has %q", got)
	}
	if got := r.files("test-git-sync"); !reflect.DeepEqual(got, localBefore) {
		t.Fatalf("upload mode changed local files: %v", got)
	}

	// --dry-run shows the conflict but exits 0.
	if out := r.mustCLI(0, "sync", "--dry-run"); !strings.Contains(out, "Dry run: 1 conflict unresolved, 1 skipped") {
		t.Fatalf("dry run:\n%s", out)
	}

	out = r.mustCLI(0, "sync", "test-git-sync", "--force")
	if !strings.HasPrefix(out, "⚠ conflict  file1.txt → file1_copy_") || !strings.Contains(out, "Drive version kept as the copy") {
		t.Fatalf("--force output:\n%s", out)
	}
	drive := r.driveFiles()
	if c := copyOf(t, drive, "file1"); drive["file1.txt"] != "mine" || drive[c] != "theirs" {
		t.Fatalf("Drive after --force: %v", drive)
	}
	if got := r.files("test-git-sync"); !reflect.DeepEqual(got, localBefore) {
		t.Fatalf("upload mode changed local files: %v", got)
	}
}

func TestDownloadModeNeverWritesDrive(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "a.txt", "a", "Somchai")
	r.mustCLI(0, "add", "--mode", "download", folderURL+r.folder, "dl")

	r.write("dl/a.txt", "mine")
	r.d.SetContent(r.driveID("a.txt"), "theirs", "Somchai")
	r.write("dl/local-only.txt", "x")
	stdout, stderr, code := cli(t, "sync", "--force")
	if code != 0 || !strings.Contains(stderr, "note: --force has no effect in download mode (dl)") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	local := r.files("dl")
	if c := copyOf(t, local, "a"); local["a.txt"] != "theirs" || local[c] != "mine" {
		t.Fatalf("local = %v", local)
	}
	if r.d.Writes() != 0 {
		t.Fatalf("download mode wrote to Drive %d times", r.d.Writes())
	}
}

// A teammate clones the repo and runs sync: the folder is created and
// downloaded.
func TestSyncFreshClone(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "a.txt", "a", "Somchai")
	r.mustCLI(0, "add", r.folder, "shared/design")
	git(t, r.root, "add", ".gitsubsync", ".gitignore")
	git(t, r.root, "commit", "-q", "-m", "Add design folder")

	clone := filepath.Join(t.TempDir(), "clone")
	git(t, r.root, "clone", "-q", r.root, clone)
	t.Chdir(clone)
	r.root = strings.TrimSpace(git(t, clone, "rev-parse", "--show-toplevel"))
	stdout, stderr, code := cli(t, "sync")
	if code != 0 || stdout != "↓ download  a.txt\n1 downloaded\n" || !strings.Contains(stderr, "Creating shared/design/") {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if got := r.read("shared/design/a.txt"); got != "a" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := git(t, r.root, "status", "--porcelain", "--untracked-files=all"); got != "" {
		t.Fatalf("git status in clone:\n%s", got)
	}
}

func TestRemove(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "a.txt", "a", "")
	r.write(".gitignore", "/dist\n")
	r.mustCLI(0, "add", r.folder, "keep")
	r.mustCLI(0, "add", "--no-sync", r.folder, "drop")
	if out := r.mustCLI(0, "list"); !strings.Contains(out, "drop  both  Design        never") {
		t.Fatalf("list:\n%s", out)
	}
	r.mustCLI(0, "sync", "drop")
	writes := r.d.Writes()

	out := r.mustCLI(0, "remove", "keep")
	if !strings.Contains(out, "Removed keep. Its files are still there") {
		t.Fatalf("remove output:\n%s", out)
	}
	if got := r.read("keep/a.txt"); got != "a" {
		t.Fatalf("local file not kept: %q", got)
	}
	if got := r.read(".gitignore"); got != "/dist\n\n# git-subsync\n/drop/\n" {
		t.Fatalf(".gitignore = %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.root, ".git", "subsync", "keep.json")); !os.IsNotExist(err) {
		t.Fatal("state file not removed")
	}

	r.mustCLI(0, "remove", "--delete-files", "drop/")
	if _, err := os.Stat(filepath.Join(r.root, "drop")); !os.IsNotExist(err) {
		t.Fatal("--delete-files kept the folder")
	}
	if _, err := os.Stat(filepath.Join(r.root, ".gitsubsync")); !os.IsNotExist(err) {
		t.Fatal(".gitsubsync not deleted after the last entry")
	}
	if r.d.Writes() != writes || len(r.driveFiles()) != 1 {
		t.Fatal("remove changed Drive")
	}
	if out := r.mustCLI(0, "list"); !strings.HasPrefix(out, "No folders linked yet.") {
		t.Fatalf("list after remove: %q", out)
	}
}

func TestAddRejects(t *testing.T) {
	r := newTestRepo(t)
	file := r.d.AddFile(r.folder, "a.txt", "a", "")
	trashed := r.d.AddFolder(r.folder, "old")
	r.d.Trash(trashed)
	r.write("tracked/x.txt", "x")
	git(t, r.root, "add", "tracked")
	r.write("full/x.txt", "x")
	r.mustCLI(0, "add", "--no-sync", r.folder, "linked/here")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"add", "https://example.com/x", "p"}, "is not a Google Drive folder link or ID"},
		{[]string{"add", "--mode", "sideways", r.folder, "p"}, `unknown mode "sideways"`},
		{[]string{"add", r.folder, ".git/x"}, "is inside .git"},
		{[]string{"add", r.folder, "../x"}, "is outside the repository"},
		{[]string{"add", r.folder, "."}, "not its root"},
		{[]string{"add", r.folder, "linked/here"}, "linked/here is already linked to Drive folder " + r.folder},
		{[]string{"add", r.folder, "linked"}, "linked overlaps linked/here"},
		{[]string{"add", r.folder, "tracked"}, "tracked is tracked by Git"},
		{[]string{"add", r.folder, "full"}, "full is not empty; use --force"},
		{[]string{"add", "1NoSuchFolder000000000", "p"}, "Drive folder 1NoSuchFolder000000000 doesn't exist, or me@example.com has no access to it"},
		{[]string{"add", file, "p"}, `is a file, not a folder`},
		{[]string{"add", trashed, "p"}, `Drive folder "old" is in the trash`},
	} {
		stdout, stderr, code := cli(t, tc.args...)
		if code != 1 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit %d, stderr %q, want %q (stdout %q)", tc.args, code, stderr, tc.want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "p")); !os.IsNotExist(err) {
		t.Fatal("a rejected add created the folder")
	}
}

func TestAddWarnsWithoutWriteAccess(t *testing.T) {
	r := newTestRepo(t)
	r.d.SetCanAddChildren(r.folder, false)
	_, stderr, code := cli(t, "add", "--no-sync", r.folder, "p")
	if code != 0 || !strings.Contains(stderr, `warning: me@example.com can't add files to "Design"`) {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if _, stderr, _ := cli(t, "add", "--no-sync", "--mode", "download", r.folder, "q"); strings.Contains(stderr, "warning") {
		t.Fatalf("download mode warned: %q", stderr)
	}
}

func TestSyncErrors(t *testing.T) {
	r := newTestRepo(t)
	if out := r.mustCLI(0, "sync"); !strings.HasPrefix(out, "No folders linked yet.") {
		t.Fatalf("sync without entries: %q", out)
	}
	r.mustCLI(0, "add", "--no-sync", r.folder, "a")
	if _, stderr, code := cli(t, "sync", "nope"); code != 1 || !strings.Contains(stderr, "nope is not a git-subsync folder") {
		t.Fatalf("unknown path: exit %d, %q", code, stderr)
	}

	// A failed transfer exits 1 and the rest still syncs.
	r.d.AddFile(r.folder, "ok.txt", "ok", "")
	bad := r.d.AddFile(r.folder, "bad.txt", "bad", "")
	r.d.Hook = func(op, id string) error {
		if op == "download" && id == bad {
			return errors.New("connection reset")
		}
		return nil
	}
	stdout, _, code := cli(t, "sync")
	if code != 1 || !strings.Contains(stdout, "✗ failed    bad.txt: connection reset") || !strings.Contains(stdout, "1 downloaded, 1 failed") {
		t.Fatalf("exit %d\n%s", code, stdout)
	}

	// Losing access to the folder is reported with the account.
	r.d.Hook = func(op, id string) error {
		if op == "get" {
			return gdrive.ErrNoAccess
		}
		return nil
	}
	if _, stderr, code := cli(t, "sync"); code != 1 || !strings.Contains(stderr, "git-subsync: a: Drive folder "+r.folder+" doesn't exist, or me@example.com has no access to it") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

// A folder that already has files is synced for the first time in an
// uploading mode only after a person confirms it: someone else's
// .gitsubsync could point it at their Drive folder.
func TestFirstSyncOfFolderWithFilesNeedsConfirmation(t *testing.T) {
	r := newTestRepo(t)
	r.write("secrets/key.txt", "secret")
	r.write(".gitsubsync", "[subsync \"secrets\"]\n\tpath = secrets\n\tfolder = "+r.folder+"\n\tmode = upload\n")

	_, stderr, code := cli(t, "sync")
	if code != 1 || !strings.Contains(stderr, `secrets already has files but has never been synced with Drive folder "Design"`) ||
		!strings.Contains(stderr, "in a terminal to confirm") {
		t.Fatalf("non-interactive: exit %d, stderr %q", code, stderr)
	}
	if out := r.mustCLI(0, "sync", "--dry-run"); !strings.Contains(out, "↑ upload    key.txt") {
		t.Fatalf("dry run needs no confirmation:\n%s", out)
	}

	interactive = func() bool { return true }
	_, stderr, code = cliWithInput(t, "n\n", "sync")
	if code != 1 || !strings.Contains(stderr, "Sync it? [y/N]") || !strings.Contains(stderr, "secrets not synced") {
		t.Fatalf("declined: exit %d, stderr %q", code, stderr)
	}
	if r.d.Writes() != 0 {
		t.Fatal("uploaded without confirmation")
	}

	if _, stderr, code := cliWithInput(t, "y\n", "sync"); code != 0 || r.driveFiles()["key.txt"] != "secret" {
		t.Fatalf("confirmed: exit %d, stderr %q, Drive %v", code, stderr, r.driveFiles())
	}
	interactive = func() bool { return false }
	r.mustCLI(0, "sync") // synced before: no question

	// Pointing the entry at another Drive folder asks again.
	other := r.d.AddFolder("", "Elsewhere")
	r.write(".gitsubsync", "[subsync \"secrets\"]\n\tpath = secrets\n\tfolder = "+other+"\n\tmode = upload\n")
	if _, stderr, code := cli(t, "sync"); code != 1 || !strings.Contains(stderr, `never been synced with Drive folder "Elsewhere"`) {
		t.Fatalf("changed folder: exit %d, stderr %q", code, stderr)
	}

	// Download mode never uploads, so it doesn't ask.
	r.write(".gitsubsync", "[subsync \"secrets\"]\n\tpath = secrets\n\tfolder = "+other+"\n\tmode = download\n")
	r.mustCLI(0, "sync")
}

// A committed symlink must not let a sync write outside the repository.
func TestSyncRefusesSymlinkedPath(t *testing.T) {
	r := newTestRepo(t)
	r.d.AddFile(r.folder, "a.txt", "a", "")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(r.root, "shared")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := cli(t, "add", r.folder, "shared/design"); code != 1 || !strings.Contains(stderr, "shared is a symbolic link") {
		t.Fatalf("add: exit %d, stderr %q", code, stderr)
	}

	r.write(".gitsubsync", "[subsync \"shared/design\"]\n\tpath = shared/design\n\tfolder = "+r.folder+"\n")
	if _, stderr, code := cli(t, "sync"); code != 1 || !strings.Contains(stderr, "shared is a symbolic link") {
		t.Fatalf("sync: exit %d, stderr %q", code, stderr)
	}
	if _, stderr, code := cli(t, "remove", "--delete-files", "shared/design"); code != 1 || !strings.Contains(stderr, "shared is a symbolic link") {
		t.Fatalf("remove: exit %d, stderr %q", code, stderr)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the repo: %v", entries)
	}
}

func TestCommandsOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	for _, args := range [][]string{{"add", "1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD", "x"}, {"sync"}, {"list"}, {"remove", "x"}} {
		_, stderr, code := cli(t, args...)
		if code != 1 || !strings.Contains(stderr, "Not inside a Git repository.") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
}

// Without a saved login, Drive commands sign in first when someone is at
// the terminal, and otherwise say how to log in.
func TestDriveCommandsNeedLogin(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	t.Chdir(dir)
	t.Setenv(auth.EnvConfigDir, t.TempDir())
	t.Setenv(auth.EnvClientID, "")
	t.Setenv(auth.EnvClientSecret, "")
	orig := interactive
	t.Cleanup(func() { interactive = orig })

	interactive = func() bool { return false }
	_, stderr, code := cli(t, "add", "1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD", "x")
	if code != 1 || !strings.Contains(stderr, "git-subsync: Not logged in. Run `git subsync login`.") {
		t.Fatalf("non-interactive: exit %d, stderr %q", code, stderr)
	}

	// Interactive: the sign-in starts, and here fails for lack of an OAuth
	// client before any browser opens.
	interactive = func() bool { return true }
	_, stderr, code = cli(t, "add", "1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD", "x")
	if code != 1 || !strings.Contains(stderr, "Not logged in. Signing in to Google Drive…") || !strings.Contains(stderr, "no OAuth client configured") {
		t.Fatalf("interactive: exit %d, stderr %q", code, stderr)
	}
}

func TestExitCode(t *testing.T) {
	for err, want := range map[error]int{
		nil:                    0,
		errors.New("x"):        1,
		&exitError{code: 2}:    2,
		withHint(&exitError{}): 0,
	} {
		if got := ExitCode(err); got != want {
			t.Errorf("ExitCode(%v) = %d, want %d", err, got, want)
		}
	}
	if worseExit(2, 1) != 1 || worseExit(0, 2) != 2 || worseExit(2, 0) != 2 || worseExit(0, 0) != 0 {
		t.Error("worseExit")
	}
}
