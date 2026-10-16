package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/api/drive/v3"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
	"github.com/Tanawat-Chanhom/git-subsync/internal/repo"
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

// openDrive returns Drive access for the saved login and the account it
// acts as. Without a saved login, or after the session expired, it first
// runs the browser sign-in, if someone is at the terminal. Tests replace it.
var openDrive = func(ctx context.Context, cmd *cobra.Command) (gdrive.Drive, gdrive.User, error) {
	d, user, err := connect(ctx)
	if err == nil || !needsLogin(err) || !interactive() {
		return d, user, err
	}
	reason := "Not logged in."
	if auth.IsSessionExpired(err) {
		reason = "Session expired."
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "🔑", reason, "Signing in to Google Drive…")
	if err := signIn(ctx, cmd, false); err != nil {
		return nil, gdrive.User{}, err
	}
	d, user, err = connect(ctx)
	if err == nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "✅ Logged in as %s\n", user.Email)
	}
	return d, user, err
}

// connect returns a Drive client and its account, which also checks that
// the saved login works.
func connect(ctx context.Context) (gdrive.Drive, gdrive.User, error) {
	svc, err := driveService(ctx)
	if err != nil {
		return nil, gdrive.User{}, err
	}
	c := gdrive.NewClient(svc)
	user, err := c.WhoAmI(ctx)
	if err != nil {
		return nil, gdrive.User{}, err
	}
	return c, user, nil
}

func needsLogin(err error) bool {
	return errors.Is(err, auth.ErrNotLoggedIn) || auth.IsSessionExpired(err)
}

// interactive reports whether a person can answer a browser sign-in: both
// stdin and stderr are terminals.
var interactive = func() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stderr)
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// progressTerminal returns stderr if a live progress display can be drawn
// on it.
func progressTerminal(cmd *cobra.Command) (*os.File, bool) {
	f, ok := cmd.ErrOrStderr().(*os.File)
	return f, ok && isTerminal(f) && os.Getenv("TERM") != "dumb"
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
	case errors.Is(err, repo.ErrNotInRepo):
		return &hintError{"Not inside a Git repository. Run git subsync from inside the repo you want to sync.", err}
	}
	return err
}

// exitError ends the process with a specific exit code. Its message, if
// any, is printed like any other error.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// ExitCode returns the process exit code for an error from Execute: 0 for
// nil, the code of an exit error, and 1 otherwise.
func ExitCode(err error) int {
	var ee *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.code
	}
	return 1
}
