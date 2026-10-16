package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/repo"
	"github.com/Tanawat-Chanhom/git-subsync/internal/syncer"
)

func newSyncCmd() *cobra.Command {
	var opts syncOptions
	cmd := &cobra.Command{
		Use:   "sync [<path>...]",
		Short: "Sync linked folders with Google Drive",
		Long: `Sync the given folders (paths relative to the repo root), or every linked
folder, with Google Drive. A folder that doesn't exist yet, e.g. in a fresh
clone, is created and downloaded.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			if err := checkJobs(opts.jobs); err != nil {
				return err
			}
			w, err := openWorkspace(ctx)
			if err != nil {
				return err
			}
			if len(w.entries) == 0 {
				_, err := fmt.Fprintln(out, noEntries)
				return err
			}
			entries := w.entries
			if len(args) > 0 {
				entries = nil
				for _, arg := range args {
					e, err := w.entry(arg)
					if err != nil {
						return err
					}
					entries = append(entries, e)
				}
			}
			for _, e := range entries {
				if _, err := syncer.ParseMode(e.Mode); err != nil {
					return fmt.Errorf("%s in %s: %w", e.Path, repo.ConfigFile, err)
				}
			}

			d, user, err := openDrive(ctx, cmd)
			if err != nil {
				return err
			}
			opts.now = time.Now()
			code := 0
			for i, e := range entries {
				if len(entries) > 1 {
					if i > 0 {
						fmt.Fprintln(out)
					}
					fmt.Fprintf(out, "📂 %s\n", e.Path)
				}
				c, err := syncFolder(cmd, w, d, user, e, opts)
				if err != nil {
					printErr(cmd.ErrOrStderr(), e.Path, err)
				}
				code = worseExit(code, c)
			}
			if code != 0 {
				return &exitError{code: code}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would happen without changing anything")
	cmd.Flags().BoolVar(&opts.force, "force", false, "upload mode: upload over conflicts, keeping the Drive version as a _copy")
	addJobsFlag(cmd, &opts.jobs)
	return cmd
}
