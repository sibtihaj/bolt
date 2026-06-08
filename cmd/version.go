package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bolt version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("bolt version dev")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
