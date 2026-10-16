package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// EnvConfigDir overrides the directory where git-subsync keeps its state.
const EnvConfigDir = "GIT_SUBSYNC_CONFIG_DIR"

const tokenFileName = "token.json"

// ErrNotLoggedIn is returned when there is no saved token.
var ErrNotLoggedIn = errors.New("not logged in")

// ConfigDir returns $GIT_SUBSYNC_CONFIG_DIR, or <os.UserConfigDir>/git-subsync.
func ConfigDir(getenv func(string) string) (string, error) {
	if dir := getenv(EnvConfigDir); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(base, "git-subsync"), nil
}

// Store persists the OAuth token as JSON in dir/token.json. The directory is
// kept at 0700 and the file at 0600.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Path returns the token file path.
func (s *Store) Path() string {
	return filepath.Join(s.dir, tokenFileName)
}

// Load reads the saved token. It returns ErrNotLoggedIn if none exists.
func (s *Store) Load() (*oauth2.Token, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parsing token file %s: %w", s.Path(), err)
	}
	return &tok, nil
}

// Save atomically writes tok, creating the directory if needed.
func (s *Store) Save(tok *oauth2.Token) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	// MkdirAll leaves an existing directory's mode alone; tighten it.
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return fmt.Errorf("securing config directory: %w", err)
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding token: %w", err)
	}

	f, err := os.CreateTemp(s.dir, ".token-*.json")
	if err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("writing token: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing token: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	return nil
}

// Delete removes the saved token. Deleting a missing token is not an error.
func (s *Store) Delete() error {
	err := os.Remove(s.Path())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("deleting token: %w", err)
	}
	return nil
}
