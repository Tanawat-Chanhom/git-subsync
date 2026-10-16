package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Tanawat-Chanhom/git-subsync/internal/repo"
)

func newRemoveCmd() *cobra.Command {
	var deleteFiles bool
	cmd := &cobra.Command{
		Use:   "remove <path>",
		Short: "Unlink a folder (Drive is never changed)",
		Long: `Remove the folder at <path> from .gitsubsync and .gitignore and delete its
sync state. Local files are kept unless --delete-files is given. Drive is
never changed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			w, err := openWorkspace(ctx)
			if err != nil {
				return err
			}
			e, err := w.entry(args[0])
			if err != nil {
				return err
			}
			if deleteFiles {
				if err := repo.CheckNoSymlinks(w.root, e.Path); err != nil {
					return err
				}
			}
			store := w.store(e.Path)
			unlock, err := store.Lock()
			if err != nil {
				return err
			}
			defer unlock()

			if err := repo.RemoveEntry(ctx, w.root, e.Name); err != nil {
				return err
			}
			if err := repo.RemoveIgnore(w.root, e.Path); err != nil {
				return err
			}
			if err := store.Remove(); err != nil {
				return err
			}
			if deleteFiles {
				if err := os.RemoveAll(w.dir(e.Path)); err != nil {
					return err
				}
				fmt.Fprintf(out, "🧹 Removed %s and deleted its local files.\n", e.Path)
			} else {
				fmt.Fprintf(out, "🧹 Removed %s. Its files are still there, and Git no longer ignores them.\n", e.Path)
			}
			_, err = fmt.Fprintf(out, "💡 Drive was not changed. Commit %s and .gitignore.\n", repo.ConfigFile)
			return err
		},
	}
	cmd.Flags().BoolVar(&deleteFiles, "delete-files", false, "also delete the folder's local files")
	return cmd
}
