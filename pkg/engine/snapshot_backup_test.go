package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotKeepsPreviousBackup covers P24: replacing the only copy of the
// database with a freshly written file leaves nothing to fall back on if that
// file turns out to be wrong. The previous snapshot is kept as <name>.kdb.bak.
func TestSnapshotKeepsPreviousBackup(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.KVSet("first", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	first, err := os.ReadFile(eng.snapPath)
	if err != nil {
		t.Fatalf("read first snapshot: %v", err)
	}

	if err := eng.KVSet("second", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	backup, err := os.ReadFile(backupPathFor(eng.snapPath))
	if err != nil {
		t.Fatalf("no backup kept after the second snapshot: %v", err)
	}
	if len(backup) != len(first) {
		t.Errorf("backup is %d bytes, the first snapshot was %d", len(backup), len(first))
	}

	current, err := os.ReadFile(eng.snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) == len(first) {
		t.Log("note: both snapshots have the same size, content comparison needed")
	}

	eng.Close()
}

// TestSnapshotNoBackupOnFirstWrite keeps the common case clean: the very first
// snapshot has nothing to back up and must not fail.
func TestSnapshotNoBackupOnFirstWrite(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.KVSet("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("first snapshot failed: %v", err)
	}

	if _, err := os.Stat(backupPathFor(eng.snapPath)); err == nil {
		t.Error("a backup exists after the first snapshot, but there was nothing to back up")
	}
}

// TestSnapshotBackupIsUsable proves the backup can actually restore the
// database, not just that a file exists.
func TestSnapshotBackupIsUsable(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0

	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.KVSet("only_in_first", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// A second snapshot, after deleting the key, produces a state the first
	// snapshot does not have.
	if err := eng.KVDelete("only_in_first"); err != nil {
		t.Fatal(err)
	}
	if err := eng.KVSet("only_in_second", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	// Restore the backup over the live snapshot, as an operator would.
	backup := backupPathFor(filepath.Join(dir, "kektordb.kdb"))
	live := filepath.Join(dir, "kektordb.kdb")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if err := os.WriteFile(live, data, 0o644); err != nil {
		t.Fatalf("restore backup: %v", err)
	}
	// The AOF would otherwise replay the deletion on top of the restored state.
	if err := os.Remove(filepath.Join(dir, "kektordb.aof")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	restored, err := Open(opts)
	if err != nil {
		t.Fatalf("open restored snapshot: %v", err)
	}
	defer restored.Close()

	if _, ok := restored.KVGet("only_in_first"); !ok {
		t.Error("the restored backup does not contain the key that was in the first snapshot")
	}
}
