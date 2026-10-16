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
		// Users run this as `git subsync`; show that in help output.
		Annotations: map[string]string{cobra.CommandDisplayNameAnnotation: "git subsync"},
	}
	root.SetVersionTemplate("git-subsync {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newVersionCmd(),
	)
	return root
}

// Execute runs the CLI with os.Args. Errors carry user-facing messages.
func Execute(ctx context.Context) error {
	return run(ctx, NewRootCmd())
}

func run(ctx context.Context, root *cobra.Command) error {
	return withHint(root.ExecuteContext(ctx))
}
