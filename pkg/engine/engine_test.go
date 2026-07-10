package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
)

func createTestIndex(t *testing.T, eng *Engine, name string) {
	t.Helper()
	if err := eng.VCreate(name, distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate failed: %v", err)
	}
}

func reopenEngine(t *testing.T, dir string) *Engine {
	t.Helper()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	return eng
}

// TestOpenFreshDataDir verifies that opening an empty data directory does not
// panic and leaves the AOF base size at zero. This is a regression test for the
// nil-pointer panic that could occur when AOF.File().Stat() returned an error.
func TestOpenFreshDataDir(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed on fresh data dir: %v", err)
	}
	defer eng.Close()

	if got := eng.aofBaseSize.Load(); got != 0 {
		t.Fatalf("expected aofBaseSize == 0 for fresh data dir, got %d", got)
	}
}

// TestOpenExistingAOFBaseSize verifies that the AOF base size is initialized
// from the existing AOF file size when reopening an engine.
func TestOpenExistingAOFBaseSize(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	// Create an engine, write a vector, and close it to leave an AOF behind.
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := eng.VCreate("idx", distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate failed: %v", err)
	}
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen and compare aofBaseSize with the actual AOF file size.
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed on existing AOF: %v", err)
	}
	defer eng2.Close()

	fi, err := os.Stat(filepath.Join(dir, opts.AofFilename))
	if err != nil {
		t.Fatalf("stat AOF failed: %v", err)
	}
	if got := eng2.aofBaseSize.Load(); got != fi.Size() {
		t.Fatalf("expected aofBaseSize == %d, got %d", fi.Size(), got)
	}
}

// TestRewriteAOFUpdatesBaseSize verifies that a successful AOF rewrite updates
// the base size without panicking. This exercises the Stat() guard added to
// recovery.go.
func TestRewriteAOFUpdatesBaseSize(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	opts.AofRewritePercentage = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	if err := eng.VCreate("idx", distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate failed: %v", err)
	}
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}

	baseBefore := eng.aofBaseSize.Load()
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF failed: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, opts.AofFilename))
	if err != nil {
		t.Fatalf("stat AOF after rewrite failed: %v", err)
	}
	if got := eng.aofBaseSize.Load(); got != fi.Size() {
		t.Fatalf("expected aofBaseSize == %d after rewrite, got %d", fi.Size(), got)
	}
	if baseBefore < 0 {
		t.Fatalf("unexpected baseBefore value: %d", baseBefore)
	}
}

// TestVAdd_AOFFirstSurvivesRestart verifies that a vector added with VAdd is
// recovered from the AOF after a restart. Regression test for E1
// (Memory-before-AOF).
func TestVAdd_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	data, err := eng2.VGet("idx", "a")
	if err != nil {
		t.Fatalf("VGet after restart failed: %v", err)
	}
	if data.Metadata["k"] != "v" {
		t.Fatalf("metadata not recovered: got %v", data.Metadata)
	}
}

// TestVDelete_AOFFirstSurvivesRestart verifies that VDelete is replayed from
// the AOF after a restart.
func TestVDelete_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if err := eng2.VDelete("idx", "a"); err != nil {
		t.Fatalf("VDelete failed: %v", err)
	}
	if err := eng2.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng3 := reopenEngine(t, dir)
	defer eng3.Close()

	if _, err := eng3.VGet("idx", "a"); err == nil {
		t.Fatalf("expected deleted vector to be absent after restart")
	}
}

// TestVSetMetadata_AOFFirstSurvivesRestart verifies that metadata updates are
// replayed from the AOF after a restart.
func TestVSetMetadata_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"x": 1}); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if err := eng2.VSetMetadata("idx", "a", map[string]any{"y": 2}); err != nil {
		t.Fatalf("VSetMetadata failed: %v", err)
	}
	if err := eng2.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng3 := reopenEngine(t, dir)
	defer eng3.Close()

	data, err := eng3.VGet("idx", "a")
	if err != nil {
		t.Fatalf("VGet after restart failed: %v", err)
	}
	if data.Metadata["x"] != float64(1) || data.Metadata["y"] != float64(2) {
		t.Fatalf("metadata not recovered: got %v", data.Metadata)
	}
}

// TestVReinforce_AOFFirstSurvivesRestart verifies that VReinforce updates are
// replayed from the AOF after a restart.
func TestVReinforce_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"_pinned": false}); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if err := eng2.VReinforce("idx", []string{"a"}); err != nil {
		t.Fatalf("VReinforce failed: %v", err)
	}
	if err := eng2.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng3 := reopenEngine(t, dir)
	defer eng3.Close()

	data, err := eng3.VGet("idx", "a")
	if err != nil {
		t.Fatalf("VGet after restart failed: %v", err)
	}
	if data.Metadata["_access_count"] != float64(1) {
		t.Fatalf("expected _access_count == 1, got %v", data.Metadata["_access_count"])
	}
	if _, ok := data.Metadata["_last_accessed"]; !ok {
		t.Fatalf("expected _last_accessed to be set")
	}
}

// TestVAddBatch_AOFFirstSurvivesRestart verifies that batch inserts are
// replayed from the AOF after a restart.
func TestVAddBatch_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	items := []types.BatchObject{
		{Id: "a", Vector: []float32{1, 0, 0, 0}, Metadata: map[string]any{"k": "v1"}},
		{Id: "b", Vector: []float32{0, 1, 0, 0}, Metadata: map[string]any{"k": "v2"}},
	}
	if err := eng.VAddBatch("idx", items); err != nil {
		t.Fatalf("VAddBatch failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	for _, id := range []string{"a", "b"} {
		data, err := eng2.VGet("idx", id)
		if err != nil {
			t.Fatalf("VGet %s after restart failed: %v", id, err)
		}
		if data.Metadata["k"] == nil {
			t.Fatalf("metadata for %s not recovered", id)
		}
	}
}

// TestVUpdateIndexConfig_AOFFirstSurvivesRestart verifies that index config
// updates are replayed from the AOF after a restart.
func TestVUpdateIndexConfig_AOFFirstSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	custom := hnsw.AutoMaintenanceConfig{RefineEnabled: true, DeleteThreshold: 0.42}
	if err := eng2.VUpdateIndexConfig("idx", custom); err != nil {
		t.Fatalf("VUpdateIndexConfig failed: %v", err)
	}
	if err := eng2.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng3 := reopenEngine(t, dir)
	defer eng3.Close()

	idx, ok := eng3.DB.GetVectorIndex("idx")
	if !ok {
		t.Fatalf("index not found after restart")
	}
	hnswIdx, err := getHNSWIndex(idx)
	if err != nil {
		t.Fatalf("index is not hnsw: %v", err)
	}
	cfg := hnswIdx.GetMaintenanceConfig()
	if !cfg.RefineEnabled || cfg.DeleteThreshold != 0.42 {
		t.Fatalf("config not recovered: got %+v", cfg)
	}
}
