package cli

import (
	"context"
	"errors"
	"os"

	"google.golang.org/api/drive/v3"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
)

func newStore() (*auth.Store, error) {
	dir, err := auth.ConfigDir(os.Getenv)
	if err != nil {
		return nil, err
	}
	return auth.NewStore(dir), nil
}

// driveService returns a Drive client for the saved login. Refreshed tokens
// are written back to disk.
func driveService(ctx context.Context) (*drive.Service, error) {
	store, err := newStore()
	if err != nil {
		return nil, err
	}
	cc, ccErr := auth.ResolveClientConfig(os.Getenv)
	ts, err := auth.TokenSource(ctx, auth.OAuthConfig(cc), store)
	if err != nil {
		return nil, err // "not logged in" takes precedence over a missing client config
	}
	if ccErr != nil {
		return nil, ccErr
	}
	return gdrive.NewService(ctx, ts)
}

// hintError replaces an error's message with an actionable one while keeping
// the original error for errors.Is/As.
type hintError struct {
	msg string
	err error
}

func (e *hintError) Error() string { return e.msg }
func (e *hintError) Unwrap() error { return e.err }

// withHint maps well-known errors to messages that tell the user what to do.
func withHint(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrNotLoggedIn):
		return &hintError{"Not logged in. Run `git subsync login`.", err}
	case auth.IsSessionExpired(err):
		return &hintError{"Session expired. Run `git subsync login` again.", err}
	}
	return err
}
