package tui

import (
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type MainModel struct {
	client   *TUIClient
	httpAddr string

	width  int
	height int
	ready  bool

	activeTab int
	tabs      []string

	lastPoll time.Time
	polling  bool

	stats     *SystemStats
	gardener  *GardenerStatus
	indexes   []IndexInfo
	statsErr  error

	events      []SSEEvent
	eventsMu    sync.Mutex
	pauseEvents bool
	eventFilter string // empty=all, or event type like "vector.add"
	sseCh       chan tea.Msg

	graphFocus       string
	graphNodes       map[string]GraphNode
	graphEdges       map[string][]string
	graphRelTypes    map[string]string
	graphStack       []string
	graphErr         error
	graphSearch      bool
	graphSelectedIdx int
	graphNodeList    []graphNodeView
	graphListIdx     int
	graphListLimit   int

	searchInput    textinput.Model
	filterInput    textinput.Model
	searchViewport viewport.Model
	searchFocus    int // 0=textinput, 1=index/alpha, 2=filter, 3=relations, 4=limit, 99=results
	searchMode     string
	searchAlpha    float64
	searchFilter   string
	searchRelations   []string
	searchResults     []SearchResult
	searchUIRaws      []SearchResultWithNode
	searchErr         error
	searchLoading     bool
	searchIndex       string
	searchK          int

	embedderMode string
}

func NewMainModel(httpAddr string) *MainModel {
	ti := textinput.New()
	ti.Placeholder = "Type search query..."
	ti.CharLimit = 200
	ti.SetWidth(50)
	ti.Focus()

	fi := textinput.New()
	fi.Placeholder = "SQL-like filter..."
	fi.CharLimit = 200
	fi.SetWidth(50)
	fi.Blur()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))

	return &MainModel{
		client:         NewTUIClient(httpAddr),
		httpAddr:       httpAddr,
		tabs:           []string{"Dashboard", "Graph", "Search", "Timeline", "Settings"},
		searchInput:    ti,
		filterInput:    fi,
		searchViewport: vp,
		searchFocus:    0,
		searchMode:     "quick",
		searchAlpha:    0.5,
		searchIndex:    "",
		searchK:        10,
		embedderMode:   "auto",
		graphNodes:     make(map[string]GraphNode),
		graphEdges:     make(map[string][]string),
		graphRelTypes:  make(map[string]string),
		graphSelectedIdx: 0,
		graphListLimit: 50,
	}
}

func (m *MainModel) Init() tea.Cmd {
	m.sseCh = make(chan tea.Msg, 32)
	go m.readSSELoop()
	return tea.Batch(
		tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
			return pollMsg{}
		}),
		textinput.Blink,
		waitForSSE(m.sseCh),
	)
}

type pollMsg struct{}

type statsMsg struct {
	stats *SystemStats
	err   error
}

type gardenerMsg struct {
	status *GardenerStatus
	err    error
}

type indexesMsg struct {
	indexes []IndexInfo
	err     error
}

type searchResultMsg struct {
	results []SearchResult
	raw     []SearchResultWithNode
	err     error
}

type graphRelationsMsg struct {
	nodeID    string
	relations []struct {
		TargetID string
		RelType  string
	}
	err error
}

type embedderReloadMsg struct {
	response *EmbedderReloadResponse
	err      error
}

type sseEventMsg struct {
	event SSEEvent
}

type sseErrMsg struct {
	err error
}
