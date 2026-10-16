package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestIgnoreLine(t *testing.T) {
	for p, want := range map[string]string{
		"test-git-sync": "/test-git-sync/",
		"docs/drive":    "/docs/drive/",
		`a*b?[c]\d`:     `/a\*b\?\[c]\\d/`,
		"#x":            "/#x/",
	} {
		if got := IgnoreLine(p); got != want {
			t.Errorf("IgnoreLine(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestAddIgnore(t *testing.T) {
	for _, tc := range []struct {
		name, before, path, want string
	}{
		{
			name: "no file",
			path: "a",
			want: "# git-subsync\n/a/\n",
		},
		{
			name:   "no trailing newline",
			before: "/dist",
			path:   "a",
			want:   "/dist\n\n# git-subsync\n/a/\n",
		},
		{
			name:   "appends to the existing block",
			before: "/dist\n\n# git-subsync\n/a/\n\n# other\n*.log\n",
			path:   "b",
			want:   "/dist\n\n# git-subsync\n/a/\n/b/\n\n# other\n*.log\n",
		},
		{
			name:   "block followed directly by a comment",
			before: "# git-subsync\n/a/\n# other\n*.log\n",
			path:   "b",
			want:   "# git-subsync\n/a/\n/b/\n# other\n*.log\n",
		},
		{
			name:   "already present",
			before: "# git-subsync\n/a/\n",
			path:   "a",
			want:   "# git-subsync\n/a/\n",
		},
		{
			name:   "keeps CRLF",
			before: "/dist\r\n",
			path:   "a",
			want:   "/dist\r\n\r\n# git-subsync\r\n/a/\r\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, ".gitignore")
			if tc.before != "" {
				writeFile(t, file, tc.before)
			}
			if err := AddIgnore(root, tc.path); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, file); got != tc.want {
				t.Fatalf(".gitignore =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestRemoveIgnore(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".gitignore")

	if err := RemoveIgnore(root, "a"); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("RemoveIgnore created .gitignore")
	}

	writeFile(t, file, "/dist\n\n# git-subsync\n/a/\n/b/\n/a/sub/\na/\n")
	if err := RemoveIgnore(root, "a"); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, file), "/dist\n\n# git-subsync\n/b/\n/a/sub/\na/\n"; got != want {
		t.Fatalf(".gitignore =\n%q\nwant\n%q", got, want)
	}
}

// Git itself must ignore everything in the folder, even with glob
// characters in its name.
func TestIgnoreLineWithGit(t *testing.T) {
	root := initRepo(t)
	for _, p := range []string{"plain", "with space", "a*b", "br[ack]et", `back\slash`, "nested/dir"} {
		if err := AddIgnore(root, p); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, filepath.FromSlash(p), "sub", "f.txt"), "x")
	}
	writeFile(t, filepath.Join(root, "ab", "f.txt"), "x") // must not match a*b
	writeFile(t, filepath.Join(root, "bracket", "f.txt"), "x")

	out := run(t, root, "git", "status", "--porcelain", "--untracked-files=all")
	want := "?? .gitignore\n?? ab/f.txt\n?? bracket/f.txt\n"
	if out != want {
		t.Fatalf("git status =\n%s\nwant\n%s", out, want)
	}
	if err := exec.Command("git", "-C", root, "check-ignore", "-q", "plain/sub/f.txt").Run(); err != nil {
		t.Fatalf("check-ignore: %v", err)
	}
}
