package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotFailureKeepsConcurrentWrites covers a data-loss path verified
// before the fix: writes that arrive while a snapshot is in flight go to the
// AOF shadow buffer, and the error path dropped them.
//
// The scenario: snapshot mode is active, a write lands in the shadow buffer,
// then the snapshot fails (a full disk, a permissions change, a rename across
// filesystems). The write must still reach the AOF, or the client is told it
// succeeded while it is gone.
func TestSnapshotFailureKeepsConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}

	// A payload large enough that encoding takes long enough for a concurrent
	// write to land inside the snapshot window. Without it the window is a few
	// microseconds and the test would pass for the wrong reason.
	blob := make([]byte, 4<<20)
	for i := range blob {
		blob[i] = byte(i)
	}
	for i := 0; i < 12; i++ {
		key := "bulk_" + string(rune('a'+i))
		if err := eng.KVSet(key, blob); err != nil {
			t.Fatal(err)
		}
	}

	// Force the snapshot to fail at the rename step: a directory occupies the
	// path the rename targets.
	blocker := filepath.Join(dir, "blocked.kdb")
	if err := os.MkdirAll(filepath.Join(blocker, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	eng.snapPath = blocker

	// Writes issued while the snapshot is encoding. Each one is acknowledged,
	// so each one must survive.
	const duringWrites = 50
	writeErr := make(chan error, 1)
	go func() {
		for i := 0; i < duringWrites; i++ {
			key := "during_" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			if err := eng.KVSet(key, []byte("must-not-vanish")); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	err = eng.SaveSnapshot()
	if werr := <-writeErr; werr != nil {
		t.Fatalf("KVSet during snapshot: %v", werr)
	}

	if err == nil {
		t.Fatal("SaveSnapshot succeeded although the rename target was a directory")
	}
	t.Logf("snapshot failed as intended: %v", err)

	// Close flushes the AOF, exactly as a graceful shutdown would.
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	eng.snapPath = filepath.Join(dir, "kektordb.kdb")

	reopened, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	missing := 0
	var firstMissing string
	for i := 0; i < duringWrites; i++ {
		key := "during_" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		if _, ok := reopened.KVGet(key); !ok {
			missing++
			if firstMissing == "" {
				firstMissing = key
			}
		}
	}
	if missing > 0 {
		t.Errorf("%d of %d writes acknowledged during a failed snapshot were lost (first: %q)",
			missing, duringWrites, firstMissing)
	}
}
