package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

var destroyOpts struct {
	Name  string
	Force bool
}

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy a TFE deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := state.Load(destroyOpts.Name)
		if err != nil {
			return fmt.Errorf("deployment %q not found: %w", destroyOpts.Name, err)
		}
		p, err := tfe.NewProvisioner(d)
		if err != nil {
			return err
		}
		return p.Destroy(destroyOpts.Force)
	},
}

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().StringVarP(&destroyOpts.Name, "name", "n", "", "deployment name (required)")
	destroyCmd.Flags().BoolVarP(&destroyOpts.Force, "force", "f", false, "force destroy even if errors occur")
	_ = destroyCmd.MarkFlagRequired("name")
}
