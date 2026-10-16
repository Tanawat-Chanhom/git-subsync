package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/auth"
)

const revokeTimeout = 10 * time.Second

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the Google token and delete it locally",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := newStore()
			if err != nil {
				return err
			}

			tok, err := store.Load()
			switch {
			case errors.Is(err, auth.ErrNotLoggedIn):
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Not logged in.")
				return err
			case err != nil:
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v; deleting it without revoking\n", err)
			default:
				// Best effort: the local token is deleted even if Google is unreachable.
				ctx, cancel := context.WithTimeout(cmd.Context(), revokeTimeout)
				defer cancel()
				if err := auth.Revoke(ctx, tok); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not revoke the token with Google: %v\n", err)
				}
			}

			if err := store.Delete(); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return err
		},
	}
}
