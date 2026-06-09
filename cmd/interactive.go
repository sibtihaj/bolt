package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

// ── Shared styles ────────────────────────────────────────────────────────────

var (
	tfeColor = lipgloss.Color("#5C4EE5") // HashiCorp purple

	bannerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tfeColor).
			Padding(0, 2).
			MarginBottom(1)

	bannerTextStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(tfeColor)

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(tfeColor).
			MarginTop(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#555555", Dark: "#999999"})

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#CCCCCC", Dark: "#444444"})
)

func printBanner() {
	content := bannerTextStyle.Render(fmt.Sprintf("⚡  bolt  —  TFE Provisioner  %s", version))
	fmt.Println(bannerStyle.Render(content))
}

// ── Entry point ───────────────────────────────────────────────────────────────

func runInteractive() error {
	printBanner()

	var action string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What would you like to do?").
				Options(
					huh.NewOption("Deploy Terraform Enterprise", "deploy"),
					huh.NewOption("Destroy a deployment", "destroy"),
					huh.NewOption("List deployments", "list"),
					huh.NewOption("Check deployment status", "status"),
					huh.NewOption("Exit", "exit"),
				).
				Value(&action),
		),
	).WithTheme(huh.ThemeCharm()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Println("Goodbye!")
		return nil
	}
	if err != nil {
		return err
	}

	switch action {
	case "deploy":
		return interactiveDeploy()
	case "destroy":
		return interactiveDestroy()
	case "list":
		return interactiveList()
	case "status":
		return interactiveStatus()
	case "exit":
		fmt.Println("Goodbye!")
	}
	return nil
}

// ── List ──────────────────────────────────────────────────────────────────────

func interactiveList() error {
	deployments, err := state.List()
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		fmt.Println("\n  No deployments found.")
		return nil
	}

	fmt.Println()
	fmt.Printf("  %-18s  %-8s  %-14s  %-10s  %s\n",
		sectionStyle.Render("NAME"),
		sectionStyle.Render("BACKEND"),
		sectionStyle.Render("MODE"),
		sectionStyle.Render("STATUS"),
		sectionStyle.Render("HOSTNAME"),
	)
	fmt.Println("  " + dividerStyle.Render(strings.Repeat("─", 68)))
	for _, d := range deployments {
		fmt.Printf("  %-18s  %-8s  %-14s  %-10s  %s\n",
			d.Name,
			string(d.Backend),
			string(d.Mode),
			string(d.Status),
			d.Hostname,
		)
	}
	fmt.Println()
	return nil
}

// ── Status ────────────────────────────────────────────────────────────────────

func interactiveStatus() error {
	deployments, err := state.List()
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		fmt.Println("\n  No deployments found.")
		return nil
	}

	options := make([]huh.Option[string], len(deployments))
	for i, d := range deployments {
		options[i] = huh.NewOption(
			fmt.Sprintf("%s  (%s / %s)", d.Name, d.Backend, d.Mode),
			d.Name,
		)
	}

	var name string
	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Check status for which deployment?").
				Options(options...).
				Value(&name),
		),
	).WithTheme(huh.ThemeCharm()).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}
	if err != nil {
		return err
	}

	d, err := state.Load(name)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  %s %s\n", labelStyle.Render("Name:    "), d.Name)
	fmt.Printf("  %s %s\n", labelStyle.Render("Backend: "), string(d.Backend))
	fmt.Printf("  %s %s\n", labelStyle.Render("Mode:    "), string(d.Mode))
	fmt.Printf("  %s %s\n", labelStyle.Render("Status:  "), string(d.Status))
	fmt.Printf("  %s https://%s\n", labelStyle.Render("URL:     "), d.Hostname)
	fmt.Printf("  %s %s\n", labelStyle.Render("Updated: "), d.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Println()

	p, err := tfe.NewProvisioner(d)
	if err != nil {
		return err
	}
	_, err = p.Status()
	return err
}

// ── Destroy ───────────────────────────────────────────────────────────────────

func interactiveDestroy() error {
	deployments, err := state.List()
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		fmt.Println("\n  No deployments found.")
		return nil
	}

	options := make([]huh.Option[string], len(deployments))
	for i, d := range deployments {
		options[i] = huh.NewOption(
			fmt.Sprintf("%s  (%s / %s)", d.Name, d.Backend, d.Mode),
			d.Name,
		)
	}

	var (
		name      string
		confirmed bool
	)

	err = huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which deployment to destroy?").
				Options(options...).
				Value(&name),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Permanently remove this deployment and all its resources?").
				Affirmative("Yes, destroy it").
				Negative("Cancel").
				Value(&confirmed),
		),
	).WithTheme(huh.ThemeCharm()).Run()

	if errors.Is(err, huh.ErrUserAborted) || !confirmed {
		fmt.Println("Cancelled.")
		return nil
	}
	if err != nil {
		return err
	}

	d, err := state.Load(name)
	if err != nil {
		return err
	}
	p, err := tfe.NewProvisioner(d)
	if err != nil {
		return err
	}
	return p.Destroy(false)
}
