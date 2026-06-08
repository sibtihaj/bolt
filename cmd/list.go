package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/state"
)

var listOpts struct {
	Output string
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all known TFE deployments",
	RunE: func(cmd *cobra.Command, args []string) error {
		deployments, err := state.List()
		if err != nil {
			return err
		}
		if len(deployments) == 0 {
			fmt.Println("No deployments found.")
			return nil
		}

		if listOpts.Output == "json" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(deployments)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tBACKEND\tMODE\tSTATUS\tHOSTNAME\tCREATED")
		for _, d := range deployments {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				d.Name,
				d.Backend,
				d.Mode,
				d.Status,
				d.Hostname,
				d.CreatedAt.Format("2006-01-02 15:04"),
			)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().StringVarP(&listOpts.Output, "output", "o", "table", "output format: table or json")
}
