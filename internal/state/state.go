// Package state stores what git-subsync last synced for each folder. State
// lives in the Git directory, one JSON file per folder, and is never
// committed.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const version = 1

// File is the last synced version of one file.
type File struct {
	ID    string    `json:"id"`    // Drive file ID
	MD5   string    `json:"md5"`   // content checksum on both sides at the last sync
	Size  int64     `json:"size"`  // local size, to reuse MD5 when the file is unchanged
	MTime time.Time `json:"mtime"` // local modification time, likewise
}

// State is the sync state of one folder. Paths are slash-separated and
// relative to the synced folder.
type State struct {
	Version    int               `json:"version"`
	Folder     string            `json:"folder"`               // Drive folder ID the state belongs to
	FolderName string            `json:"folderName,omitempty"` // as of the last sync, for `list`
	LastSync   time.Time         `json:"lastSync,omitzero"`
	Files      map[string]File   `json:"files"`
	Folders    map[string]string `json:"folders"` // subfolder path → Drive folder ID
}

// New returns an empty state for the Drive folder with the given ID.
func New(folder string) *State {
	return &State{Version: version, Folder: folder, Files: map[string]File{}, Folders: map[string]string{}}
}

// Store keeps the state of one synced folder in <dir>/<key>.json, where key
// is the folder's path, escaped.
type Store struct {
	dir string
	key string
}

// NewStore returns the store for the synced folder at entryPath.
func NewStore(dir, entryPath string) *Store {
	return &Store{dir: dir, key: url.PathEscape(entryPath)}
}

// Path returns the state file path.
func (s *Store) Path() string { return filepath.Join(s.dir, s.key+".json") }

func (s *Store) lockPath() string { return filepath.Join(s.dir, s.key+".lock") }

// Load returns the saved state, or nil if there is none.
func (s *Store) Load() (*State, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("reading sync state %s: %w", s.Path(), err)
	}
	if st.Version != version {
		return nil, fmt.Errorf("sync state %s has unsupported version %d", s.Path(), st.Version)
	}
	if st.Files == nil {
		st.Files = map[string]File{}
	}
	if st.Folders == nil {
		st.Folders = map[string]string{}
	}
	return &st, nil
}

// Save writes st to a temporary file and renames it into place, so the
// state file is always either the old or the new version.
func (s *Store) Save(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, s.key+".json.tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path())
}

// Remove deletes the state file, if any.
func (s *Store) Remove() error {
	if err := os.Remove(s.Path()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// LockedError means another process holds the folder's lock.
type LockedError struct {
	PID  int
	Path string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("another git-subsync process (pid %d) is syncing this folder; if it isn't running, delete %s", e.PID, e.Path)
}

// Lock takes the folder's lock file, which holds this process's PID. A lock
// left behind by a process that no longer runs is taken over. The returned
// function releases the lock.
func (s *Store) Lock() (unlock func(), err error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	path := s.lockPath()
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(path)
				return nil, werr
			}
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		pid := readPID(path)
		if pid > 0 && processRunning(pid) {
			return nil, &LockedError{PID: pid, Path: path}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
}

func readPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func processRunning(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
