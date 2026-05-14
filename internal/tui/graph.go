package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

type graphNodeView struct {
	id    string
	label string
	nType string
	line  int
	depth int
}

type graphLoadMsg struct {
	nodes []graphNodeView
	err   error
}

func (m *MainModel) renderGraph() string {
	if m.graphSearch {
		return m.renderGraphSearch()
	}
	if len(m.graphNodeList) > 0 {
		return m.renderGraphNodeList()
	}
	if m.graphFocus == "" {
		return m.renderGraphInit()
	}
	return m.renderGraphExplorer()
}

func (m *MainModel) renderGraphInit() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Graph Explorer"))
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("  [f] search entity by name"))
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("  [l] list all graph nodes"))
	b.WriteString("\n")
	if m.graphErr != nil {
		b.WriteString("\n")
		b.WriteString(styleDanger.Render(fmt.Sprintf("  Error: %v", m.graphErr)))
	}
	b.WriteString("\n[f] find  [l] list all  [1-5] tabs")
	return styleBorder.Render(b.String())
}

func (m *MainModel) renderGraphSearch() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Graph  [Esc] cancel  [Enter] search"))
	b.WriteString("\n\n")
	b.WriteString("  > " + m.searchInput.View())
	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("  Type entity name and press Enter."))
	return styleBorder.Render(b.String())
}

func (m *MainModel) renderGraphNodeList() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render(fmt.Sprintf("Graph  %d nodes  [↑↓] select  [Enter] expand  [Esc] clear", len(m.graphNodeList))))
	b.WriteString("\n\n")
	for i, n := range m.graphNodeList {
		line := fmt.Sprintf("  %s (%s)", n.label, n.nType)
		if i == m.graphListIdx {
			line = styleFocused.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n[Enter] expand  [Esc] clear list  [1-5] tabs")
	return styleBorder.Render(b.String())
}

func (m *MainModel) renderGraphExplorer() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Graph  [f]ind  [b]ack  [l]ist  [Enter] expand"))
	b.WriteString("\n\n")

	focusLabel := nodeLabel(m.graphFocus, m.graphNodes)
	b.WriteString(styleTitle.Render(centerText(fmt.Sprintf("[ %s ]", focusLabel), m.width)))
	b.WriteString("\n\n")

	if m.graphErr != nil {
		b.WriteString(styleDanger.Render(fmt.Sprintf("  Error: %v", m.graphErr)))
		b.WriteString("\n\n")
	}

	targets := m.graphEdges[m.graphFocus]
		if len(targets) == 0 {
			b.WriteString(styleMuted.Render("  (no connections)"))
		} else {
			for i, target := range targets {
				prefix := "├──"
				if i == len(targets)-1 {
					prefix = "└──"
				}
				edgeKey := m.graphFocus + "->" + target
				relType := m.graphRelTypes[edgeKey]
				if relType == "" {
					relType = "connected"
				}
				label := nodeLabel(target, m.graphNodes)
				line := fmt.Sprintf("  %s %s  —%s→ %s", prefix, label, relType, truncateID(target, 20))
				if i == m.graphSelectedIdx {
					line = styleFocused.Render(line)
				}
				b.WriteString(line + "\n")
			}
		}

	b.WriteString("\n")
	sep := strings.Repeat("─", m.width-4)
	b.WriteString(styleMuted.Render(sep))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("  Node: %s  Edges: %d  Stack: %d\n", m.graphFocus, len(targets), len(m.graphStack)))
	b.WriteString("[f] find  [l] list  [b] back  [1-5] tabs")

	return styleBorder.Render(b.String())
}

func (m *MainModel) updateGraph(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	// If in graph-search mode, handle text input and search commands.
	if m.graphSearch {
		switch key {
		case "esc":
			m.graphSearch = false
			m.searchInput.Blur()
			m.searchInput.SetValue("")
			return nil

		case "enter":
			query := m.searchInput.Value()
			m.searchInput.SetValue("")
			if query != "" {
				return m.searchGraphEntities(query)
			}
			m.graphSearch = false
			m.searchInput.Blur()
			return nil
		}

		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return cmd
	}

	// Node list mode.
	if len(m.graphNodeList) > 0 {
		switch key {
		case "up":
			if m.graphListIdx > 0 {
				m.graphListIdx--
			}
			return nil
		case "down":
			if m.graphListIdx < len(m.graphNodeList)-1 {
				m.graphListIdx++
			}
			return nil
		case "enter":
			m.graphFocus = m.graphNodeList[m.graphListIdx].id
			m.graphNodeList = nil
			m.graphListIdx = 0
			return m.fetchGraphRelations(m.graphFocus)
		case "esc":
			m.graphNodeList = nil
			m.graphListIdx = 0
			return nil
		}
		return nil
	}

	// Normal graph explorer mode.
	switch key {
	case "f":
		m.graphSearch = true
		m.searchInput.SetValue("")
		m.searchInput.Focus()
		m.searchInput.SetWidth(m.width - 8)
		return nil

	case "b", "backspace", "left":
		if len(m.graphStack) > 0 {
			m.graphFocus = m.graphStack[len(m.graphStack)-1]
			m.graphStack = m.graphStack[:len(m.graphStack)-1]
			m.graphSelectedIdx = 0
			return m.fetchGraphRelations(m.graphFocus)
		}

	case "l":
		return m.fetchAllGraphNodes

	case "up":
		if m.graphSelectedIdx > 0 {
			m.graphSelectedIdx--
		}
		return nil

	case "down":
		targets := m.graphEdges[m.graphFocus]
		if m.graphSelectedIdx < len(targets)-1 {
			m.graphSelectedIdx++
		}
		return nil

	case "right", "enter":
		targets := m.graphEdges[m.graphFocus]
		if len(targets) > 0 && m.graphSelectedIdx < len(targets) {
			selectedID := targets[m.graphSelectedIdx]
			m.graphStack = append(m.graphStack, m.graphFocus)
			m.graphFocus = selectedID
			m.graphSelectedIdx = 0
			return m.fetchGraphRelations(selectedID)
		}
	}

	return nil
}

