package auth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func perm(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestStoreSavePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "git-subsync")
	s := NewStore(dir)
	if err := s.Save(&oauth2.Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dir); got != 0o700 {
		t.Errorf("dir mode = %o, want 700", got)
	}
	if got := perm(t, s.Path()); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

func TestStoreSaveTightensExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "git-subsync")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	if err := s.Save(&oauth2.Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dir); got != 0o700 {
		t.Errorf("dir mode = %o, want 700", got)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	want := &oauth2.Token{
		AccessToken:  "access",
		TokenType:    "Bearer",
		RefreshToken: "refresh",
		Expiry:       time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.TokenType != want.TokenType ||
		got.RefreshToken != want.RefreshToken || !got.Expiry.Equal(want.Expiry) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStoreSaveOverwrites(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, access := range []string{"first", "second"} {
		if err := s.Save(&oauth2.Token{AccessToken: access}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "second" {
		t.Fatalf("AccessToken = %q, want second", got.AccessToken)
	}
	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want only token.json (temp files leaked?)", len(entries))
	}
}

func TestStoreLoadMissing(t *testing.T) {
	_, err := NewStore(t.TempDir()).Load()
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
}

func TestStoreLoadCorrupt(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.WriteFile(s.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load()
	if err == nil || errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want a parse error", err)
	}
}

func TestStoreDelete(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(&oauth2.Token{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := s.Delete(); err != nil {
			t.Fatalf("Delete #%d: %v", i+1, err)
		}
	}
	if _, err := s.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Load after Delete: err = %v, want ErrNotLoggedIn", err)
	}
}

func TestConfigDir(t *testing.T) {
	got, err := ConfigDir(envMap(map[string]string{EnvConfigDir: "/custom/dir"}))
	if err != nil || got != "/custom/dir" {
		t.Fatalf("override: got %q, %v", got, err)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this system")
	}
	got, err = ConfigDir(envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "git-subsync"); got != want {
		t.Fatalf("default: got %q, want %q", got, want)
	}
}
