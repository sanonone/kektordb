package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/llm"
)

// Tests for the status command's presentation and exit codes.
//
// The layout rules are the contract with the reader: numbers grouped and sized,
// empty sections omitted, warnings last. If grouping regresses, the output still
// runs and quietly reads worse — which is why it is tested.

func TestFormatThousands(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1.000"},
		{8412, "8.412"},
		{1234567, "1.234.567"},
		{-1500, "-1.500"},
	}
	for _, tt := range tests {
		if got := formatThousands(tt.in); got != tt.want {
			t.Errorf("formatThousands(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		mb   float64
		want string
	}{
		{0, "0 KB"},
		{0.5, "512 KB"},
		{12.4, "12.4 MB"},
		{2048, "2.0 GB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.mb); got != tt.want {
			t.Errorf("formatBytes(%v) = %q, want %q", tt.mb, got, tt.want)
		}
	}
}

func TestFormatAgo(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s fa"},
		{5 * time.Minute, "5m fa"},
		{3 * time.Hour, "3h fa"},
		{48 * time.Hour, "2g fa"},
	}
	for _, tt := range tests {
		if got := formatAgo(tt.d); got != tt.want {
			t.Errorf("formatAgo(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "0m"},
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h 30m"},
		{76 * time.Hour, "3g 4h"},
	}
	for _, tt := range tests {
		if got := formatDuration(tt.d); got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestStatusOmitsEmptySections is the rule that keeps the view readable.
func TestStatusOmitsEmptySections(t *testing.T) {
	st := server.SystemStatus{
		Version: "v1",
		Memory: &server.StatusMemory{
			Indexes:      []server.StatusIndex{},
			TotalVectors: 0,
		},
		Maintenance: &server.StatusMaint{Enabled: false, Features: []server.StatusFeature{}},
		LLM:         &server.StatusLLM{Configured: false, Features: []llm.FeatureUsageEntry{}},
	}

	var buf bytes.Buffer
	printStatus(&buf, st, "http://x")
	out := buf.String()

	if !strings.Contains(out, "MEMORIA") {
		t.Error("memory header missing")
	}
	if strings.Contains(out, "grafo:") {
		t.Error("graph line shown for a graph with no edges")
	}
	if !strings.Contains(out, "spenta") {
		t.Error("a disabled maintenance engine should say so")
	}
	if !strings.Contains(out, "nessun modello configurato") {
		t.Error("LLM section should say the model is not configured")
	}
	if strings.Contains(out, "ATTENZIONE") {
		t.Error("the attention section appeared with no warnings")
	}
}

// TestStatusShowsWarningsLast pins the ordering: warnings are the last thing a
// reader sees, so they are not scrolled past.
func TestStatusShowsWarningsLast(t *testing.T) {
	st := server.SystemStatus{
		Version:  "v1",
		Warnings: []string{"first warning", "second warning"},
	}
	var buf bytes.Buffer
	printStatus(&buf, st, "http://x")
	out := buf.String()

	idx := strings.Index(out, "ATTENZIONE")
	if idx < 0 {
		t.Fatal("no attention section for a status with warnings")
	}
	if strings.Count(out, "warning") != 2 {
		t.Errorf("warnings not all printed:\n%s", out)
	}
	if strings.Index(out, "second warning") < idx {
		t.Error("warnings appear before the attention header")
	}
}

// TestStatusLayersCompact checks the per-layer breakdown keeps its order.
func TestStatusLayersCompact(t *testing.T) {
	got := formatLayers(map[string]int{"semantic": 1800, "episodic": 3100, "procedural": 340})
	want := "episodic 3.100 / semantic 1.800 / procedural 340"
	if got != want {
		t.Errorf("formatLayers = %q, want %q", got, want)
	}
}

// TestShortFeature trims the shared prefix, which carries no information.
func TestShortFeature(t *testing.T) {
	if got := shortFeature("gardener_contradiction"); got != "contradiction" {
		t.Errorf("shortFeature = %q", got)
	}
	if got := shortFeature("other"); got != "other" {
		t.Errorf("shortFeature stripped a prefix that was not there: %q", got)
	}
}

// TestFetchStatusServerDown is the exit-2 path.
func TestFetchStatusServerDown(t *testing.T) {
	_, err := fetchStatus(statusOptions{baseURL: "http://127.0.0.1:1"})
	if err == nil {
		t.Fatal("fetchStatus succeeded against a closed port")
	}
}

// TestRunStatusExitCodeWarnings pins the scriptable contract.
func TestRunStatusExitCodeWarnings(t *testing.T) {
	if statusExitWarnings == statusExitOK || statusExitWarnings == statusExitNoServer {
		t.Fatal("the exit codes are not distinct, so a script cannot tell them apart")
	}
}
