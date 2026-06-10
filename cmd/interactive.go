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

// ── ASCII block art for each letter of "bolt" ─────────────────────────────────

const (
	artB = "██████╗ \n██╔══██╗\n███████╗\n██╔══██╗\n██████╔╝\n╚═════╝ "
	artO = " ██████╗ \n██╔═══██╗\n██║   ██║\n██║   ██║\n╚██████╔╝\n ╚═════╝ "
	artL = "██╗     \n██║     \n██║     \n██║     \n███████╗\n╚══════╝"
	artT = "████████╗\n╚══██╔══╝\n   ██║   \n   ██║   \n   ██║   \n   ╚═╝   "
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

// ── Shared styles (also used in interactive_deploy.go) ───────────────────────

var (
	taglineStyle = lipgloss.NewStyle().Foreground(lilacColor)

	badgeStyle = lipgloss.NewStyle().
			Foreground(tfeColor).
			Background(lipgloss.AdaptiveColor{Light: "#EDE9FE", Dark: "#2D1B69"}).
			Padding(0, 1).
			Bold(true)

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(tfeColor).
			MarginTop(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#9CA3AF"})

	dividerStyle = lipgloss.NewStyle().Foreground(borderFaint)

	hintStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	errorBoxStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(redColor).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(redColor).
			Padding(0, 2)

	thStyle = lipgloss.NewStyle().Bold(true).Foreground(lilacColor)
	tcStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#F9FAFB"})

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

// buildLogoLines renders the 6-line gradient ASCII-art "bolt" logo.
// Each letter is colored with a different shade: indigo → violet → purple → fuchsia.
func buildLogoLines() []string {
	grad := [4]lipgloss.Color{
		"#4338CA", // B — deep indigo
		"#7C3AED", // O — violet
		"#A855F7", // L — purple
		"#D946EF", // T — fuchsia
	}
	letters := [4][]string{
		strings.Split(artB, "\n"),
		strings.Split(artO, "\n"),
		strings.Split(artL, "\n"),
		strings.Split(artT, "\n"),
	}
	rows := make([]string, 6)
	for r := 0; r < 6; r++ {
		var sb strings.Builder
		for c := 0; c < 4; c++ {
			sb.WriteString(lipgloss.NewStyle().Foreground(grad[c]).Render(letters[c][r]))
			if c < 3 {
				sb.WriteByte(' ')
			}
		}
		rows[r] = sb.String()
	}
	return rows
}

func printBanner() {
	// ── Left panel: big logo + tagline + version ──────────────────────────────
	logoLines := buildLogoLines()
	leftLines := append(logoLines,
		"",
		taglineStyle.Render("Deploy Terraform Enterprise in a bolt")+"  "+badgeStyle.Render(" v"+version+" "),
	)
	leftPanel := lipgloss.NewStyle().
		Padding(1, 3).
		Render(strings.Join(leftLines, "\n"))

	// ── Right panel: getting-started tips ─────────────────────────────────────
	tip := func(s string) string {
		return "  " + lipgloss.NewStyle().Foreground(lilacColor).Render(s)
	}
	rightLines := []string{
		thStyle.Render("Getting started"),
		"",
		hintStyle.Render("Use the menu below, or pass"),
		hintStyle.Render("flags directly for scripting:"),
		"",
		tip("bolt deploy k8s  --name prod"),
		tip("bolt deploy docker  --name dev"),
		tip("bolt list"),
		tip("bolt status  --name prod"),
		tip("bolt destroy  --name prod"),
	}
	rightPanel := lipgloss.NewStyle().
		Padding(1, 2).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderLeftForeground(borderFaint).
		Render(strings.Join(rightLines, "\n"))

	// ── Outer box ─────────────────────────────────────────────────────────────
	cols := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(tfeColor).
		Render(cols)

	fmt.Println()
	fmt.Println(box)
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
			fmt.Println("\n" + hintStyle.Render("  Goodbye!") + "  " +
				lipgloss.NewStyle().Foreground(lipgloss.Color("#D946EF")).Render("⚡") + "\n")
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

	cols := []int{18, 8, 14, 18, 0}
	headers := []string{"NAME", "BACKEND", "MODE", "STATUS", "HOSTNAME"}

	fmt.Println()
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

	for _, d := range deployments {
		row := "  " +
			padRight(tcStyle.Render(d.Name), cols[0]) +
			padRight(tcStyle.Render(string(d.Backend)), cols[1]) +
			padRight(tcStyle.Render(string(d.Mode)), cols[2]) +
			padRight(statusIcon(d.Status), cols[3]) +
			tcStyle.Render(d.Hostname)
		fmt.Println(row)
	}
	fmt.Println()
	return nil
}

// padRight pads a rendered (possibly ANSI-colored) string to a fixed visible width.
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
			fmt.Sprintf("%s  (%s / %s)", d.Name, d.Backend, d.Mode), d.Name,
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

	card := lipgloss.JoinVertical(lipgloss.Left,
		sectionStyle.Render(d.Name),
		"",
		labelStyle.Render("Backend:  ")+tcStyle.Render(string(d.Backend)),
		labelStyle.Render("Mode:     ")+tcStyle.Render(string(d.Mode)),
		labelStyle.Render("Status:   ")+statusIcon(d.Status),
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
			fmt.Sprintf("%s  (%s / %s)", d.Name, d.Backend, d.Mode), d.Name,
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
