package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/sibtihaj/bolt/app/state"
	"github.com/sibtihaj/bolt/app/tfe"
)

// ── Palette ───────────────────────────────────────────────────────────────────

var (
	tfeColor    = lipgloss.Color("#5C4EE5")
	lilacColor  = lipgloss.Color("#9B8FFF")
	greenColor  = lipgloss.Color("#22C55E")
	redColor    = lipgloss.Color("#EF4444")
	amberColor  = lipgloss.Color("#F59E0B")
	mutedColor  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	borderFaint = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}
)

// ── Shared styles ─────────────────────────────────────────────────────────────

var (
	// Banner
	bannerBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tfeColor).
			Padding(1, 4).
			Width(58)

	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF"))

	iconStyle = lipgloss.NewStyle().
			Foreground(lilacColor)

	taglineStyle = lipgloss.NewStyle().
			Foreground(lilacColor)

	badgeStyle = lipgloss.NewStyle().
			Foreground(tfeColor).
			Background(lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#2D1B69"}).
			Padding(0, 1).
			Bold(true)

	// Shared section/label styles (also used in interactive_deploy.go)
	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(tfeColor).
			MarginTop(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#9CA3AF"})

	dividerStyle = lipgloss.NewStyle().
			Foreground(borderFaint)

	// Feedback
	hintStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	errorBoxStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(redColor).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(redColor).
			Padding(0, 2)

	// Table
	thStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lilacColor)

	tcStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#F9FAFB"})

	// Status card
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tfeColor).
			Padding(1, 3).
			Width(52)
)

// ── Custom bolt theme for huh forms ──────────────────────────────────────────

func boltTheme() *huh.Theme {
	t := huh.ThemeCharm()
	purple := lipgloss.AdaptiveColor{Light: "#5C4EE5", Dark: "#9B8FFF"}
	purpleBright := lipgloss.Color("#5C4EE5")
	cream := lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}

	t.Focused.Title = t.Focused.Title.Foreground(purple).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(purple).Bold(true)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(lilacColor)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(lilacColor)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(lilacColor)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(lilacColor)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(lipgloss.Color("#22C55E"))
	t.Focused.SelectedPrefix = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E")).SetString("✓ ")
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(cream).Background(purpleBright)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(lipgloss.Color("#22C55E"))
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(lilacColor)
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

// ── Banner ────────────────────────────────────────────────────────────────────

func printBanner() {
	icon := iconStyle.Render("⚡")
	name := logoStyle.Render(" bolt")
	badge := badgeStyle.Render(" v" + version + " ")

	// Top line: icon + name left, badge right — padded to fill box inner width
	innerWidth := 48
	left := icon + name
	rightPad := innerWidth - lipgloss.Width(left) - lipgloss.Width(badge)
	if rightPad < 1 {
		rightPad = 1
	}
	topLine := left + strings.Repeat(" ", rightPad) + badge

	tagline := taglineStyle.Render("Terraform Enterprise Provisioner")

	content := lipgloss.JoinVertical(lipgloss.Left, topLine, tagline)
	fmt.Println(bannerBoxStyle.Render(content))
	fmt.Println()
}

// ── Main loop ─────────────────────────────────────────────────────────────────

func runInteractive() error {
	printBanner()

	for {
		var action string
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What would you like to do?").
					Options(
						huh.NewOption("  ›  Deploy Terraform Enterprise", "deploy"),
						huh.NewOption("  ✗  Destroy a deployment", "destroy"),
						huh.NewOption("  ≡  List deployments", "list"),
						huh.NewOption("  ◎  Check deployment status", "status"),
						huh.NewOption("  ←  Exit", "exit"),
					).
					Value(&action),
			),
		).WithTheme(boltTheme()).Run()

		if errors.Is(err, huh.ErrUserAborted) || action == "exit" {
			fmt.Println("\n" + hintStyle.Render("  Goodbye!") + "  " + iconStyle.Render("⚡") + "\n")
			return nil
		}
		if err != nil {
			return err
		}

		fmt.Println()
		var actionErr error

		switch action {
		case "deploy":
			actionErr = interactiveDeploy()
		case "destroy":
			actionErr = interactiveDestroy()
		case "list":
			actionErr = interactiveList()
		case "status":
			actionErr = interactiveStatus()
		}

		if actionErr != nil {
			fmt.Println(errorBoxStyle.Render("  ✗  " + actionErr.Error()))
		}

		waitForEnter()
		fmt.Println(dividerStyle.Render("  " + strings.Repeat("─", 54)))
		fmt.Println()
	}
}

