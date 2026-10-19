package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List linked folders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w, err := openWorkspace(cmd.Context())
			if err != nil {
				return err
			}
			if len(w.entries) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), noEntries)
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "FOLDER\tMODE\tDRIVE FOLDER\tLAST SYNC")
			for _, e := range w.entries {
				folder, last := e.Folder, "never"
				st, err := w.store(e.Path).Load()
				switch {
				case err != nil:
					last = "unknown (unreadable state)"
				case st != nil && st.Folder == e.Folder:
					if st.FolderName != "" {
						folder = st.FolderName
					}
					if !st.LastSync.IsZero() {
						last = st.LastSync.Local().Format("2006-01-02 15:04")
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Path, e.Mode, folder, last)
			}
			return tw.Flush()
		},
	}
}
