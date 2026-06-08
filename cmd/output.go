package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/state"
)

var outputOpts struct {
	Name   string
	Format string
}

var outputCmd = &cobra.Command{
	Use:   "output",
	Short: "Print useful environment variables for a deployment",
	Long: `Prints TFE_ADDRESS and TFE_TOKEN hints so you can connect tools to your deployment.

  eval $(bolt output -n myenv --format export)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := state.Load(outputOpts.Name)
		if err != nil {
			return fmt.Errorf("deployment %q not found: %w", outputOpts.Name, err)
		}

		vars := map[string]string{
			"TFE_ADDRESS":  fmt.Sprintf("https://%s", d.Hostname),
			"TFE_HOSTNAME": d.Hostname,
		}

		switch outputOpts.Format {
		case "export":
			for k, v := range vars {
				fmt.Fprintf(os.Stdout, "export %s=%q\n", k, v)
			}
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(vars)
		default:
			return fmt.Errorf("unknown format %q — use export or json", outputOpts.Format)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(outputCmd)
	outputCmd.Flags().StringVarP(&outputOpts.Name, "name", "n", "", "deployment name (required)")
	outputCmd.Flags().StringVar(&outputOpts.Format, "format", "export", "output format: export or json")
	_ = outputCmd.MarkFlagRequired("name")
}
