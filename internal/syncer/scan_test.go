package syncer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive/drivetest"
	"github.com/Tanawat-Chanhom/git-subsync/internal/state"
)

const md5Hello = "5d41402abc4b2a76b9719d911017c592"

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanLocal(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "hello.txt"), "hello")
	write(t, filepath.Join(dir, "docs", "sub", "a.txt"), "a")
	write(t, filepath.Join(dir, ".DS_Store"), "x")
	write(t, filepath.Join(dir, "docs", "Thumbs.db"), "x")
	write(t, filepath.Join(dir, "docs", tempPrefix+"123"), "partial")
	if err := os.Symlink("hello.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	local, err := ScanLocal(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(local.Files); !reflect.DeepEqual(got, []string{"docs/sub/a.txt", "hello.txt"}) {
		t.Errorf("files = %v", got)
	}
	if f := local.Files["hello.txt"]; f.MD5 != md5Hello || f.Size != 5 || f.MTime.IsZero() {
		t.Errorf("hello.txt = %+v", f)
	}
	if got := sortedKeys(local.Dirs); !reflect.DeepEqual(got, []string{"docs", "docs/sub", "empty"}) {
		t.Errorf("dirs = %v", got)
	}
	if !reflect.DeepEqual(local.Skipped, map[string]string{"link": "symbolic link"}) {
		t.Errorf("skipped = %v", local.Skipped)
	}
	if !reflect.DeepEqual(local.Temps, []string{"docs/" + tempPrefix + "123"}) {
		t.Errorf("temps = %v", local.Temps)
	}
}

func TestScanLocalReusesMD5(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	write(t, p, "hello")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	// Matching size and mtime: the state's MD5 is trusted, even if wrong.
	base := map[string]state.File{"a.txt": {MD5: "from-state", Size: 5, MTime: fi.ModTime()}}
	local, err := ScanLocal(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Files["a.txt"].MD5; got != "from-state" {
		t.Fatalf("MD5 = %q, want the one from state", got)
	}

	// A different mtime means the file is hashed again.
	if err := os.Chtimes(p, time.Now(), fi.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	local, err = ScanLocal(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Files["a.txt"].MD5; got != md5Hello {
		t.Fatalf("MD5 = %q, want %q", got, md5Hello)
	}
}

func TestScanLocalMissingDir(t *testing.T) {
	local, err := ScanLocal(filepath.Join(t.TempDir(), "missing"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.Files)+len(local.Dirs)+len(local.Skipped) != 0 {
		t.Fatalf("local = %+v", local)
	}
}

func TestScanRemote(t *testing.T) {
	d := drivetest.New()
	root := d.AddFolder("", "Shared")
	d.AddFile(root, "hello.txt", "hello", "Somchai")
	docs := d.AddFolder(root, "docs")
	d.AddFile(docs, "a.txt", "a", "")
	d.AddGoogleDoc(docs, "Plan")
	d.AddFile(root, "dup.txt", "1", "")
	d.AddFile(root, "dup.txt", "2", "")
	dupDir := d.AddFolder(root, "same")
	d.AddFile(dupDir, "inside.txt", "x", "") // not listed: its folder is skipped
	d.AddFolder(root, "same")
	d.AddFile(root, "a/b", "slash", "")
	d.AddFile(root, ".DS_Store", "x", "")
	d.Trash(d.AddFile(root, "trashed.txt", "x", ""))
	noSum := d.AddFile(root, "nosum.bin", "x", "")
	d.SetMD5(noSum, "")

	remote, err := ScanRemote(context.Background(), d, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(remote.Files); !reflect.DeepEqual(got, []string{"docs/a.txt", "hello.txt"}) {
		t.Errorf("files = %v", got)
	}
	if f := remote.Files["hello.txt"]; f.MD5 != md5Hello || f.Size != 5 || f.ModifiedBy != "Somchai" || f.ModifiedTime.IsZero() || f.ID == "" {
		t.Errorf("hello.txt = %+v", f)
	}
	if !reflect.DeepEqual(remote.Dirs, map[string]string{"docs": docs}) {
		t.Errorf("dirs = %v", remote.Dirs)
	}
	want := map[string]string{
		"docs/Plan": "Google Docs file",
		"dup.txt":   "2 items with this name in the Drive folder",
		"same":      "2 items with this name in the Drive folder",
		"a∕b":       "name isn't valid for a local file",
		"nosum.bin": "Drive has no checksum for it",
	}
	if !reflect.DeepEqual(remote.Skipped, want) {
		t.Errorf("skipped = %v\nwant      %v", remote.Skipped, want)
	}
}

func TestScanRemoteListError(t *testing.T) {
	d := drivetest.New()
	root := d.AddFolder("", "Shared")
	d.AddFolder(root, "docs")
	boom := errors.New("boom")
	d.Hook = func(op, name string) error {
		if op == "list" && name != root {
			return boom
		}
		return nil
	}
	if _, err := ScanRemote(context.Background(), d, root); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}
