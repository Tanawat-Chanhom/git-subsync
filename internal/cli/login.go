package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
)

func newLoginCmd() *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Google Drive in your browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if err := signIn(ctx, cmd, noBrowser); err != nil {
				return err
			}
			svc, err := driveService(ctx)
			if err != nil {
				return err
			}
			user, err := gdrive.WhoAmI(ctx, svc)
			if err != nil {
				return fmt.Errorf("signed in, but could not read the account: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\n", user.Email)
			return err
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the login URL instead of opening a browser")
	return cmd
}

// signIn runs the browser sign-in and saves the token. Progress goes to
// stderr.
func signIn(ctx context.Context, cmd *cobra.Command, noBrowser bool) error {
	cc, err := auth.ResolveClientConfig(os.Getenv)
	if err != nil {
		return err
	}
	store, err := newStore()
	if err != nil {
		return err
	}
	opts := auth.LoginOptions{Out: cmd.ErrOrStderr()}
	if !noBrowser {
		opts.OpenBrowser = auth.OpenBrowser
	}
	tok, err := auth.Login(ctx, auth.OAuthConfig(cc), opts)
	if err != nil {
		return err
	}
	return store.Save(tok)
}
