// Package repo locates the Git repository and edits the files git-subsync
// keeps in it: .gitsubsync and .gitignore. It runs the git binary.
package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotInRepo is returned when the working directory is not inside a Git
// work tree.
var ErrNotInRepo = errors.New("not inside a Git repository")

// git runs git in dir and returns its stdout. Errors carry git's stderr.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], strings.TrimPrefix(msg, "fatal: "))
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}

// Root returns the top-level directory of the work tree containing dir.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", ErrNotInRepo
		}
		return "", err
	}
	return filepath.FromSlash(strings.TrimSpace(out)), nil
}

// GitPath returns the absolute path of name inside the Git directory, as
// `git rev-parse --git-path` resolves it.
func GitPath(ctx context.Context, root, name string) (string, error) {
	out, err := git(ctx, root, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	p := filepath.FromSlash(strings.TrimSpace(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return p, nil
}

// IsTracked reports whether Git tracks rel or any file under it.
func IsTracked(ctx context.Context, root, rel string) (bool, error) {
	out, err := git(ctx, root, "--literal-pathspecs", "ls-files", "-z", "--", rel)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// CleanPath turns a <path> argument into a clean, slash-separated path
// relative to root. A relative argument is relative to root, not to the
// current directory.
func CleanPath(root, arg string) (string, error) {
	if strings.TrimSpace(arg) == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsAny(arg, "\x00\n\r") {
		return "", fmt.Errorf("path %q contains a control character", arg)
	}
	p := arg
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", fmt.Errorf("%s is outside the repository", arg)
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	switch {
	case p == ".":
		return "", errors.New("path must be a folder inside the repository, not its root")
	case p == ".." || strings.HasPrefix(p, "../"):
		return "", fmt.Errorf("%s is outside the repository", arg)
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, ".git") {
			return "", fmt.Errorf("%s is inside .git", arg)
		}
	}
	return p, nil
}
