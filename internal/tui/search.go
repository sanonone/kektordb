package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"
)

func (m *MainModel) renderSearch() string {
	if m.searchMode == "advanced" {
		return m.renderAdvancedSearch()
	}
	return m.renderQuickSearch()
}

func (m *MainModel) renderQuickSearch() string {
	var b strings.Builder

	b.WriteString(styleHeader.Render("Search Quick"))
	b.WriteString("\n\n")

	// Text input - always visible, focus=0
	inputLine := "  > " + m.searchInput.View()
	if m.searchFocus == 0 {
		inputLine = styleFocused.Render(inputLine)
	}
	b.WriteString(inputLine)
	b.WriteString("\n\n")

	// Index selector - focus=1
	idxCount := ""
	if len(m.indexes) <= 1 {
		idxCount = fmt.Sprintf(" (%d available)", len(m.indexes))
	}
	idxLine := fmt.Sprintf("  Index: %s ▼%s", m.searchIndexOrAll(), idxCount)
	if m.searchFocus == 1 {
		idxLine = styleFocused.Render(idxLine)
	}
	b.WriteString(idxLine)
	b.WriteString("\n\n")

	// Results area (viewport) - focus=99
	vpContent := ""
	if m.searchErr != nil {
		vpContent = styleDanger.Render(fmt.Sprintf("Error: %v", m.searchErr))
	} else if m.searchLoading {
		vpContent = styleMuted.Render("Searching...")
	} else if len(m.searchUIRaws) == 0 {
		vpContent = styleMuted.Render("Type a query and press Enter to search.")
	} else {
		for _, result := range m.searchUIRaws {
			vpContent += fmt.Sprintf("%s %.2f  %s\n", scoreBar(result.Score, 20), result.Score, result.ID)
			if content, ok := result.Node.Metadata["content"].(string); ok && content != "" {
				snippet := content
				if len(snippet) > 70 {
					snippet = snippet[:70] + "..."
				}
				vpContent += styleMuted.Render("    " + snippet)
				vpContent += "\n"
			}
			vpContent += "\n"
		}
	}
	m.searchViewport.SetContent(vpContent)
	m.searchViewport.SetWidth(m.width - 4)
	m.searchViewport.SetHeight(m.height - 14)

	vpStyle := styleBorder
	if m.searchFocus == 99 {
		vpStyle = styleBorder.BorderForeground(lipgloss.Color("#7D56F4"))
	}

	b.WriteString(vpStyle.Render(m.searchViewport.View()))

	b.WriteString("\n[Enter] search  [/] advanced  [↑↓] focus  [Esc] clear")
	return b.String()
}

