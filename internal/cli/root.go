// Package cli wires the git-subsync cobra commands. Business logic lives in
// the auth and gdrive packages so future commands can reuse it.
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/version"
)

// NewRootCmd builds the root command and all subcommands.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "git-subsync",
		Short:         "Sync a folder in a Git repo with a Google Drive folder",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("git-subsync {{.Version}}\n")

	root.AddCommand(
		newVersionCmd(),
	)
	return root
}

// Execute runs the root command with the given context.
func Execute(ctx context.Context) error {
	return NewRootCmd().ExecuteContext(ctx)
}
