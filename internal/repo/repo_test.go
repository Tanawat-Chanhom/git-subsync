package repo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates an empty Git repo and returns its root as git reports
// it (symlinks resolved, e.g. /private/var on macOS).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q")
	root, err := Root(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRootFromSubdir(t *testing.T) {
	root := initRepo(t)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Root(context.Background(), sub)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("Root = %q, want %q", got, root)
	}
}

func TestRootOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := Root(context.Background(), dir); !errors.Is(err, ErrNotInRepo) {
		t.Fatalf("err = %v, want ErrNotInRepo", err)
	}
}

func TestGitPath(t *testing.T) {
	root := initRepo(t)
	got, err := GitPath(context.Background(), root, "subsync")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".git", "subsync"); got != want {
		t.Fatalf("GitPath = %q, want %q", got, want)
	}
}

func TestIsTracked(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)
	writeFile(t, filepath.Join(root, "tracked", "a.txt"), "a")
	writeFile(t, filepath.Join(root, "untracked", "b.txt"), "b")
	writeFile(t, filepath.Join(root, "glob[1]", "c.txt"), "c")
	run(t, root, "git", "add", "tracked", "glob[1]")

	for p, want := range map[string]bool{
		"tracked":         true,
		"tracked/a.txt":   true,
		"untracked":       false,
		"missing":         false,
		"glob[1]":         true,
		"glob1":           false, // pathspecs are literal, not globs
		"tracked/missing": false,
	} {
		got, err := IsTracked(ctx, root, p)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("IsTracked(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCleanPath(t *testing.T) {
	root := filepath.FromSlash("/repo")
	for arg, want := range map[string]string{
		"docs":                               "docs",
		"docs/":                              "docs",
		"./docs/drive/":                      "docs/drive",
		"a/../b":                             "b",
		filepath.FromSlash("/repo/docs/sub"): "docs/sub",
		"my folder":                          "my folder",
	} {
		got, err := CleanPath(root, arg)
		if err != nil {
			t.Errorf("CleanPath(%q): %v", arg, err)
			continue
		}
		if got != want {
			t.Errorf("CleanPath(%q) = %q, want %q", arg, got, want)
		}
	}
	for _, arg := range []string{"", " ", ".", "./", "..", "../x", "a/../../x", ".git", ".git/x", "a/.GIT", filepath.FromSlash("/elsewhere/x"), "a\nb"} {
		if got, err := CleanPath(root, arg); err == nil {
			t.Errorf("CleanPath(%q) = %q, want error", arg, got)
		}
	}
}

func TestConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)

	entries, err := LoadConfig(ctx, root)
	if err != nil || entries != nil {
		t.Fatalf("missing file: entries = %v, err = %v", entries, err)
	}

	want := []Entry{
		{Name: "my dir.v2", Path: "my dir.v2", Folder: "-folder2", Mode: "upload"},
		{Name: "test-git-sync", Path: "test-git-sync", Folder: "1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD", Mode: "both"},
	}
	for _, e := range []Entry{want[1], want[0]} {
		if err := AddEntry(ctx, root, e); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "[subsync \"test-git-sync\"]\n\tpath = test-git-sync\n\tfolder = 1YCRTT34nq009SX1Ndxmb0ndgLzNEn0TD\n\tmode = both\n") {
		t.Errorf(".gitsubsync =\n%s", data)
	}

	got, err := LoadConfig(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("LoadConfig = %+v, want %+v", got, want)
	}

	if err := RemoveEntry(ctx, root, "my dir.v2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadConfig(ctx, root); len(got) != 1 || got[0] != want[1] {
		t.Fatalf("after remove: %+v", got)
	}
	if err := RemoveEntry(ctx, root, "test-git-sync"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("empty .gitsubsync not deleted (stat err = %v)", err)
	}
}

func TestLoadConfigDefaultsAndErrors(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)
	file := filepath.Join(root, ConfigFile)

	writeFile(t, file, "[subsync \"a\"]\n\tpath = a\n\tfolder = F\n[core]\n\tx = 1\n")
	got, err := LoadConfig(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Entry{Name: "a", Path: "a", Folder: "F", Mode: DefaultMode}); len(got) != 1 || got[0] != want {
		t.Fatalf("LoadConfig = %+v, want [%+v]", got, want)
	}

	writeFile(t, file, "[subsync \"a\"]\n\tfolder = F\n")
	if _, err := LoadConfig(ctx, root); err == nil || !strings.Contains(err.Error(), "no path") {
		t.Fatalf("err = %v, want 'no path'", err)
	}
	writeFile(t, file, "[subsync \"a\"\n")
	if _, err := LoadConfig(ctx, root); err == nil {
		t.Fatal("want error for a malformed file")
	}
}

func TestOverlapping(t *testing.T) {
	entries := []Entry{{Path: "docs/drive"}, {Path: "assets"}}
	for p, want := range map[string]bool{
		"docs/drive":     true,
		"Docs/Drive":     true,
		"docs":           true,
		"docs/drive/sub": true,
		"docs/driver":    false,
		"doc":            false,
		"assets2":        false,
		"other":          false,
	} {
		if _, got := Overlapping(entries, p); got != want {
			t.Errorf("Overlapping(%q) = %v, want %v", p, got, want)
		}
	}
}