// waitForEnter pauses and waits for Enter before returning to the main menu.
func waitForEnter() {
	fmt.Print("\n" + hintStyle.Render("  ↵  Press Enter to return to main menu  "))
	bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
}

// ── List ──────────────────────────────────────────────────────────────────────

func interactiveList() error {
	deployments, err := state.List()
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		fmt.Println(hintStyle.Render("\n  No deployments found."))
		return nil
	}

	cols := []int{18, 8, 14, 18, 0} // widths: name, backend, mode, status, hostname (free)
	headers := []string{"NAME", "BACKEND", "MODE", "STATUS", "HOSTNAME"}

	fmt.Println()
	// Header row
	header := "  "
	for i, h := range headers {
		cell := thStyle.Render(h)
		if cols[i] > 0 {
			cell = padRight(cell, cols[i])
		}
		header += cell
	}
	fmt.Println(header)
	fmt.Println("  " + dividerStyle.Render(strings.Repeat("─", 70)))

	// Data rows
	for _, d := range deployments {
		icon := statusIcon(d.Status)
		row := "  " +
			padRight(tcStyle.Render(d.Name), cols[0]) +
			padRight(tcStyle.Render(string(d.Backend)), cols[1]) +
			padRight(tcStyle.Render(string(d.Mode)), cols[2]) +
			padRight(icon, cols[3]) +
			tcStyle.Render(d.Hostname)
		fmt.Println(row)
	}
	fmt.Println()
	return nil
}

// padRight pads a rendered string to a fixed visible width, accounting for ANSI codes.
func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// ── Status ────────────────────────────────────────────────────────────────────

func interactiveStatus() error {
	deployments, err := state.List()
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		fmt.Println(hintStyle.Render("\n  No deployments found."))
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
	).WithTheme(boltTheme()).Run()

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

	icon := statusIcon(d.Status)
	card := lipgloss.JoinVertical(lipgloss.Left,
		sectionStyle.Render(d.Name),
		"",
		labelStyle.Render("Backend:  ")+tcStyle.Render(string(d.Backend)),
		labelStyle.Render("Mode:     ")+tcStyle.Render(string(d.Mode)),
		labelStyle.Render("Status:   ")+icon,
		labelStyle.Render("URL:      ")+tcStyle.Render("https://"+d.Hostname),
		labelStyle.Render("Updated:  ")+tcStyle.Render(d.UpdatedAt.Format("2006-01-02 15:04:05")),
	)

	fmt.Println()
	fmt.Println(cardStyle.Render(card))
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
		fmt.Println(hintStyle.Render("\n  No deployments found."))
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
	).WithTheme(boltTheme()).Run()

	if errors.Is(err, huh.ErrUserAborted) || !confirmed {
		fmt.Println(hintStyle.Render("\n  Cancelled."))
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

// ── Helpers ───────────────────────────────────────────────────────────────────

func statusIcon(s state.DeploymentStatus) string {
	switch s {
	case state.StatusRunning:
		return lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render("● running")
	case state.StatusPending:
		return lipgloss.NewStyle().Foreground(amberColor).Bold(true).Render("◐ pending")
	case state.StatusFailed:
		return lipgloss.NewStyle().Foreground(redColor).Bold(true).Render("✗ failed")
	default:
		return lipgloss.NewStyle().Foreground(mutedColor).Render("○ " + string(s))
	}
}
