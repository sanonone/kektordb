package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *MainModel) renderTimeline() string {
	var b strings.Builder
	pauseLabel := "pause"
	if m.pauseEvents {
		pauseLabel = "resume"
	}
	b.WriteString(styleHeader.Render(fmt.Sprintf("Timeline  [Space] %s", pauseLabel)))
	b.WriteString("\n\n")

	m.eventsMu.Lock()
	count := len(m.events)
	m.eventsMu.Unlock()

	if count == 0 {
		b.WriteString(styleMuted.Render("  Waiting for events... Start using KektorDB.\n"))
	} else {
		start := max(0, count-max(50, m.height-8))
		m.eventsMu.Lock()
		for i := start; i < count; i++ {
			e := m.events[i]
			icon := eventIcon(e.Type)
			ts := formatTimestamp(e.Timestamp)
			desc := formatEventDesc(e)
			b.WriteString(fmt.Sprintf(" %s %s  %s\n", icon, ts, desc))
		}
		m.eventsMu.Unlock()
	}

	b.WriteString(fmt.Sprintf("\n  %d events buffered", count))
	b.WriteString("\n[Space] pause  [1-5] tabs")
	return styleBorder.Render(b.String())
}

func (m *MainModel) updateTimeline(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "space" {
		m.pauseEvents = !m.pauseEvents
	}
	return nil
}

func (m *MainModel) appendEvent(e SSEEvent) {
	m.eventsMu.Lock()
	defer m.eventsMu.Unlock()
	m.events = append(m.events, e)
	if len(m.events) > 200 {
		m.events = m.events[len(m.events)-200:]
	}
}
