package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
	"github.com/Tanawat-Chanhom/git-subsync/internal/version"
)

// execute runs the CLI with args in an isolated config dir and returns
// stdout, stderr and the user-facing error.
func execute(t *testing.T, configDir string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv(auth.EnvConfigDir, configDir)
	var stdout, stderr bytes.Buffer
	root := NewRootCmd()
	root.SetArgs(args)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := run(context.Background(), root)
	return stdout.String(), stderr.String(), err
}

func TestWhoamiNotLoggedIn(t *testing.T) {
	for _, withClient := range []bool{true, false} {
		t.Run(fmt.Sprintf("client configured=%v", withClient), func(t *testing.T) {
			id, secret := "", ""
			if withClient {
				id, secret = "id", "secret"
			}
			t.Setenv(auth.EnvClientID, id)
			t.Setenv(auth.EnvClientSecret, secret)

			_, _, err := execute(t, t.TempDir(), "whoami")
			if !errors.Is(err, auth.ErrNotLoggedIn) {
				t.Fatalf("err = %v, want ErrNotLoggedIn", err)
			}
			if want := "Not logged in. Run `git subsync login`."; err.Error() != want {
				t.Fatalf("message = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestLogoutNotLoggedIn(t *testing.T) {
	stdout, _, err := execute(t, t.TempDir(), "logout")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "Not logged in.\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestLogoutDeletesUnreadableToken(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token.json")
	if err := os.WriteFile(tokenPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := execute(t, dir, "logout")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "Logged out.\n" || !strings.Contains(stderr, "warning:") {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("token file still exists (stat err = %v)", err)
	}
}

func TestVersion(t *testing.T) {
	orig := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = orig })

	for _, args := range [][]string{{"version"}, {"--version"}} {
		stdout, _, err := execute(t, t.TempDir(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stdout, "git-subsync 1.2.3 (commit ") {
			t.Fatalf("%v: stdout = %q", args, stdout)
		}
	}
}

func TestWithHintSessionExpired(t *testing.T) {
	cause := fmt.Errorf("getting Drive account info: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"})
	err := withHint(cause)
	if want := "Session expired. Run `git subsync login` again."; err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("hint error does not wrap the cause")
	}
}

func TestWithHintPassesOtherErrors(t *testing.T) {
	cause := errors.New("boom")
	if err := withHint(cause); err != cause {
		t.Fatalf("err = %v, want unchanged", err)
	}
	if withHint(nil) != nil {
		t.Fatal("withHint(nil) != nil")
	}
}