func (m *MainModel) fetchGraphRelations(nodeID string) tea.Cmd {
	return func() tea.Msg {
		rel, err := m.client.GetRelations(m.searchIndex, nodeID)
		if err != nil {
			return graphRelationsMsg{nodeID: nodeID, err: err}
		}
		edges := make([]struct {
			TargetID string
			RelType  string
		}, 0)
		for relType, targets := range rel.Relations {
			for _, target := range targets {
				edges = append(edges, struct {
					TargetID string
					RelType  string
				}{TargetID: target, RelType: relType})
			}
		}
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].RelType != edges[j].RelType {
				return edges[i].RelType < edges[j].RelType
			}
			return edges[i].TargetID < edges[j].TargetID
		})
		return graphRelationsMsg{nodeID: nodeID, relations: edges, err: nil}
	}
}

func (m *MainModel) fetchAllGraphNodes() tea.Msg {
	nodes, err := m.client.SearchNodes(m.searchIndex, "", m.graphListLimit)
	if err != nil {
		return graphLoadMsg{err: err}
	}
	views := make([]graphNodeView, len(nodes))
	for i, n := range nodes {
		views[i] = graphNodeView{
			id:    n.ID,
			label: nodeLabel(n.ID, m.graphNodes),
			nType: fmt.Sprintf("%v", n.Properties["type"]),
		}
		m.graphNodes[n.ID] = n
	}
	if len(views) == 0 {
		return graphLoadMsg{err: fmt.Errorf("no graph nodes found")}
	}
	return graphLoadMsg{nodes: views}
}

func (m *MainModel) searchGraphEntities(query string) tea.Cmd {
	return func() tea.Msg {
		nodes, err := m.client.SearchNodes(m.searchIndex, query, m.graphListLimit)
		if err != nil {
			return graphLoadMsg{err: err}
		}
		if len(nodes) == 0 {
			return graphLoadMsg{err: fmt.Errorf("no entities matching '%s'", query)}
		}
		for _, n := range nodes {
			m.graphNodes[n.ID] = n
		}
		views := make([]graphNodeView, len(nodes))
		for i, n := range nodes {
			views[i] = graphNodeView{
				id:    n.ID,
				label: nodeLabel(n.ID, m.graphNodes),
				nType: fmt.Sprintf("%v", n.Properties["type"]),
			}
		}
		return graphLoadMsg{nodes: views}
	}
}

func nodeLabel(id string, nodes map[string]GraphNode) string {
	if n, ok := nodes[id]; ok {
		// Try common label properties in order of preference.
		props := n.Properties
		if name, ok := props["name"].(string); ok && name != "" {
			return name
		}
		if title, ok := props["title"].(string); ok && title != "" {
			return title
		}
		if content, ok := props["content"].(string); ok && content != "" {
			// Use first line or first 40 chars of content.
			content = strings.TrimSpace(content)
			if idx := strings.IndexByte(content, '\n'); idx > 0 {
				content = content[:idx]
			}
			if len(content) > 40 {
				content = content[:40] + "..."
			}
			if content != "" {
				return content
			}
		}
		// Check for file path pattern.
		if source, ok := props["source"].(string); ok && source != "" {
			return filepathLabel(source)
		}
	}
	// Fallback: show last part of ID.
	return filepathLabel(id)
}

// filepathLabel extracts a human-readable label from a file path.
func filepathLabel(path string) string {
	// Try to extract just the filename.
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 && idx < len(path)-1 {
		return path[idx+1:]
	}
	return truncateID(path, 30)
}

func centerText(text string, width int) string {
	textLen := utf8.RuneCountInString(text)
	if textLen >= width {
		return text
	}
	pad := (width - textLen) / 2
	return strings.Repeat(" ", pad) + text
}
