package cmd

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/sibtihaj/bolt/app/update"
)

// version is set at build time via GoReleaser ldflags:
//
//	-X github.com/sibtihaj/bolt/cmd.version={{.Version}}
var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the bolt version and check for updates",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("bolt v%s\n", version)

		rel := update.Check(version)
		if rel == nil {
			fmt.Println(lipgloss.NewStyle().Foreground(greenColor).Render("✓  You are up to date."))
			return
		}

		fmt.Println()
		line1 := lipgloss.NewStyle().Bold(true).Foreground(amberColor).Render(
			"A newer version is available: v" + rel.Version,
		)
		line2 := "Run: " + lipgloss.NewStyle().Foreground(cyanBright).Render("brew upgrade sibtihaj/tap/bolt")
		line3 := "Release notes: " + lipgloss.NewStyle().Foreground(cyanBright).Render(rel.URL)
		fmt.Println(updateNoticeStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left, line1, "", line2, line3),
		))
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
