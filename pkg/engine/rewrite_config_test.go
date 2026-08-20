package engine

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// TestRewriteAOF_PreservesMemoryAndMaintenanceConfig verifies that RewriteAOF
// persists MEMORY_CONFIG (decay) and VCONFIG (vacuum/refine) so both survive a
// rewrite followed by a restart. Regression for P0-2: the rewrite previously
// emitted VCREATE without MEMORY_CONFIG and never emitted VCONFIG, silently
// losing the configuration on restart.
func TestRewriteAOF_PreservesMemoryAndMaintenanceConfig(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	indexName := "cfg_idx"
	maintCfg := hnsw.AutoMaintenanceConfig{
		VacuumInterval:  hnsw.Duration(2 * time.Minute),
		DeleteThreshold: 0.25,
		GraphRetention:  hnsw.Duration(720 * time.Hour),
	}
	memCfg := hnsw.MemoryConfig{
		Enabled:       true,
		DecayModel:    hnsw.DecayExponential,
		DecayHalfLife: hnsw.Duration(168 * time.Hour),
	}

	if err := eng.VCreate(indexName, distance.Cosine, 4, 10, distance.Float32, "", &maintCfg, nil, &memCfg); err != nil {
		t.Fatalf("VCreate failed: %v", err)
	}
	if err := eng.VAdd(indexName, "a", []float32{1, 0, 0, 0}, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("AOF flush failed: %v", err)
	}

	// Force a rewrite: this is the operation that used to drop the configs.
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF failed: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	eng2 := reopenEngine(t, dir)
	defer eng2.Close()

	idx, ok := eng2.DB.GetVectorIndex(indexName)
	if !ok {
		t.Fatalf("index %q missing after restart", indexName)
	}
	h, ok := idx.(*hnsw.Index)
	if !ok {
		t.Fatalf("index %q is not an HNSW index", indexName)
	}

	// Memory config (decay) must survive the rewrite + restart.
	gotMem := h.GetMemoryConfig()
	if !gotMem.Enabled {
		t.Fatalf("memory config lost after rewrite: got %+v", gotMem)
	}
	if gotMem.DecayModel != memCfg.DecayModel || gotMem.DecayHalfLife != memCfg.DecayHalfLife {
		t.Fatalf("memory config mismatch after rewrite: got %+v, want %+v", gotMem, memCfg)
	}

	// Maintenance config (vacuum/refine) must survive the rewrite + restart.
	gotMaint := h.GetMaintenanceConfig()
	if gotMaint.VacuumInterval != maintCfg.VacuumInterval {
		t.Fatalf("maintenance config lost after rewrite: got %+v, want %+v", gotMaint, maintCfg)
	}
	if gotMaint.DeleteThreshold != maintCfg.DeleteThreshold {
		t.Fatalf("maintenance config threshold mismatch: got %+v, want %+v", gotMaint, maintCfg)
	}
}
