package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestGracefulShutdownWithBackgroundTasks verifies that Close() waits for background goroutines
func TestGracefulShutdownWithBackgroundTasks(t *testing.T) {
	// Create a temporary directory for test data
	opts := DefaultOptions(t.TempDir())

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}

	// Create the index explicitly
	if err := eng.VCreate("test_idx", distance.Cosine, 16, 200, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	vec := make([]float32, 10)
	for i := range vec {
		vec[i] = float32(i) * 0.1
	}

	// Add vectors
	if err := eng.VAdd("test_idx", "node1", vec, map[string]any{"type": "test"}); err != nil {
		t.Fatalf("Failed to add node1: %v", err)
	}
	if err := eng.VAdd("test_idx", "node2", vec, map[string]any{"type": "test"}); err != nil {
		t.Fatalf("Failed to add node2: %v", err)
	}
	if err := eng.VAdd("test_idx", "node3", vec, map[string]any{"type": "test"}); err != nil {
		t.Fatalf("Failed to add node3: %v", err)
	}

	// Create links between nodes (to trigger cascade delete)
	if err := eng.VLink("test_idx", "node2", "node1", "points_to", "", 1.0, nil); err != nil {
		t.Fatalf("Failed to link: %v", err)
	}
	if err := eng.VLink("test_idx", "node3", "node1", "points_to", "", 1.0, nil); err != nil {
		t.Fatalf("Failed to link: %v", err)
	}

	// Delete node1 (triggers cascade delete in background)
	if err := eng.VDelete("test_idx", "node1"); err != nil {
		t.Fatalf("Failed to delete: %v", err)
	}

	// Small delay to allow the goroutine to start
	time.Sleep(50 * time.Millisecond)

	// Close should wait for background goroutines
	done := make(chan error, 1)
	go func() {
		done <- eng.Close()
	}()

	// Wait for close with timeout
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close returned error: %v", err)
		}
		// Success - Close completed
	case <-time.After(5 * time.Second):
		t.Error("Close() timed out - background goroutines may not be properly tracked")
	}
}

// TestContextCancellation verifies that context is cancelled during shutdown
func TestContextCancellation(t *testing.T) {
	opts := DefaultOptions(t.TempDir())

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}

	// Verify context exists and is not cancelled
	if eng.ctx == nil {
		t.Fatal("Engine context should not be nil")
	}

	select {
	case <-eng.ctx.Done():
		t.Error("Context should not be cancelled before Close()")
	default:
		// Good - context is active
	}

	// Close should cancel the context
	if err := eng.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}

	// After close, context should be cancelled
	select {
	case <-eng.ctx.Done():
		// Good - context was cancelled
	default:
		t.Error("Context should be cancelled after Close()")
	}
}
