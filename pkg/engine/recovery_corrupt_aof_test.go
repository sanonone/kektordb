package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/persistence"
)

// TestRecovery_CorruptGLINKWeightSkipped verifies that a GLINK frame with an
// unparseable weight is skipped during AOF replay — the engine must start
// successfully and must NOT add the edge.
func TestRecovery_CorruptGLINKWeightSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	// Step 1: open engine and write a corrupt GLINK frame directly into the AOF.
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	// GLINK args: indexName, source, target, relType, invRelType, weight, props
	cmd := persistence.FormatCommand("GLINK",
		[]byte(""),            // indexName (unused during recovery)
		[]byte("node_a"),      // source
		[]byte("node_b"),      // target
		[]byte("links_to"),    // relType
		[]byte(""),            // invRelType
		[]byte("not_a_float"), // corrupt weight
		[]byte("{}"),          // props
	)
	if err := eng.AOF.Write(cmd); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	// Step 2: reopen — AOF replay must skip the corrupt frame and succeed.
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("engine failed to open after corrupt GLINK in AOF: %v", err)
	}
	defer eng2.Close()

	// The edge must NOT have been added.
	incoming := eng2.DB.GetAllRelations("node_b", "in")
	for _, sources := range incoming {
		for _, src := range sources {
			if src == "node_a" {
				t.Error("edge node_a -> node_b should not exist after corrupt GLINK was skipped")
			}
		}
	}
}

// TestRecovery_CorruptGUNLINKTimestampSkipped verifies that a GUNLINK frame with
// an unparseable timestamp is skipped during AOF replay — the engine must start
// successfully.
func TestRecovery_CorruptGUNLINKTimestampSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	// GUNLINK args: indexName, source, target, relType, invRelType, hardDelete, timestamp
	cmd := persistence.FormatCommand("GUNLINK",
		[]byte(""),           // indexName
		[]byte("node_a"),     // source
		[]byte("node_b"),     // target
		[]byte("links_to"),   // relType
		[]byte(""),           // invRelType
		[]byte("false"),      // hardDelete
		[]byte("not_an_int"), // corrupt timestamp
	)
	if err := eng.AOF.Write(cmd); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("engine failed to open after corrupt GUNLINK in AOF: %v", err)
	}
	eng2.Close()
}

// TestRecovery_VDROPStaleArenaDirCleaned verifies that when a VDROP record is
// replayed and a stale arena directory exists on disk, the directory is removed
// and the engine starts successfully.
func TestRecovery_VDROPStaleArenaDirCleaned(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0

	const indexName = "stale_arena_idx"

	// Step 1: create the index then delete it so the VDROP lands in the AOF.
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.VCreate(indexName, distance.Cosine, 16, 100, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VDeleteIndex(indexName); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	// Step 2: re-create the arena directory to simulate a stale leftover.
	staleDir := filepath.Join(tmpDir, "arenas", indexName)
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("failed to create stale arena dir: %v", err)
	}

	// Step 3: reopen — VDROP replay must remove the stale directory and succeed.
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("engine failed to open with stale arena dir present: %v", err)
	}
	eng2.Close()

	// The stale directory must have been cleaned up.
	if _, statErr := os.Stat(staleDir); !os.IsNotExist(statErr) {
		t.Errorf("stale arena directory %q should have been removed during VDROP recovery", staleDir)
	}
}
