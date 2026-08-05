package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/internal/version"
)

func TestVersionString(t *testing.T) {
	want := "kektordb " + version.Version
	if got := versionString(); got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
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
		name      string
		authToken string
		httpAddr  string
		wantWarn  bool
	}{
		{"auth disabled on all-interfaces", "", ":9091", true},
		{"auth disabled on 0.0.0.0", "", "0.0.0.0:9091", true},
		{"auth disabled on LAN", "", "192.168.1.5:9091", true},
		{"auth disabled on IPv6 all", "", "[::]:9091", true},
		{"auth set on all-interfaces", "secret", ":9091", false},
		{"auth disabled on localhost", "", "localhost:9091", false},
		{"auth disabled on 127.0.0.1", "", "127.0.0.1:9091", false},
		{"auth disabled on ::1", "", "[::1]:9091", false},
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
		})
	}
}
