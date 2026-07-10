package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

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
