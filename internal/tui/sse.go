package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// waitForSSE returns a command that reads the next SSE message from the channel.
func waitForSSE(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return sseErrMsg{err: fmt.Errorf("SSE channel closed")}
		}
		return msg
	}
}

// readSSELoop connects to GET /events/stream and pushes parsed events onto sseCh.
// On disconnect it retries after a 2-second delay.
func (m *MainModel) readSSELoop() {
	defer close(m.sseCh)

	for {
		m.streamSSE()
		select {
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *MainModel) streamSSE() {
	url := fmt.Sprintf("http://%s/events/stream", m.httpAddr)
	resp, err := http.Get(url)
	if err != nil {
		m.sseCh <- sseErrMsg{err: err}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		m.sseCh <- sseErrMsg{err: fmt.Errorf("SSE stream returned status %d", resp.StatusCode)}
		return
	}

	reader := bufio.NewReader(resp.Body)
	var dataLines []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			m.sseCh <- sseErrMsg{err: err}
			return
		}

		line = strings.TrimSpace(line)

		if line == "" {
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "")
				var event SSEEvent
				if json.Unmarshal([]byte(data), &event) == nil {
					m.sseCh <- sseEventMsg{event: event}
				}
				dataLines = nil
			}
		} else if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(line[5:])
			dataLines = append(dataLines, payload)
		}
	}
}
