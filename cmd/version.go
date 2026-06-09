package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set at build time via GoReleaser ldflags:
//
//	-X github.com/sibtihaj/bolt/cmd.version={{.Version}}
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bolt version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("bolt %s\n", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
