package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/persistence"
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

// TestRecovery_ResyncAfterCorruption verifies that replayAOF can resync
// (skip corrupted bytes) and continue replaying valid frames that appear
// after the corrupted section. This is the regression test for H28.
func TestRecovery_ResyncAfterCorruption(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	aofPath := filepath.Join(dir, opts.AofFilename)

	// Step 1: create engine, add vector "a", flush, close
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if err := eng.VCreate("idx", distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate failed: %v", err)
	}
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"k": "a"}); err != nil {
		t.Fatalf("VAdd a failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Step 2: generate a valid framed VADD command for vector "b"
	genDir := t.TempDir()
	genPath := filepath.Join(genDir, "frame_gen.aof")
	genW, err := persistence.NewAOFWriter(genPath, 0)
	if err != nil {
		t.Fatalf("NewAOFWriter failed: %v", err)
	}
	cmdB := persistence.FormatCommand("VADD",
		[]byte("idx"), []byte("b"),
		[]byte(float32SliceToString([]float32{0, 1, 0, 0})), []byte(`{"k":"b"}`))
	if err := genW.Write(cmdB); err != nil {
		t.Fatalf("genW.Write failed: %v", err)
	}
	if err := genW.Flush(); err != nil {
		t.Fatalf("genW.Flush failed: %v", err)
	}
	genW.Close()
	framedBytes, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("ReadFile genPath failed: %v", err)
	}

	// Step 3: append garbage (no 0xA5 bytes) + framed VADD for "b" to the AOF
	aofFile, err := os.OpenFile(aofPath, os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		t.Fatalf("OpenFile AOF failed: %v", err)
	}
	garbage := make([]byte, 200)
	for i := range garbage {
		garbage[i] = byte(0x01) // all non-0xA5 bytes, not a valid magic
	}
	if _, err := aofFile.Write(garbage); err != nil {
		t.Fatalf("Write garbage failed: %v", err)
	}
	if _, err := aofFile.Write(framedBytes); err != nil {
		t.Fatalf("Write framed VADD b failed: %v", err)
	}
	if err := aofFile.Close(); err != nil {
		t.Fatalf("Close AOF failed: %v", err)
	}

	// Step 4: reopen — resync should skip garbage and recover vector "b"
	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	dataA, err := eng2.VGet("idx", "a")
	if err != nil {
		t.Fatalf("VGet a after resync failed: %v", err)
	}
	if dataA.Metadata["k"] != "a" {
		t.Fatalf("metadata for a not recovered: %v", dataA.Metadata)
	}

	dataB, err := eng2.VGet("idx", "b")
	if err != nil {
		t.Fatalf("VGet b after resync failed (H28 resync may have failed): %v", err)
	}
	if dataB.Metadata["k"] != "b" {
		t.Fatalf("metadata for b not recovered: %v", dataB.Metadata)
	}
}

// TestRewriteAOF_NoDataLossWithConcurrentWrites verifies that concurrent writes
// during RewriteAOF are not lost. Regression test for C22.
func TestRewriteAOF_NoDataLossWithConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	opts.AofRewritePercentage = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")

	// Phase 1: write initial vectors to build up AOF content.
	for i := 0; i < 5; i++ {
		id := "init_" + string(rune('a'+i))
		if err := eng.VAdd("idx", id, []float32{1, 0, 0, 0}, nil); err != nil {
			t.Fatalf("VAdd %s failed: %v", id, err)
		}
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}

	// Phase 2: write BEFORE rewrite so they're in writeCh when snapshot starts.
	for i := 0; i < 5; i++ {
		id := "before_" + string(rune('a'+i))
		if err := eng.VAdd("idx", id, []float32{0, 1, 0, 0}, map[string]any{"phase": "before"}); err != nil {
			t.Fatalf("VAdd %s failed: %v", id, err)
		}
	}

	done := make(chan struct{})
	var rewriteErr error

	// Phase 3: run RewriteAOF in background.
	go func() {
		rewriteErr = eng.RewriteAOF()
		close(done)
	}()

	// Phase 4: writes DURING rewrite (land in shadow buffer).
	for i := 0; i < 5; i++ {
		id := "during_" + string(rune('a'+i))
		if err := eng.VAdd("idx", id, []float32{0, 0, 1, 0}, map[string]any{"phase": "during"}); err != nil {
			t.Fatalf("VAdd %s failed: %v", id, err)
		}
	}

	// Wait for rewrite to finish.
	<-done
	if rewriteErr != nil {
		t.Fatalf("RewriteAOF failed: %v", rewriteErr)
	}

	// Phase 5: writes AFTER rewrite (land in new AOF via regular buffer).
	for i := 0; i < 5; i++ {
		id := "after_" + string(rune('a'+i))
		if err := eng.VAdd("idx", id, []float32{0, 0, 0, 1}, map[string]any{"phase": "after"}); err != nil {
			t.Fatalf("VAdd %s failed: %v", id, err)
		}
	}

	// Final sync: the rewrite's shadow replay already called Sync(),
	// but the concurrent/after writes may still be in writeCh. We flush
	// a few times to ensure the lazy writer processes them all before Close.
	for i := 0; i < 3; i++ {
		eng.AOF.Flush()
	}
	if err := eng.AOF.Sync(); err != nil {
		t.Fatalf("AOF sync failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen and verify all vectors survived.
	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	allIDs := []string{}
	for i := 0; i < 5; i++ {
		allIDs = append(allIDs, "init_"+string(rune('a'+i)))
		allIDs = append(allIDs, "before_"+string(rune('a'+i)))
		allIDs = append(allIDs, "during_"+string(rune('a'+i)))
		allIDs = append(allIDs, "after_"+string(rune('a'+i)))
	}

	for _, id := range allIDs {
		if _, err := eng2.VGet("idx", id); err != nil {
			t.Fatalf("vector %s lost after rewrite: %v", id, err)
		}
	}
}

// TestBurstWritesSurviveClose verifies that a burst of fire-and-forget writes
// followed by immediate Close does not lose data. Regression test for E4.
func TestBurstWritesSurviveClose(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	createTestIndex(t, eng, "idx")

	const burstSize = 100

	// Burst writes without any intermediate flush — all go to writeCh.
	for i := 0; i < burstSize; i++ {
		id := "vec_" + strconv.Itoa(i)
		if err := eng.VAdd("idx", id, []float32{1, 0, 0, 0}, map[string]any{"i": i}); err != nil {
			t.Fatalf("VAdd %d failed: %v", i, err)
		}
	}

	// Close immediately — the lazy writer must drain writeCh before closing.
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen and verify ALL vectors survived.
	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	for i := 0; i < burstSize; i++ {
		id := "vec_" + strconv.Itoa(i)
		data, err := eng2.VGet("idx", id)
		if err != nil {
			t.Fatalf("vector %s lost after burst+close: %v", id, err)
		}
		if v, ok := data.Metadata["i"]; !ok || int(v.(float64)) != i {
			t.Fatalf("metadata mismatch for %s: got %v", id, data.Metadata)
		}
	}
}
