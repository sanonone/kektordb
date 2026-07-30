package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
)

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
