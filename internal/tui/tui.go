package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	tea "charm.land/bubbletea/v2"
)

func RunTUI(httpAddr string) error {
	m := NewMainModel(httpAddr)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

func (m *MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.searchInput.SetWidth(msg.Width - 8)
		m.searchViewport.SetWidth(msg.Width - 4)
		m.searchViewport.SetHeight(msg.Height - 18)
		m.ready = true

	case tea.KeyPressMsg:
		key := msg.String()

		// ── Global quit ──
		if key == "ctrl+c" || key == "q" {
			return m, tea.Quit
		}

		// ── Tab switching ──
		switch key {
		case "1":
			m.setTab(0)
		case "2":
			m.setTab(1)
		case "3":
			m.setTab(2)
		case "4":
			m.setTab(3)
		case "5":
			m.setTab(4)
		}

		// ── Manual refresh ──
		if key == "r" && m.activeTab != 2 {
			cmds = append(cmds, m.fetchStats)
		}

		// ── Delegate to active tab ──
		if cmd := m.updateActiveTab(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case pollMsg:
		if !m.polling {
			m.polling = true
			cmds = append(cmds, m.fetchStats)
			cmds = append(cmds, m.fetchGardener)
			cmds = append(cmds, m.fetchIndexes)
			cmds = append(cmds, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return pollMsg{}
			}))
		}

	case statsMsg:
		m.polling = false
		if msg.err != nil {
			m.statsErr = msg.err
		} else {
			m.stats = msg.stats
			m.statsErr = nil
		}

	case gardenerMsg:
		if msg.err == nil {
			m.gardener = msg.status
		}

	case indexesMsg:
		if msg.err == nil {
			m.indexes = msg.indexes
			if m.searchIndex == "" && len(m.indexes) > 0 {
				m.searchIndex = m.indexes[0].Name
			}
		}

	case searchResultMsg:
		m.searchLoading = false
		if msg.err != nil {
			m.searchErr = msg.err
		} else {
			m.searchResults = msg.results
			m.searchUIRaws = msg.raw
			m.searchErr = nil
		}

	case graphRelationsMsg:
		if msg.err == nil {
			targets := make([]string, len(msg.relations))
			for i, r := range msg.relations {
				targets[i] = r.TargetID
				edgeKey := msg.nodeID + "->" + r.TargetID
				m.graphRelTypes[edgeKey] = r.RelType
			}
			m.graphEdges[msg.nodeID] = targets
		}
		m.graphErr = msg.err

	case graphLoadMsg:
		m.graphSearch = false
		if msg.err == nil {
			if len(msg.nodes) == 1 {
				m.graphFocus = msg.nodes[0].id
				cmds = append(cmds, m.fetchGraphRelations(m.graphFocus))
			} else if len(msg.nodes) > 1 {
				m.graphNodeList = msg.nodes
				m.graphListIdx = 0
			}
			m.graphErr = nil
		} else {
			m.graphErr = msg.err
		}

	case embedderReloadMsg:
		cmds = append(cmds, m.fetchStats)
	}

	return m, tea.Batch(cmds...)
}

func (m *MainModel) View() tea.View {
	if !m.ready {
		return tea.NewView("Loading...\n")
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		m.renderContent(),
		m.renderFooter(),
	)

	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "KektorDB TUI"
	return v
}

func (m *MainModel) renderContent() string {
	var tab string
	switch m.activeTab {
	case 0:
		tab = m.renderDashboard()
	case 1:
		tab = m.renderGraph()
	case 2:
		tab = m.renderSearch()
	case 3:
		tab = m.renderTimeline()
	case 4:
		tab = m.renderSettings()
	}

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height - 5).
		MaxHeight(m.height - 5).
		Render(tab)
}

func (m *MainModel) setTab(tab int) {
	old := m.activeTab
	m.activeTab = tab

	// ── Cleanup tab precedente ──
	m.graphSearch = false
	if old == 1 || old == 2 {
		m.searchInput.Blur()
		m.filterInput.Blur()
	}

	// ── Setup nuovo tab ──
	if tab == 2 {
		m.searchFocus = 0
		m.syncSearchFocus()
	}
}

func (m *MainModel) renderHeader() string {
	left := styleTitle.Render(" KektorDB TUI ")
	if m.stats != nil {
		embInfo := fmt.Sprintf("Embedder: %s (%dd)", m.stats.Embedder.Active, m.stats.Embedder.Dimension)
		left += styleSuccess.Render(" " + embInfo + " ✓")
	}
	right := ""
	if m.stats != nil {
		h := m.stats.UptimeSeconds / 3600
		min := (m.stats.UptimeSeconds % 3600) / 60
		right = fmt.Sprintf("Uptime: %dh %dm", h, min)
	}
	space := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if space < 1 {
		space = 1
	}
	return left + strings.Repeat(" ", space) + right
}

func (m *MainModel) renderFooter() string {
	var tabs []string
	for i, name := range m.tabs {
		label := fmt.Sprintf("[%d] %s", i+1, name)
		if i == m.activeTab {
			tabs = append(tabs, styleTabActive.Render(label))
		} else {
			tabs = append(tabs, styleTabInactive.Render(label))
		}
	}
	keys := "  [1-5] tabs  [Esc] clear  [r] refresh  [q] quit"
	return styleFooter.Render(strings.Join(tabs, " ") + keys)
}

func (m *MainModel) updateActiveTab(msg tea.KeyPressMsg) tea.Cmd {
	switch m.activeTab {
	case 1:
		return m.updateGraph(msg)
	case 2:
		return m.updateSearch(msg)
	case 3:
		return m.updateTimeline(msg)
	case 4:
		return m.updateSettings(msg)
	}
	return nil
}

func (m *MainModel) fetchStats() tea.Msg {
	stats, err := m.client.FetchStats()
	return statsMsg{stats: stats, err: err}
}

func (m *MainModel) fetchGardener() tea.Msg {
	status, err := m.client.FetchGardener()
	return gardenerMsg{status: status, err: err}
}

func (m *MainModel) fetchIndexes() tea.Msg {
	indexes, err := m.client.FetchIndexes()
	return indexesMsg{indexes: indexes, err: err}
}
