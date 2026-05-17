package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *MainModel) syncSearchFocus() {
	if m.activeTab == 2 && m.searchFocus == 0 {
		m.searchInput.Focus()
	} else {
		m.searchInput.Blur()
	}
	if m.activeTab == 2 && m.searchFocus == 2 {
		m.filterInput.Focus()
	} else {
		m.filterInput.Blur()
	}
}

func (m *MainModel) maxSearchFocus() int {
	if m.searchMode == "quick" {
		return 1
	}
	return 4 // 0=query, 1=alpha, 2=filter, 3=relations, 4=limit
}

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
	var filterLine string
	if m.searchFocus == 2 {
		filterLine = styleFocused.Render("  Filter: " + m.filterInput.View())
	} else {
		filterDisplay := m.filterInput.Value()
		if filterDisplay == "" {
			filterDisplay = "(empty)"
		}
		filterLine = fmt.Sprintf("  Filter: %s", filterDisplay)
	}
	b.WriteString(filterLine)
	if m.searchFocus == 2 {
		b.WriteString(styleMuted.Render(" ◀ editing — Esc to exit"))
	}
	b.WriteString("\n\n")

	// Relations - focus=3
	relDisplay := "(none)"
	if len(m.searchRelations) > 0 && m.searchRelations[0] != "" {
		relDisplay = strings.Join(m.searchRelations, ", ")
	}
	relLine := fmt.Sprintf("  Incl:   %s", relDisplay)
	if m.searchFocus == 3 {
		relLine = styleFocused.Render(relLine)
	}
	b.WriteString(relLine)
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("          [Enter] toggle relation paths"))
	b.WriteString("\n\n")

	// Limit K - focus=4
	kLine := fmt.Sprintf("  Limit:  %d", m.searchK)
	if m.searchFocus == 4 {
		kLine = styleFocused.Render(kLine)
	}
	b.WriteString(kLine)
	b.WriteString("\n")
	b.WriteString(styleMuted.Render("          [←→] adjust  min 5  max 100"))
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

	b.WriteString("\n[Enter] run  [/] close  [r] re-run  [←→] adjust  [↑↓] focus  [Esc] clear query")
	return b.String()
}

func (m *MainModel) updateSearch(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()

	// ── Toggle advanced mode con / (solo fuori dai text input) ──
	if key == "/" && m.searchFocus != 0 && m.searchFocus != 2 {
		if m.searchMode == "quick" {
			m.searchMode = "advanced"
		} else {
			m.searchMode = "quick"
		}
		m.searchFocus = 0
		m.syncSearchFocus()
		return nil
	}

	// ── Viewport focus ──
	if m.searchFocus == 99 {
		var vpCmd tea.Cmd
		m.searchViewport, vpCmd = m.searchViewport.Update(msg)
		if key == "esc" || key == "0" {
			m.searchFocus = 0
			m.syncSearchFocus()
		}
		return vpCmd
	}

	// ── Navigazione focus con ↑↓ (prima del textinput) ──
	switch key {
	case "up":
		if m.searchFocus > 0 {
			m.searchFocus--
		}
		m.syncSearchFocus()
		return nil
	case "down":
		maxFocus := m.maxSearchFocus()
		if m.searchFocus < maxFocus {
			m.searchFocus++
		} else if m.searchFocus == maxFocus {
			m.searchFocus = 99
		}
		m.syncSearchFocus()
		return nil
	case "0":
		m.searchFocus = 0
		m.syncSearchFocus()
		return nil
	case "v":
		if m.searchFocus != 0 {
			m.searchFocus = 99
			m.syncSearchFocus()
		}
		return nil
	}

	// ── Passa il messaggio al textinput appropriato ──
	var inputCmd tea.Cmd
	if m.searchFocus == 0 {
		m.searchInput, inputCmd = m.searchInput.Update(msg)
	} else if m.searchFocus == 2 {
		m.filterInput, inputCmd = m.filterInput.Update(msg)
		m.searchFilter = m.filterInput.Value()
	}

	// ── Azioni globali Search ──
	switch key {
	case "tab":
		m.cycleIndex()
		return inputCmd
	case "r":
		if m.searchInput.Value() != "" {
			if m.searchMode == "advanced" {
				return m.doAdvancedSearch
			}
			return m.doQuickSearch
		}
		return inputCmd
	}

	// ── Global Esc: da qualsiasi campo torna a focus 0 ──
	if key == "esc" && m.searchFocus != 0 {
		m.searchFocus = 0
		m.syncSearchFocus()
		return inputCmd
	}

	// ── Azioni per focus specifico ──
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
			if key == "enter" || key == " " {
				m.cycleIndex()
			}
		} else {
			switch key {
			case "left":
				m.searchAlpha = max(0, m.searchAlpha-0.1)
			case "right":
				m.searchAlpha = min(1.0, m.searchAlpha+0.1)
			}
		}
	case 2:
		switch key {
		case "esc":
			m.searchFocus = 0
			m.syncSearchFocus()
			return inputCmd
		case "enter":
			m.searchFilter = m.filterInput.Value()
			return inputCmd
		}
	case 3:
		if key == "enter" || key == " " {
			m.cycleRelations()
		}
	case 4:
		switch key {
		case "left":
			if m.searchK > 5 {
				m.searchK -= 5
			}
		case "right":
			if m.searchK < 100 {
				m.searchK += 5
			}
		}
	}

	return inputCmd
}

func (m *MainModel) doQuickSearch() tea.Msg {
	query := m.searchInput.Value()
	if query == "" {
		return searchResultMsg{err: fmt.Errorf("empty query")}
	}
	req := SearchRequest{
		IndexName: m.searchIndex,
		QueryText: query,
		K:         m.searchK,
		Hydrate:   true,
	}
	results, err := m.client.SearchHydrated(req)
	return searchResultMsg{raw: results, err: err}
}

func (m *MainModel) doAdvancedSearch() tea.Msg {
	req := SearchRequest{
		IndexName: m.searchIndex,
		K:         m.searchK,
		QueryText: m.searchInput.Value(),
		Alpha:     m.searchAlpha,
		Filter:    m.filterInput.Value(),
	}
	if len(m.searchRelations) > 0 {
		req.IncludeRelations = m.searchRelations
	}
	results, err := m.client.Search(req)
	return searchResultMsg{results: results, err: err}
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
