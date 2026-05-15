package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *MainModel) renderSettings() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Settings  [↑↓] change  [Enter] apply"))
	b.WriteString("\n\n")

	// Embedder (read/write)
	b.WriteString(styleTitle.Render("  Embedder"))
	if m.stats != nil {
		b.WriteString(fmt.Sprintf("  Status: %s (%dd)\n", m.stats.Embedder.Active, m.stats.Embedder.Dimension))
	}
	b.WriteString(fmt.Sprintf("    Mode: [ %s ]\n", m.embedderMode))
	b.WriteString(styleMuted.Render("          (auto / ollama / openai / local) [↑↓] change  [Enter] apply\n"))
	b.WriteString("\n")

	// Gardener (read-only)
	b.WriteString(styleTitle.Render("  Gardener"))
	b.WriteString(styleMuted.Render("  (restart to change)"))
	b.WriteString("\n")
	if m.gardener != nil {
		b.WriteString(fmt.Sprintf("    Enabled: %v  Mode: %s  Interval: %s\n",
			m.gardener.Enabled, m.gardener.Mode, m.gardener.Interval))
		thinkTime := "never"
		if m.gardener.LastThinkTime != "" {
			thinkTime = m.gardener.LastThinkTime
		}
		b.WriteString(fmt.Sprintf("    Last think: %s  Reflections: %d\n", thinkTime, m.gardener.TotalReflections))
		if len(m.gardener.TargetIndexes) > 0 {
			b.WriteString(fmt.Sprintf("    Targets: %s\n", strings.Join(m.gardener.TargetIndexes, ", ")))
		}
	} else {
		b.WriteString(styleMuted.Render("    Gardener not running.\n"))
	}
	b.WriteString("\n")

	// Server
	b.WriteString(styleTitle.Render("  Server"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("    HTTP: http://%s\n", m.httpAddr))
	b.WriteString("\n[↑↓] change mode  [Enter] apply  [1-5] tabs")

	return m.renderBordered(b.String())
}

func (m *MainModel) updateSettings(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		return m.doEmbedderReload

	case "down", "right":
		m.nextEmbedderMode()
	case "up", "left":
		m.prevEmbedderMode()
	}
	return nil
}

func (m *MainModel) nextEmbedderMode() {
	modes := []string{"auto", "ollama", "openai", "local"}
	for i, mode := range modes {
		if mode == m.embedderMode {
			m.embedderMode = modes[(i+1)%len(modes)]
			return
		}
	}
	m.embedderMode = "auto"
}

func (m *MainModel) prevEmbedderMode() {
	modes := []string{"auto", "ollama", "openai", "local"}
	for i, mode := range modes {
		if mode == m.embedderMode {
			idx := (i - 1 + len(modes)) % len(modes)
			m.embedderMode = modes[idx]
			return
		}
	}
	m.embedderMode = "auto"
}

func (m *MainModel) doEmbedderReload() tea.Msg {
	resp, err := m.client.ReloadEmbedder(m.embedderMode)
	return embedderReloadMsg{response: resp, err: err}
}