func (m *MainModel) renderAdvancedSearch() string {
	var b strings.Builder

	b.WriteString(styleHeader.Render("Search Advanced"))
	b.WriteString("\n\n")

	// Text input - focus=0
	inputLine := "  > " + m.searchInput.View()
	if m.searchFocus == 0 {
		inputLine = styleFocused.Render(inputLine)
	}
	b.WriteString(inputLine)
	b.WriteString("\n\n")

	// Alpha - focus=1
	alphaBar := renderAlphaSlider(m.searchAlpha, 30)
	alphaLine := fmt.Sprintf("  Alpha: %.1f  %s", m.searchAlpha, alphaBar)
	if m.searchFocus == 1 {
		alphaLine = styleFocused.Render(alphaLine)
	}
	b.WriteString(alphaLine)
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("          ←→ adjust  0.0=BM25  0.5=hybrid  1.0=Vector"))
	b.WriteString("\n\n")

	// Filter - focus=2
	filterDisplay := m.searchFilter
	if filterDisplay == "" {
		filterDisplay = "(empty)"
	}
	filterLine := fmt.Sprintf("  Filter: %s", filterDisplay)
	if m.searchFocus == 2 {
		filterLine = styleFocused.Render(filterLine)
	}
	b.WriteString(filterLine)
	if m.searchFocus == 2 {
		b.WriteString(" ◀ editing — type to set, Esc to clear")
	}
	b.WriteString("\n\n")

	// Graph entity - focus=3
	graphDisplay := m.searchGraphEntity
	if graphDisplay == "" {
		graphDisplay = "(none)"
	}
	graphLine := fmt.Sprintf("  Graph:  %s", graphDisplay)
	if m.searchFocus == 3 {
		graphLine = styleFocused.Render(graphLine)
	}
	b.WriteString(graphLine)
	b.WriteString("\n\n")

	// Relations - focus=4
	relDisplay := "(none)"
	if len(m.searchRelations) > 0 && m.searchRelations[0] != "" {
		relDisplay = strings.Join(m.searchRelations, ", ")
	}
	relLine := fmt.Sprintf("  Incl:   %s", relDisplay)
	if m.searchFocus == 4 {
		relLine = styleFocused.Render(relLine)
	}
	b.WriteString(relLine)
	b.WriteString("\n\n")

	// Results (viewport) - focus=99
	vpContent := ""
	if m.searchErr != nil {
		vpContent = styleDanger.Render(fmt.Sprintf("Error: %v", m.searchErr))
	} else if m.searchLoading {
		vpContent = styleMuted.Render("Searching...")
	} else if len(m.searchResults) > 0 {
		for _, result := range m.searchResults {
			vpContent += fmt.Sprintf("%s %.2f  %s\n", scoreBar(result.Score, 20), result.Score, result.ID)
		}
	} else {
		vpContent = styleMuted.Render("Set parameters and press Enter to search.")
	}
	m.searchViewport.SetContent(vpContent)
	m.searchViewport.SetWidth(m.width - 4)
	m.searchViewport.SetHeight(m.height - 25)

	vpStyle := styleBorder
	if m.searchFocus == 99 {
		vpStyle = styleBorder.BorderForeground(lipgloss.Color("#7D56F4"))
	}
	b.WriteString(vpStyle.Render(m.searchViewport.View()))

	b.WriteString("\n[Enter] run  [/] close  [r] re-run  [↑↓] focus  [Esc] clear")
	return b.String()
}

func (m *MainModel) updateSearch(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	// Delegate to viewport when it's focused.
	if m.searchFocus == 99 {
		var vpCmd tea.Cmd
		m.searchViewport, vpCmd = m.searchViewport.Update(msg)
		if key == "esc" || key == "0" {
			m.searchFocus = 0
		}
		return vpCmd
	}

	// If filter is focused, handle typing directly.
	if m.searchFocus == 2 {
		switch key {
		case "esc":
			m.searchFilter = ""
			m.searchFocus = 0
			return nil
		case "backspace":
			if len(m.searchFilter) > 0 {
				m.searchFilter = m.searchFilter[:len(m.searchFilter)-1]
			}
			return nil
		case "enter":
			m.searchFocus = 0
			return nil
		default:
			if len(key) == 1 {
				m.searchFilter += key
			}
			return nil
		}
	}

	// Text input always processes keys when focus=0.
	var inputCmd tea.Cmd
	if m.searchFocus == 0 {
		m.searchInput, inputCmd = m.searchInput.Update(msg)
	}

	// Focus navigation: ↑↓
	switch key {
	case "up":
		m.searchFocus = max(0, m.searchFocus-1)
	case "down":
		m.searchFocus = min(99, m.searchFocus+1)
		if m.searchMode == "quick" && m.searchFocus > 1 {
			m.searchFocus = 1
		}
		if m.searchMode == "advanced" && m.searchFocus == 5 {
			m.searchFocus = 99
		}
		return inputCmd
	case "0":
		m.searchFocus = 0
		return inputCmd
	case "v":
		// Only focus viewport when NOT on text input.
		if m.searchFocus != 0 {
			m.searchFocus = 99
		}
		return inputCmd
	}

	// Global search keys (work regardless of focus).
	switch key {
	case "/":
		m.searchMode = map[bool]string{true: "quick", false: "advanced"}[m.searchMode == "quick"]
	case "r":
		if m.searchMode == "advanced" && m.searchInput.Value() != "" {
			return m.doAdvancedSearch
		}
	case "tab":
		m.cycleIndex()
	}

	// Actions on specific focuses.
	switch m.searchFocus {
	case 0:
		switch key {
		case "enter":
			if m.searchInput.Value() != "" {
				if m.searchMode == "quick" {
					return tea.Batch(inputCmd, m.doQuickSearch)
				}
				return tea.Batch(inputCmd, m.doAdvancedSearch)
			}
		case "esc":
			m.searchInput.SetValue("")
			m.searchResults = nil
			m.searchUIRaws = nil
			m.searchErr = nil
		}

	case 1:
		if m.searchMode == "quick" {
			// Index selector
			if key == "enter" || key == " " {
				m.cycleIndex()
			}
		} else {
			// Alpha slider
			switch key {
			case "left":
				m.searchAlpha = max(0, m.searchAlpha-0.1)
			case "right":
				m.searchAlpha = min(1.0, m.searchAlpha+0.1)
			}
		}

	case 2:
		// Filter — handled above

	case 3:
		if key == "enter" || key == " " {
			return m.doSearchEntities
		}

	case 4:
		if key == "enter" || key == " " {
			m.cycleRelations()
		}
	}

	// Global search keys.
	switch key {
	case "r":
		if m.searchMode == "advanced" && m.searchInput.Value() != "" {
			return m.doAdvancedSearch
		}
	case "tab":
		m.cycleIndex()
	}

	return inputCmd
}

