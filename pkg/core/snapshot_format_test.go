package core

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
)

// writeLegacySnapshot produces a .kdb exactly as KektorDB wrote it before the
// framed format existed: a bare gob stream, no header.
func writeLegacySnapshot(t *testing.T, db *DB) []byte {
	t.Helper()
	// Reproduce the old writer: gob-encode the Snapshot value directly.
	var buf bytes.Buffer
	snap := buildSnapshotForTest(t, db)
	if err := gob.NewEncoder(&buf).Encode(snap); err != nil {
		t.Fatalf("legacy encode: %v", err)
	}
	return buf.Bytes()
}

// TestSnapshotLegacyFileStillLoads is the compatibility guarantee: a database
// created by a previous release must keep opening after the upgrade.
//
// Without this, adding a header would leave every existing user with an
// unreadable database and no in-place recovery, since the AOF is truncated
// after each snapshot.
func TestSnapshotLegacyFileStillLoads(t *testing.T) {
	dir := t.TempDir()
	db := NewDB()
	db.GetKVStore().Set("legacy_key", []byte("legacy_value"))

	legacy := writeLegacySnapshot(t, db)

	if bytes.HasPrefix(legacy, []byte(snapshotMagic)) {
		t.Fatal("the legacy fixture accidentally starts with the new magic")
	}
	if legacy[0] == 0xFF {
		t.Fatal("legacy gob must not start with 0xFF, or detection cannot work")
	}

	restored := LoadFromSnapshotBytes(t, legacy, dir)
	if got, ok := restored.GetKVStore().Get("legacy_key"); !ok || string(got) != "legacy_value" {
		t.Fatalf("legacy snapshot did not round-trip: got %q, ok=%v", got, ok)
	}
}

// TestSnapshotFramedRoundTrip covers the new path.
func TestSnapshotFramedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db := NewDB()
	db.GetKVStore().Set("key", []byte("value"))

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte(snapshotMagic)) {
		t.Fatal("new snapshots must carry the magic header")
	}

	restored := LoadFromSnapshotBytes(t, buf.Bytes(), dir)
	if got, ok := restored.GetKVStore().Get("key"); !ok || string(got) != "value" {
		t.Fatalf("framed snapshot did not round-trip: %q ok=%v", got, ok)
	}
}

// TestSnapshotDetectsCorruption is what the CRC is for: a flipped bit that
// still decodes must be refused rather than loaded as a slightly wrong database.
func TestSnapshotDetectsCorruption(t *testing.T) {
	db := NewDB()
	db.GetKVStore().Set("key", []byte("a value long enough that a flip is detectable"))

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	payload := buf.Bytes()

	// Flip one byte in the middle of the payload.
	corrupt := make([]byte, len(payload))
	copy(corrupt, payload)
	mid := snapshotHeaderSize + (len(payload)-snapshotHeaderSize)/2
	corrupt[mid] ^= 0xFF

	if _, err := LoadFromSnapshotErr(corrupt); err == nil {
		t.Fatal("a corrupted snapshot loaded without error: the checksum is not being verified")
	}
}

// TestSnapshotDetectsTruncation: a file cut short must be an error, not a
// partially loaded database.
func TestSnapshotDetectsTruncation(t *testing.T) {
	db := NewDB()
	db.GetKVStore().Set("key", []byte("value"))

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	truncated := buf.Bytes()[:len(buf.Bytes())-10]
	if _, err := LoadFromSnapshotErr(truncated); err == nil {
		t.Fatal("a truncated snapshot loaded without error")
	}
}

// TestSnapshotRejectsFutureVersion: a file from a newer build must fail loudly
// instead of being decoded with fields silently missing.
func TestSnapshotRejectsFutureVersion(t *testing.T) {
	db := NewDB()
	db.GetKVStore().Set("key", []byte("value"))

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	data := buf.Bytes()
	// Bump the version field to something this build does not know.
	data[len(snapshotMagic)+3] = 99

	if _, err := LoadFromSnapshotErr(data); err == nil {
		t.Fatal("a snapshot with an unknown version was accepted")
	}
}

// TestSnapshotNonSeekableReader keeps the older API contract: bytes.Buffer has
// no Seek, and three tests in this package pass one.
func TestSnapshotNonSeekableReader(t *testing.T) {
	dir := t.TempDir()
	db := NewDB()
	db.GetKVStore().Set("k", []byte("v"))

	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := NewDB()
	if err := restored.LoadFromSnapshot(&buf, dir); err != nil {
		t.Fatalf("LoadFromSnapshot with a non-seekable reader: %v", err)
	}
	if got, ok := restored.GetKVStore().Get("k"); !ok || string(got) != "v" {
		t.Fatalf("non-seekable round-trip failed: %q ok=%v", got, ok)
	}
}

// --- helpers ---

// buildSnapshotForTest produces the Snapshot value the old writer encoded.
func buildSnapshotForTest(t *testing.T, db *DB) *Snapshot {
	t.Helper()
	var buf bytes.Buffer
	if err := db.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Decode the new framing to obtain the plain value, then re-encode as legacy.
	// This keeps the fixture honest: it is the same Snapshot struct, minus the header.
	snap, err := snapshotReadDecode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode for fixture: %v", err)
	}
	return snap
}

func LoadFromSnapshotBytes(t *testing.T, data []byte, dir string) *DB {
	t.Helper()
	db := NewDB()
	if err := db.LoadFromSnapshot(bytes.NewReader(data), dir); err != nil {
		t.Fatalf("LoadFromSnapshot: %v", err)
	}
	return db
}

func LoadFromSnapshotErr(data []byte) (*DB, error) {
	db := NewDB()
	err := db.LoadFromSnapshot(bytes.NewReader(data), filepath.Join(os.TempDir(), "kdbtest"))
	return db, err
}
