package cmd

import "github.com/spf13/cobra"

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy a Terraform Enterprise instance",
}

func init() {
	rootCmd.AddCommand(deployCmd)
}
