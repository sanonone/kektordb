package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/internal/version"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

func TestVersionString(t *testing.T) {
	want := "kektordb " + version.Version
	if got := versionString(); got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
	}
}

func TestSeedDemoData(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := seedDemoData(eng, embeddings.NoopEmbedder{}); err != nil {
		t.Fatalf("seedDemoData: %v", err)
	}

	if !eng.IndexExists("mcp_memory") {
		t.Fatal("expected mcp_memory index after seeding")
	}

	// All demo memories are searchable by vector (deterministic hash vectors).
	queryVec := demoHashVec(demoMemories[1].content)
	results, err := eng.VSearchWithScores("mcp_memory", queryVec, 10)
	if err != nil {
		t.Fatalf("VSearchWithScores: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for a demo query")
	}
	if results[0].ID != demoMemories[1].id {
		t.Errorf("expected top hit %q, got %q", demoMemories[1].id, results[0].ID)
	}

	// Entity + links are present.
	if _, err := eng.VGet("mcp_memory", "entity:project_kektordb"); err != nil {
		t.Errorf("expected entity node: %v", err)
	}
	edges, found := eng.VGetEdges("mcp_memory", "mem_gardener", "mentions", 0)
	if !found || len(edges) == 0 {
		t.Error("expected 'mentions' edge from mem_gardener to the entity")
	}

	// Demo vectors are deterministic (seeding twice yields identical vectors).
	v1 := demoHashVec("same text")
	v2 := demoHashVec("same text")
	if len(v1) != len(v2) || len(v1) != 384 {
		t.Fatalf("demo vector dim = %d, want 384", len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatal("demo vectors not deterministic")
		}
	}
}

func TestSetupLogger_WritesToProvidedWriter(t *testing.T) {
	var buf bytes.Buffer
	setupLogger("info", &buf)

	slog.Info("hello from test")

	output := buf.String()
	if !strings.Contains(output, "hello from test") {
		t.Errorf("expected log in buffer, got: %s", output)
	}
}

func TestSetupLogger_DoesNotWriteToStdoutWhenRedirected(t *testing.T) {
	// Capture the real stdout.
	realStdout := os.Stdout
	defer func() { os.Stdout = realStdout }()

	// Create a pipe as fake stdout.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	// Redirect slog to a buffer (MCP mode).
	var buf bytes.Buffer
	setupLogger("info", &buf)
	slog.Info("should go to buffer not stdout")

	// Close the write end so ReadAll completes.
	w.Close()

	// Read whatever was written to the fake stdout.
	var stdoutBuf bytes.Buffer
	_, _ = stdoutBuf.ReadFrom(r)

	if stdoutBuf.Len() > 0 {
		t.Errorf("stdout leaked %d bytes: %s", stdoutBuf.Len(), stdoutBuf.String())
	}
	if !strings.Contains(buf.String(), "should go to buffer not stdout") {
		t.Error("log message missing from redirect buffer")
	}
}

func TestSetupLogger_NonMCPWritesToStdout(t *testing.T) {
	// This test validates the default behaviour: when no redirect is active,
	// setupLogger writes to the provided writer (os.Stdout in production).
	// We use a buffer here to avoid spamming test output with log lines.
	var buf bytes.Buffer
	setupLogger("info", &buf)
	slog.Info("stdout-mode test")

	if !strings.Contains(buf.String(), "stdout-mode test") {
		t.Error("log message missing from writer")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"localhost:9091", true},
		{"127.0.0.1:9091", true},
		{"127.1.2.3:9091", true},
		{"[::1]:9091", true},
		{"::1", true},
		{":9091", false},
		{"0.0.0.0:9091", false},
		{"[::]:9091", false},
		{"192.168.1.5:9091", false},
		{"10.0.0.1:9091", false},
		{"myserver:9091", false},
		{"127.0.0.1", true},
	}
	for _, tt := range tests {
		if got := isLoopbackHost(tt.addr); got != tt.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestSecurityWarnings(t *testing.T) {
	tests := []struct {
		name         string
		authToken    string
		httpAddr     string
		wantWarn     bool
		wantContains string
	}{
		{"auth disabled on all-interfaces", "", ":9091", true, ""},
		{"auth disabled on 0.0.0.0", "", "0.0.0.0:9091", true, ""},
		{"auth disabled on LAN", "", "192.168.1.5:9091", true, ""},
		{"auth disabled on IPv6 all", "", "[::]:9091", true, ""},
		{"auth disabled on custom port", "", "0.0.0.0:8088", true, "127.0.0.1:8088"},
		{"auth set on all-interfaces", "secret", ":9091", false, ""},
		{"auth disabled on localhost", "", "localhost:9091", false, ""},
		{"auth disabled on 127.0.0.1", "", "127.0.0.1:9091", false, ""},
		{"auth disabled on ::1", "", "[::1]:9091", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := securityWarnings(tt.authToken, tt.httpAddr)
			if tt.wantWarn && len(warnings) == 0 {
				t.Errorf("expected warning for authToken=%q httpAddr=%q, got none", tt.authToken, tt.httpAddr)
			}
			if !tt.wantWarn && len(warnings) > 0 {
				t.Errorf("unexpected warnings for authToken=%q httpAddr=%q: %v", tt.authToken, tt.httpAddr, warnings)
			}
			if tt.wantContains != "" {
				if len(warnings) == 0 || !strings.Contains(warnings[0], tt.wantContains) {
					t.Errorf("warning should mention %q, got: %v", tt.wantContains, warnings)
				}
			}
		})
	}
}