func (m *MainModel) doQuickSearch() tea.Msg {
	query := m.searchInput.Value()
	if query == "" {
		return searchResultMsg{err: fmt.Errorf("empty query")}
	}
	req := UISearchRequest{
		IndexName: m.searchIndex,
		Query:     query,
		K:         10,
	}
	results, err := m.client.UISearch(req)
	return searchResultMsg{raw: results, err: err}
}

func (m *MainModel) doAdvancedSearch() tea.Msg {
	req := SearchRequest{
		IndexName: m.searchIndex,
		K:         10,
		Alpha:     m.searchAlpha,
		Filter:    m.searchFilter,
	}
	if len(m.searchRelations) > 0 {
		req.IncludeRelations = m.searchRelations
	}
	results, err := m.client.Search(req)
	return searchResultMsg{results: results, err: err}
}

func (m *MainModel) doSearchEntities() tea.Msg {
	nodes, err := m.client.SearchNodes(m.searchIndex, m.searchFilter)
	if err != nil {
		return searchResultMsg{err: err}
	}
	if len(nodes) > 0 {
		m.searchGraphEntity = nodes[0].ID
	}
	return nil
}

func (m *MainModel) cycleIndex() {
	if len(m.indexes) > 0 {
		for i, idx := range m.indexes {
			if idx.Name == m.searchIndex {
				m.searchIndex = m.indexes[(i+1)%len(m.indexes)].Name
				return
			}
		}
		m.searchIndex = m.indexes[0].Name
	}
}

func (m *MainModel) cycleRelations() {
	opts := []string{"parent.child", "next", "prev", "mentions"}
	if len(m.searchRelations) == 0 || m.searchRelations[0] == "" {
		m.searchRelations = opts
	} else {
		m.searchRelations = nil
	}
}

func (m *MainModel) searchIndexOrAll() string {
	if m.searchIndex == "" {
		return "all indexes"
	}
	return m.searchIndex
}

func renderAlphaSlider(alpha float64, width int) string {
	pos := int(alpha * float64(width))
	if pos < 0 {
		pos = 0
	}
	if pos > width {
		pos = width
	}
	bar := ""
	for i := 0; i < width; i++ {
		if i < pos {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return "[" + bar + "]"
}
