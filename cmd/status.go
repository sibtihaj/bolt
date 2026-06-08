package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

var statusOpts struct {
	Name string
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the status of a TFE deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := state.Load(statusOpts.Name)
		if err != nil {
			return fmt.Errorf("deployment %q not found: %w", statusOpts.Name, err)
		}

		fmt.Printf("Name:      %s\n", d.Name)
		fmt.Printf("Backend:   %s\n", d.Backend)
		fmt.Printf("Mode:      %s\n", d.Mode)
		fmt.Printf("Status:    %s\n", d.Status)
		fmt.Printf("Hostname:  %s\n", d.Hostname)
		fmt.Printf("Created:   %s\n", d.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Updated:   %s\n", d.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()

		p, err := tfe.NewProvisioner(d)
		if err != nil {
			return err
		}
		st, err := p.Status()
		if err != nil {
			return err
		}
		if st.URL != "" {
			fmt.Printf("URL: %s\n", st.URL)
		}
		if st.Message != "" {
			fmt.Printf("Message: %s\n", st.Message)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringVarP(&statusOpts.Name, "name", "n", "", "deployment name (required)")
	_ = statusCmd.MarkFlagRequired("name")
}
