package repo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	ignoreFile   = ".gitignore"
	ignoreHeader = "# git-subsync"
)

// IgnoreLine returns the .gitignore pattern that ignores the folder at the
// slash-separated path p, anchored at the repo root.
func IgnoreLine(p string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range p {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('/')
	return b.String()
}

// AddIgnore adds IgnoreLine(p) to root/.gitignore, at the end of the block
// under the "# git-subsync" comment. The comment is added if missing. It
// does nothing if the line is already there.
func AddIgnore(root, p string) error {
	lf, err := readLines(filepath.Join(root, ignoreFile))
	if err != nil {
		return err
	}
	lines := lf.lines
	line := IgnoreLine(p)
	header := -1
	for i, l := range lines {
		switch l {
		case line:
			return nil
		case ignoreHeader:
			if header < 0 {
				header = i
			}
		}
	}

	if header < 0 {
		if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, ignoreHeader, line)
	} else {
		end := header + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" && !strings.HasPrefix(lines[end], "#") {
			end++
		}
		lines = append(lines[:end], append([]string{line}, lines[end:]...)...)
	}
	lf.lines = lines
	return lf.write()
}

// RemoveIgnore deletes IgnoreLine(p) from root/.gitignore, leaving every
// other line as it is.
func RemoveIgnore(root, p string) error {
	lf, err := readLines(filepath.Join(root, ignoreFile))
	if err != nil {
		return err
	}
	line := IgnoreLine(p)
	var kept []string
	for _, l := range lf.lines {
		if l != line {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(lf.lines) {
		return nil
	}
	lf.lines = kept
	return lf.write()
}

// lineFile is a text file split into lines, remembering its permissions
// and line ending so a rewrite changes only the lines we touch.
type lineFile struct {
	path  string
	lines []string
	perm  fs.FileMode
	eol   string
}

// readLines reads file. A missing file has no lines.
func readLines(file string) (*lineFile, error) {
	lf := &lineFile{path: file, perm: 0o644, eol: "\n"}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	lf.perm = fi.Mode().Perm()
	s := string(data)
	if strings.Contains(s, "\r\n") {
		lf.eol = "\r\n"
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
	if s != "" {
		lf.lines = strings.Split(s, "\n")
		for i, l := range lf.lines {
			lf.lines[i] = strings.TrimSuffix(l, "\r")
		}
	}
	return lf, nil
}

func (lf *lineFile) write() error {
	var b strings.Builder
	for _, l := range lf.lines {
		b.WriteString(l)
		b.WriteString(lf.eol)
	}
	return os.WriteFile(lf.path, []byte(b.String()), lf.perm)
}
