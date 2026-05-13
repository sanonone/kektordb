package tui

import "charm.land/lipgloss/v2"

var (
	colorPrimary   = lipgloss.Color("#7D56F4")
	colorSuccess   = lipgloss.Color("#04B575")
	colorWarning   = lipgloss.Color("#F0C000")
	colorDanger    = lipgloss.Color("#FF3333")
	colorMuted     = lipgloss.Color("#626262")
	colorAccent    = lipgloss.Color("#FF79C6")
	colorHighlight = lipgloss.BrightBlue

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.BrightMagenta).
			Padding(0, 1)

	styleFooter = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.White).
			Background(colorPrimary).
			Padding(0, 1)

	styleTabInactive = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleWarning = lipgloss.NewStyle().
			Foreground(colorWarning)

	styleDanger = lipgloss.NewStyle().
			Foreground(colorDanger)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleBorder = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted)

	stylePanel = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorMuted).
			Padding(0, 1)

	styleScoreBar = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleKeyHint = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleFocused = lipgloss.NewStyle().
			Reverse(true).
			Bold(true).
			Padding(0, 1)
)

func scoreBar(score float64, width int) string {
	filled := int(score * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 1 && score > 0 {
		filled = 1
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "▬"
		} else {
			bar += " "
		}
	}
	return styleScoreBar.Render(bar)
}

func truncateID(id string, maxLen int) string {
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen-3] + "..."
}

func eventIcon(eventType string) string {
	switch eventType {
	case "vector.add", "edge.create":
		return "🟢"
	case "vector.access", "gardener.scan":
		return "🟡"
	case "vector.delete", "edge.delete":
		return "🔴"
	default:
		return "⚪"
	}
}
