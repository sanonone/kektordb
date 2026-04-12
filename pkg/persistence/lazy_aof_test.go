package persistence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLazyAOFWriterSnapshotMode tests the snapshot mode functionality
// which prevents AOF data loss during snapshot+truncate operations.
func TestLazyAOFWriterSnapshotMode(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	// Create underlying AOF writer
	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	// Create lazy writer with short flush interval for testing
	lw := NewLazyAOFWriterWithConfig(
		underlying,
		50*time.Millisecond,  // flush interval
		200*time.Millisecond, // sync interval
		10,                   // max buffer size
	)
	defer lw.Close()

	// Write some initial data
	if err := lw.Write("CMD1"); err != nil {
		t.Fatalf("Failed to write CMD1: %v", err)
	}
	if err := lw.Write("CMD2"); err != nil {
		t.Fatalf("Failed to write CMD2: %v", err)
	}

	// Wait for flush
	time.Sleep(100 * time.Millisecond)

	// Begin snapshot mode
	if err := lw.BeginSnapshotMode(); err != nil {
		t.Fatalf("Failed to begin snapshot mode: %v", err)
	}

	if !lw.IsSnapshotModeActive() {
		t.Error("Snapshot mode should be active")
	}

	// Write data during snapshot mode - these should go to shadow buffer
	if err := lw.Write("SHADOW1"); err != nil {
		t.Fatalf("Failed to write SHADOW1: %v", err)
	}
	if err := lw.Write("SHADOW2"); err != nil {
		t.Fatalf("Failed to write SHADOW2: %v", err)
	}
	if err := lw.Write("SHADOW3"); err != nil {
		t.Fatalf("Failed to write SHADOW3: %v", err)
	}

	// Wait - shadow writes should NOT be flushed automatically
	time.Sleep(100 * time.Millisecond)

	// Check file size before truncate
	info1, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Failed to stat AOF: %v", err)
	}
	sizeBefore := info1.Size()

	// Truncate AOF (simulating what happens during snapshot)
	if err := lw.Truncate(); err != nil {
		t.Fatalf("Failed to truncate AOF: %v", err)
	}

	// End snapshot mode and get shadow writes
	shadowWrites, err := lw.EndSnapshotMode()
	if err != nil {
		t.Fatalf("Failed to end snapshot mode: %v", err)
	}

	if lw.IsSnapshotModeActive() {
		t.Error("Snapshot mode should not be active after EndSnapshotMode")
	}

	// Verify we got the shadow writes
	if len(shadowWrites) != 3 {
		t.Errorf("Expected 3 shadow writes, got %d", len(shadowWrites))
	}

	// Write shadow writes back to AOF
	for _, write := range shadowWrites {
		if err := lw.Write(write); err != nil {
			t.Fatalf("Failed to write shadow entry: %v", err)
		}
	}

	// Flush to disk
	if err := lw.Flush(); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Verify AOF has the shadow writes
	info2, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Failed to stat AOF after: %v", err)
	}

	// After truncate + rewriting shadow entries, file should have some content
	if info2.Size() == 0 {
		t.Error("AOF should have shadow writes after truncate, but is empty")
	}

	t.Logf("AOF size before truncate: %d, after truncate+shadow: %d", sizeBefore, info2.Size())
}

// TestLazyAOFWriterSnapshotModeConcurrent tests concurrent writes during snapshot mode
func TestLazyAOFWriterSnapshotModeConcurrent(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	lw := NewLazyAOFWriterWithConfig(
		underlying,
		10*time.Millisecond,
		50*time.Millisecond,
		100,
	)
	defer lw.Close()

	// Start goroutine that writes continuously
	done := make(chan struct{})
	writeCount := 0
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if err := lw.Write("CONCURRENT_WRITE"); err != nil {
				t.Errorf("Write failed: %v", err)
				return
			}
			writeCount++
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Begin snapshot mode while writes are happening
	time.Sleep(20 * time.Millisecond)
	if err := lw.BeginSnapshotMode(); err != nil {
		t.Fatalf("Failed to begin snapshot mode: %v", err)
	}

	// Let more writes happen during snapshot mode
	time.Sleep(50 * time.Millisecond)

	// End snapshot mode
	shadowWrites, err := lw.EndSnapshotMode()
	if err != nil {
		t.Fatalf("Failed to end snapshot mode: %v", err)
	}

	// Wait for writer goroutine to finish
	<-done

	t.Logf("Total writes: %d, Shadow writes: %d", writeCount, len(shadowWrites))

	// Some writes should have gone to shadow buffer
	if len(shadowWrites) == 0 {
		t.Error("Expected some shadow writes during concurrent snapshot mode")
	}
}

// TestLazyAOFWriterSnapshotModeDoubleBegin tests error on double BeginSnapshotMode
func TestLazyAOFWriterSnapshotModeDoubleBegin(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	lw := NewLazyAOFWriter(underlying)
	defer lw.Close()

	if err := lw.BeginSnapshotMode(); err != nil {
		t.Fatalf("First BeginSnapshotMode should succeed: %v", err)
	}

	if err := lw.BeginSnapshotMode(); err == nil {
		t.Error("Second BeginSnapshotMode should fail")
	}
}

// TestLazyAOFWriterSnapshotModeEndWithoutBegin tests error on EndSnapshotMode without Begin
func TestLazyAOFWriterSnapshotModeEndWithoutBegin(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	lw := NewLazyAOFWriter(underlying)
	defer lw.Close()

	_, err = lw.EndSnapshotMode()
	if err == nil {
		t.Error("EndSnapshotMode without BeginSnapshotMode should fail")
	}
}

// TestLazyAOFWriterSnapshotModeClosedWriter tests error on closed writer
func TestLazyAOFWriterSnapshotModeClosedWriter(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	lw := NewLazyAOFWriter(underlying)
	lw.Close()

	if err := lw.BeginSnapshotMode(); err == nil {
		t.Error("BeginSnapshotMode on closed writer should fail")
	}
}

// TestLazyAOFWriterSnapshotModePreservesOrder tests that shadow writes preserve order
func TestLazyAOFWriterSnapshotModePreservesOrder(t *testing.T) {
	tempDir := t.TempDir()
	aofPath := filepath.Join(tempDir, "test.aof")

	underlying, err := NewAOFWriter(aofPath)
	if err != nil {
		t.Fatalf("Failed to create AOF writer: %v", err)
	}

	lw := NewLazyAOFWriter(underlying)
	defer lw.Close()

	if err := lw.BeginSnapshotMode(); err != nil {
		t.Fatalf("Failed to begin snapshot mode: %v", err)
	}

	// Write numbered entries
	for i := 0; i < 10; i++ {
		cmd := string(rune('A' + i))
		if err := lw.Write(cmd); err != nil {
			t.Fatalf("Failed to write %s: %v", cmd, err)
		}
	}

	shadowWrites, err := lw.EndSnapshotMode()
	if err != nil {
		t.Fatalf("Failed to end snapshot mode: %v", err)
	}

	// Verify order is preserved
	if len(shadowWrites) != 10 {
		t.Fatalf("Expected 10 shadow writes, got %d", len(shadowWrites))
	}

	for i, write := range shadowWrites {
		expected := string(rune('A' + i))
		if write != expected {
			t.Errorf("Write %d: expected %s, got %s", i, expected, write)
		}
	}
}
