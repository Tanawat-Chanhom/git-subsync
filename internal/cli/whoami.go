package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/gdrive"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the Google account Drive access is using",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			svc, err := driveService(ctx)
			if err != nil {
				return err
			}
			user, err := gdrive.WhoAmI(ctx, svc)
			if err != nil {
				return err
			}
			if user.DisplayName == "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), user.Email)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s <%s>\n", user.DisplayName, user.Email)
			}
			return err
		},
	}
}
