package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "git-subsync %s\n", version.String())
			return err
		},
	}
}
