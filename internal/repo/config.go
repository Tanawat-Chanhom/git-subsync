package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfigFile is the committed file that lists synced folders, in Git config
// format (like .gitmodules).
const ConfigFile = ".gitsubsync"

// DefaultMode is the mode of entries that don't set one.
const DefaultMode = "both"

// Entry is one [subsync "<name>"] section of .gitsubsync.
type Entry struct {
	Name   string // section name; equal to Path for entries git-subsync writes
	Path   string // slash-separated, relative to the repo root
	Folder string // Drive folder ID
	Mode   string
}

// LoadConfig reads root/.gitsubsync, sorted by path. A missing file means no
// entries.
func LoadConfig(ctx context.Context, root string) ([]Entry, error) {
	file := filepath.Join(root, ConfigFile)
	if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	out, err := git(ctx, root, "config", "-f", file, "-z", "--list")
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ConfigFile, err)
	}

	byName := map[string]*Entry{}
	var entries []*Entry
	for _, rec := range strings.Split(out, "\x00") {
		key, value, _ := strings.Cut(rec, "\n")
		rest, ok := strings.CutPrefix(key, "subsync.")
		if !ok {
			continue
		}
		dot := strings.LastIndexByte(rest, '.')
		if dot < 0 {
			continue
		}
		name, field := rest[:dot], rest[dot+1:]
		e := byName[name]
		if e == nil {
			e = &Entry{Name: name}
			byName[name] = e
			entries = append(entries, e)
		}
		switch field {
		case "path":
			e.Path = value
		case "folder":
			e.Folder = value
		case "mode":
			e.Mode = value
		}
	}

	// .gitsubsync comes from whoever committed it, so paths are checked
	// as strictly as `add` checks them.
	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Path == "" {
			return nil, fmt.Errorf("%s: [subsync %q] has no path", ConfigFile, e.Name)
		}
		if p, err := CleanPath(root, e.Path); err != nil || p != e.Path {
			return nil, fmt.Errorf("%s: [subsync %q] has an invalid path %q; it must be a clean path inside the repository, relative to its root", ConfigFile, e.Name, e.Path)
		}
		if other, ok := Overlapping(result, e.Path); ok {
			return nil, fmt.Errorf("%s: %s and %s overlap", ConfigFile, other.Path, e.Path)
		}
		if e.Folder == "" {
			return nil, fmt.Errorf("%s: [subsync %q] has no folder", ConfigFile, e.Name)
		}
		if e.Mode == "" {
			e.Mode = DefaultMode
		}
		result = append(result, *e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

// AddEntry writes e to root/.gitsubsync as [subsync "<e.Path>"].
func AddEntry(ctx context.Context, root string, e Entry) error {
	file := filepath.Join(root, ConfigFile)
	for _, kv := range [][2]string{{"path", e.Path}, {"folder", e.Folder}, {"mode", e.Mode}} {
		if _, err := git(ctx, root, "config", "-f", file, "subsync."+e.Path+"."+kv[0], kv[1]); err != nil {
			return fmt.Errorf("writing %s: %w", ConfigFile, err)
		}
	}
	return nil
}

// RemoveEntry deletes the [subsync "<name>"] section, and the file itself
// once it has no sections left.
func RemoveEntry(ctx context.Context, root, name string) error {
	file := filepath.Join(root, ConfigFile)
	if _, err := git(ctx, root, "config", "-f", file, "--remove-section", "subsync."+name); err != nil {
		return fmt.Errorf("writing %s: %w", ConfigFile, err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return os.Remove(file)
	}
	return nil
}

// FindEntry returns the entry whose path is p.
func FindEntry(entries []Entry, p string) (Entry, bool) {
	for _, e := range entries {
		if e.Path == p {
			return e, true
		}
	}
	return Entry{}, false
}

// Overlapping returns an entry whose folder is p, contains p, or is inside
// p. Paths are compared case-insensitively, because macOS file systems are.
func Overlapping(entries []Entry, p string) (Entry, bool) {
	lp := strings.ToLower(p)
	for _, e := range entries {
		le := strings.ToLower(e.Path)
		if le == lp || strings.HasPrefix(lp, le+"/") || strings.HasPrefix(le, lp+"/") {
			return e, true
		}
	}
	return Entry{}, false
}
