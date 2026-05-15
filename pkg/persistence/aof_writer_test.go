package persistence

import (
	"path/filepath"
	"testing"
)

// TestNewAOFWriter_ZeroBufferSizeFallsBackToDefault verifies that passing bufferSize=0
// causes NewAOFWriter to use DefaultAOFWriteBufferSize without error.
func TestNewAOFWriter_ZeroBufferSizeFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	w, err := NewAOFWriter(path, 0)
	if err != nil {
		t.Fatalf("NewAOFWriter(path, 0) unexpected error: %v", err)
	}
	defer w.Close()

	if err := w.Write("PING"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
}

// TestNewAOFWriter_NegativeBufferSizeFallsBackToDefault verifies that passing a
// negative bufferSize also falls back to DefaultAOFWriteBufferSize.
func TestNewAOFWriter_NegativeBufferSizeFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	w, err := NewAOFWriter(path, -1)
	if err != nil {
		t.Fatalf("NewAOFWriter(path, -1) unexpected error: %v", err)
	}
	defer w.Close()

	if err := w.Write("PING"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
}

// TestNewAOFWriter_CustomBufferSize verifies that an explicit positive bufferSize is
// accepted and the writer functions correctly.
func TestNewAOFWriter_CustomBufferSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	w, err := NewAOFWriter(path, 512)
	if err != nil {
		t.Fatalf("NewAOFWriter(path, 512) unexpected error: %v", err)
	}
	defer w.Close()

	if err := w.Write("PING"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
}
