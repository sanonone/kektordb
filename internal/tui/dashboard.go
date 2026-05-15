package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *MainModel) renderDashboard() string {
	if m.stats == nil {
		return styleMuted.Render("\n\n  Loading... (waiting for server)\n")
	}
	if m.statsErr != nil {
		return styleDanger.Render(fmt.Sprintf("\n\n  Error: %v\n", m.statsErr))
	}

	s := m.stats
	var b strings.Builder

	// Three stat panels side by side.
	panelW := max(25, m.width/3-4)
	col1 := renderPanel("Vectors",
		fmt.Sprintf("%d total", s.TotalVectors),
		fmt.Sprintf("%d indexes", s.TotalIndexes),
		"",
		fmt.Sprintf("Heap: %.0f MB", s.Memory.HeapAllocMB),
		fmt.Sprintf("AOF:  %.0f MB", s.Memory.AOFSizeMB),
	)
	col2 := renderPanel("Graph",
		fmt.Sprintf("%d entities", s.Graph.NodesWithLinks),
		fmt.Sprintf("%d edges", s.Graph.TotalEdges),
		fmt.Sprintf("%d pinned", s.Graph.PinnedNodes),
	)
	gardenerStatus := "inactive"
	if s.Gardener.Enabled {
		gardenerStatus = "active"
	}
	thinkAgo := "never"
	if s.Gardener.LastThinkAgoMs > 0 {
		thinkAgo = fmt.Sprintf("%.0fs ago", float64(s.Gardener.LastThinkAgoMs)/1000)
	}
	col3 := renderPanel("Gardener",
		fmt.Sprintf("Status: %s", gardenerStatus),
		fmt.Sprintf("Mode: %s", s.Gardener.Mode),
		fmt.Sprintf("Last think: %s", thinkAgo),
		"",
		fmt.Sprintf("Reflections: %d", s.Gardener.TotalReflections),
		fmt.Sprintf("Contradictions: %d", s.Gardener.ContradictionsPending),
		fmt.Sprintf("Decayed: %d", s.Gardener.DecayedTotal),
	)
	_ = panelW

	columns := lipgloss.JoinHorizontal(lipgloss.Top, col1, col2, col3)
	b.WriteString(columns)
	b.WriteString("\n\n")

	// Recent events — show as many as fit vertically.
	b.WriteString(styleHeader.Render("Recent events"))
	b.WriteString("\n")
	eventW := m.contentWidth() - 8
	eventCap := m.eventsCap()
	m.eventsMu.Lock()
	total := len(m.events)
	m.eventsMu.Unlock()
	shown := 0
	start := max(0, total-eventCap)
	for i := start; i < total && shown < eventCap; i++ {
		m.eventsMu.Lock()
		if i >= len(m.events) {
			m.eventsMu.Unlock()
			break
		}
		e := m.events[i]
		m.eventsMu.Unlock()
		icon := eventIcon(e.Type)
		ts := formatTimestamp(e.Timestamp)
		desc := formatEventDesc(e, eventW)
		b.WriteString(fmt.Sprintf(" %s %s  %s\n", icon, ts, desc))
		shown++
	}
	if shown == 0 {
		b.WriteString(styleMuted.Render("  No events yet. Start using KektorDB to see activity.\n"))
	}
	b.WriteString("\n[r] refresh  [1-5] tabs  [q] quit")

	return m.renderBordered(b.String())
}

func (m *MainModel) eventsCap() int {
	return max(5, m.height-16)
}

func renderPanel(title string, lines ...string) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(title))
	b.WriteString("\n")
	for _, line := range lines {
		if line == "" {
			b.WriteString("\n")
		} else {
			b.WriteString(styleMuted.Render(line))
			b.WriteString("\n")
		}
	}
	return stylePanel.Render(b.String())
}

func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "--:--:--"
	}
	t := ts / 1e9
	h := (t / 3600) % 24
	m := (t / 60) % 60
	s := t % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func formatEventDesc(e SSEEvent, width int) string {
	idLen := max(12, width/3-10)
	if idLen > 64 {
		idLen = 64
	}
	switch e.Type {
	case "vector.add":
		return fmt.Sprintf("vector.add    %s  index=%s", truncateID(e.ID, idLen), e.IndexName)
	case "edge.create":
		return fmt.Sprintf("edge.create   %s → %s  (%s)", truncateID(e.ID, idLen/2), truncateID(e.TargetID, idLen/2), e.RelType)
	case "vector.delete", "edge.delete":
		return fmt.Sprintf("%s    %s", e.Type, truncateID(e.ID, idLen))
	case "vector.access":
		return fmt.Sprintf("vector.access %s", truncateID(e.ID, idLen))
	default:
		return fmt.Sprintf("%s    %s", e.Type, truncateID(e.ID, idLen))
	}
}
