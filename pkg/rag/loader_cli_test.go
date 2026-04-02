package rag

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCLILoader_Success(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello from file"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use `cat` to read the file (Linux/macOS)
	loader := NewCLILoader([]string{"cat", "{{file_path}}"}, 5*time.Second)
	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Text != "hello from file" {
		t.Errorf("got %q, want %q", doc.Text, "hello from file")
	}
}

func TestCLILoader_FileReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "hello world.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewCLILoader([]string{"cat", "{{file_path}}"}, 5*time.Second)
	doc, err := loader.Load(testFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Text != "content" {
		t.Errorf("got %q, want %q", doc.Text, "content")
	}
}

func TestCLILoader_Timeout(t *testing.T) {
	// Use `sleep` with a timeout shorter than the sleep duration
	loader := NewCLILoader([]string{"sleep", "10"}, 100*time.Millisecond)
	_, err := loader.Load("/dev/null")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCLILoader_CommandNotFound(t *testing.T) {
	loader := NewCLILoader([]string{"nonexistent_tool_xyz", "{{file_path}}"}, 5*time.Second)
	_, err := loader.Load("/dev/null")
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestCLILoader_EmptyCommand(t *testing.T) {
	loader := NewCLILoader([]string{}, 5*time.Second)
	_, err := loader.Load("/dev/null")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestCLILoader_EmptyOutput(t *testing.T) {
	// `true` produces no output
	loader := NewCLILoader([]string{"true"}, 5*time.Second)
	_, err := loader.Load("/dev/null")
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

func TestCLILoader_DefaultTimeout(t *testing.T) {
	loader := NewCLILoader([]string{"cat"}, 0)
	if loader.timeout != defaultCLITimeout {
		t.Errorf("default timeout = %v, want %v", loader.timeout, defaultCLITimeout)
	}
}
