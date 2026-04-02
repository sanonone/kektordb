package rag

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSmartLoader_NoCLI_UsesFallback(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("fallback content"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ParserConfig{Type: "internal"}
	loader := NewSmartLoader(cfg, false, "")

	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Text != "fallback content" {
		t.Errorf("got %q, want %q", doc.Text, "fallback content")
	}
}

func TestSmartLoader_CLIFallback(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("fallback text"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Configure CLI with a command that will fail (nonexistent tool)
	cfg := ParserConfig{
		Type:     "cli",
		Command:  []string{"nonexistent_tool_xyz", "{{file_path}}"},
		Timeout:  "1s",
		Fallback: "internal",
	}
	loader := NewSmartLoader(cfg, false, "")

	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if doc.Text != "fallback text" {
		t.Errorf("got %q, want %q", doc.Text, "fallback text")
	}
}

func TestSmartLoader_CLISuccess(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("cli output"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ParserConfig{
		Type:    "cli",
		Command: []string{"cat", "{{file_path}}"},
		Timeout: "5s",
	}
	loader := NewSmartLoader(cfg, false, "")

	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Text != "cli output" {
		t.Errorf("got %q, want %q", doc.Text, "cli output")
	}
}

func TestSmartLoader_EmptyCLICommand_UsesFallback(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ParserConfig{Type: "cli", Command: nil}
	loader := NewSmartLoader(cfg, false, "")

	if loader.cliLoader != nil {
		t.Error("cliLoader should be nil for empty command")
	}

	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Text != "text" {
		t.Errorf("got %q, want %q", doc.Text, "text")
	}
}

func TestSmartLoader_CLITimeout_FallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("fallback"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ParserConfig{
		Type:    "cli",
		Command: []string{"sleep", "10"},
		Timeout: "100ms",
	}
	loader := NewSmartLoader(cfg, false, "")

	start := time.Now()
	doc, err := loader.Load(testFile)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if doc.Text != "fallback" {
		t.Errorf("got %q, want %q", doc.Text, "fallback")
	}
	// Should complete quickly due to timeout + fallback
	if elapsed > 5*time.Second {
		t.Errorf("took too long: %v (timeout should have triggered)", elapsed)
	}
}
