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
	"github.com/sibtihaj/bolt/app/update"
)

// ── ASCII block art for each letter of "bolt" ────────────────────────────────

const (
	artB = "██████╗ \n██╔══██╗\n███████╗\n██╔══██╗\n██████╔╝\n╚═════╝ "
	artO = " ██████╗ \n██╔═══██╗\n██║   ██║\n██║   ██║\n╚██████╔╝\n ╚═════╝ "
	artL = "██╗     \n██║     \n██║     \n██║     \n███████╗\n╚══════╝"
	artT = "████████╗\n╚══██╔══╝\n   ██║   \n   ██║   \n   ██║   \n   ╚═╝   "
)

// ── Palette — ember: deep orange core → warm amber glow ──────────────────────

var (
	// Primary UI: deep orange — borders, titles, badges
	tfeColor = lipgloss.Color("#F97316")
	// Accent: amber — selectors, prompts, tips
	cyanBright = lipgloss.Color("#FBBF24")
	// Status colours — universal, kept intentionally simple
	greenColor = lipgloss.Color("#22C55E")
	redColor   = lipgloss.Color("#EF4444")
	amberColor = lipgloss.Color("#F97316")
	// Neutral tones
	mutedColor  = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	borderFaint = lipgloss.AdaptiveColor{Light: "#CBD5E1", Dark: "#2D1A0A"}
)

// ── Shared styles (also used in interactive_deploy.go) ───────────────────────

var (
	taglineStyle = lipgloss.NewStyle().Foreground(cyanBright)

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1C0700")).
			Background(tfeColor).
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

	thStyle = lipgloss.NewStyle().Bold(true).Foreground(tfeColor)
	tcStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F0FDFF"})

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(tfeColor).
			Padding(1, 3).
			Width(52)

	updateNoticeStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(amberColor).
				Padding(0, 2)
)

// ── Custom bolt theme for huh forms ──────────────────────────────────────────

func boltTheme() *huh.Theme {
	t := huh.ThemeCharm()
	ember := lipgloss.AdaptiveColor{Light: "#EA6C00", Dark: "#F97316"}
	emberPrimary := lipgloss.Color("#F97316")
	darkBg := lipgloss.AdaptiveColor{Light: "#FFF7ED", Dark: "#1C0700"}

	t.Focused.Title = t.Focused.Title.Foreground(ember).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(ember).Bold(true)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(cyanBright)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(cyanBright)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(cyanBright)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(cyanBright)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(lipgloss.Color("#22C55E"))
	t.Focused.SelectedPrefix = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E")).SetString("✓ ")
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(darkBg).Background(emberPrimary)
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(lipgloss.Color("#22C55E"))
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(cyanBright)
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
// Ember gradient: deep red → orange → amber glow.
func buildLogoLines() []string {
	grad := [4]lipgloss.Color{
		"#DC2626", // B — deep red
		"#F97316", // O — orange
		"#FB923C", // L — light orange
		"#FBBF24", // T — amber
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
	// ── Left panel: bolt icon + logo + tagline + version ──────────────────────
	logoLines := buildLogoLines()
	leftLines := append(logoLines,
		"",
		taglineStyle.Render("Deploy Terraform Enterprise in a bolt")+"  "+badgeStyle.Render(" v"+version+" "),
	)
	leftPanel := lipgloss.NewStyle().
		Padding(1, 3).
		Render(strings.Join(leftLines, "\n"))

	// ── Right panel: quick-start commands + deployment count ──────────────────
	accent := lipgloss.NewStyle().Foreground(cyanBright)
	tip := func(icon, s string) string {
		return "  " + lipgloss.NewStyle().Foreground(tfeColor).Render(icon) + "  " + accent.Render(s)
	}

	deployments, _ := state.List()
	var deployLine string
	switch len(deployments) {
	case 0:
		deployLine = hintStyle.Render("  No active deployments")
	case 1:
		deployLine = lipgloss.NewStyle().Foreground(greenColor).Render("  ● 1 active deployment")
	default:
		deployLine = lipgloss.NewStyle().Foreground(greenColor).Render(
			fmt.Sprintf("  ● %d active deployments", len(deployments)))
	}

	rightLines := []string{
		thStyle.Render("Quick start"),
		"",
		tip("›", "bolt deploy k8s  --name prod"),
		tip("›", "bolt deploy docker  --name dev"),
		tip("›", "bolt list"),
		tip("›", "bolt status  --name prod"),
		tip("›", "bolt destroy  --name prod"),
		"",
		dividerStyle.Render("  " + strings.Repeat("─", 30)),
		deployLine,
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

func showUpdateNotice(rel *update.Release) {
	line1 := lipgloss.NewStyle().Bold(true).Foreground(amberColor).Render(
		"⚡  A new version of bolt is available: v" + rel.Version,
	)
	line2 := labelStyle.Render("Upgrade:  ") +
		tcStyle.Render("brew upgrade sibtihaj/tap/bolt")
	line3 := labelStyle.Render("Release notes:  ") +
		lipgloss.NewStyle().Foreground(cyanBright).Render(rel.URL)
	fmt.Println(updateNoticeStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left, line1, "", line2, line3),
	))
	fmt.Println()
}

func runInteractive() error {
	// Start update check in background so it never delays startup.
	updateCh := make(chan *update.Release, 1)
	go func() { updateCh <- update.Check(version) }()

	printBanner()

	for {
		var action string
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("What would you like to do?").
					Description("↑ ↓ navigate   enter select   ctrl+c exit").
					Options(
						huh.NewOption("  ⚡  Deploy Terraform Enterprise", "deploy"),
						huh.NewOption("  ✗  Destroy a deployment", "destroy"),
						huh.NewOption("  ≡  List deployments", "list"),
						huh.NewOption("  ◎  Check deployment status", "status"),
						huh.NewOption("  ←  Exit", "exit"),
					).
					Value(&action),
			),
		).WithTheme(boltTheme()).Run()

		if errors.Is(err, huh.ErrUserAborted) || action == "exit" {
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().
				Foreground(tfeColor).Bold(true).
				Render("  ⚡  See you next time!"))
			fmt.Println(hintStyle.Render("  bolt is ready when you are."))
			fmt.Println()
			// Show update notice if a newer version was found during this session.
			select {
			case rel := <-updateCh:
				if rel != nil {
					showUpdateNotice(rel)
				}
			default:
			}
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
			fmt.Println()
			fmt.Println(errorBoxStyle.Render("  ✗  " + actionErr.Error()))
		}

		waitForEnter()
		fmt.Println(dividerStyle.Render("  " + strings.Repeat("─", 60)))
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
