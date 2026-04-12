package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotAOFShadowBuffer verifies the shadow buffer mechanism works correctly
func TestSnapshotAOFShadowBuffer(t *testing.T) {
	tempDir := t.TempDir()

	opts := DefaultOptions(tempDir)

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Write some KV data
	for i := 0; i < 5; i++ {
		key := "key_" + string(rune('A'+i))
		value := []byte("value_" + string(rune('A'+i)))
		if err := eng.KVSet(key, value); err != nil {
			t.Fatalf("Failed to set key: %v", err)
		}
	}

	// Flush to ensure data is on disk
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Get AOF size before snapshot
	aofPath := filepath.Join(tempDir, opts.AofFilename)
	info1, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Failed to stat AOF: %v", err)
	}
	sizeBefore := info1.Size()
	t.Logf("AOF size before snapshot: %d", sizeBefore)

	// Add some data that will be captured in the shadow buffer
	for i := 0; i < 5; i++ {
		key := "concurrent_key_" + string(rune('A'+i))
		value := []byte("concurrent_value_" + string(rune('0'+i)))
		if err := eng.KVSet(key, value); err != nil {
			t.Fatalf("Failed to set concurrent key: %v", err)
		}
	}

	// Trigger snapshot
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	t.Log("Snapshot completed successfully")

	// Close and check
	if err := eng.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	// Check AOF exists and has content
	info2, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Failed to stat AOF after: %v", err)
	}
	t.Logf("AOF size after snapshot: %d", info2.Size())

	// Restart and verify
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to restart: %v", err)
	}
	defer eng2.Close()

	// Check that we have keys
	val, found := eng2.KVGet("key_A")
	if !found {
		t.Error("Initial key not found after restart")
	} else if string(val) != "value_A" {
		t.Errorf("Wrong value for key_A: got %s, want value_A", string(val))
	}

	// Check concurrent keys are present
	val2, found2 := eng2.KVGet("concurrent_key_A")
	if !found2 {
		t.Error("Concurrent key not found after restart - shadow buffer may not be working")
	} else if string(val2) != "concurrent_value_0" {
		t.Errorf("Wrong value for concurrent_key_A: got %s, want concurrent_value_0", string(val2))
	}

	t.Log("AOF shadow buffer test passed")
}

// TestSnapshotWithShadowBufferActive verifies snapshot mode is active during snapshot
func TestSnapshotWithShadowBufferActive(t *testing.T) {
	tempDir := t.TempDir()
	opts := DefaultOptions(tempDir)

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}
	defer eng.Close()

	// Check that snapshot mode is not active initially
	if eng.AOF.IsSnapshotModeActive() {
		t.Error("Snapshot mode should not be active initially")
	}

	// Manually test the snapshot mode
	if err := eng.AOF.BeginSnapshotMode(); err != nil {
		t.Fatalf("Failed to begin snapshot mode: %v", err)
	}

	if !eng.AOF.IsSnapshotModeActive() {
		t.Error("Snapshot mode should be active after BeginSnapshotMode")
	}

	// Write during snapshot mode
	if err := eng.AOF.Write("TEST_CMD"); err != nil {
		t.Fatalf("Failed to write during snapshot mode: %v", err)
	}

	// End snapshot mode
	shadowWrites, err := eng.AOF.EndSnapshotMode()
	if err != nil {
		t.Fatalf("Failed to end snapshot mode: %v", err)
	}

	if eng.AOF.IsSnapshotModeActive() {
		t.Error("Snapshot mode should not be active after EndSnapshotMode")
	}

	if len(shadowWrites) != 1 {
		t.Errorf("Expected 1 shadow write, got %d", len(shadowWrites))
	}

	if shadowWrites[0] != "TEST_CMD" {
		t.Errorf("Expected 'TEST_CMD', got '%s'", shadowWrites[0])
	}

	t.Log("Shadow buffer active test passed")
}

// TestSnapshotNoDataLossSimple is a simplified version of the data loss test
func TestSnapshotNoDataLossSimple(t *testing.T) {
	tempDir := t.TempDir()
	opts := DefaultOptions(tempDir)

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Write initial data
	for i := 0; i < 10; i++ {
		key := "key_" + string(rune('0'+i))
		value := []byte("value_" + string(rune('0'+i)))
		if err := eng.KVSet(key, value); err != nil {
			t.Fatalf("Failed to set key: %v", err)
		}
	}

	// Flush initial data
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Failed to flush: %v", err)
	}

	// Write data that will be in the shadow buffer
	// These writes happen before snapshot but should be preserved
	for i := 10; i < 20; i++ {
		key := "key_" + string(rune('0'+i%10)) + "_shadow"
		value := []byte("value_shadow_" + string(rune('0'+i%10)))
		if err := eng.KVSet(key, value); err != nil {
			t.Fatalf("Failed to set shadow key: %v", err)
		}
	}

	// Snapshot should preserve the shadow writes
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// Close
	if err := eng.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	// Restart
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Failed to restart: %v", err)
	}
	defer eng2.Close()

	// Verify all keys are present
	for i := 0; i < 10; i++ {
		key := "key_" + string(rune('0'+i))
		if _, found := eng2.KVGet(key); !found {
			t.Errorf("Key %s not found after restart", key)
		}
	}

	// Verify shadow keys are present
	for i := 10; i < 20; i++ {
		key := "key_" + string(rune('0'+i%10)) + "_shadow"
		if _, found := eng2.KVGet(key); !found {
			t.Errorf("Shadow key %s not found after restart - data loss detected!", key)
		}
	}

	t.Log("No data loss test passed")
}
