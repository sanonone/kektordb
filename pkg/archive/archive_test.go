package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the archive format.
//
// The format makes a trade: no vectors, so the file stays readable and survives a
// change of embedder. That only works if everything else survives — metadata,
// timestamps, and the graph — and if a damaged archive is refused before it
// touches the target store.

func newTestArchive(t *testing.T) (string, *Writer) {
	t.Helper()
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return dir, w
}

// TestRoundTripPreservesMemory is the basic contract.
func TestRoundTripPreservesMemory(t *testing.T) {
	dir, w := newTestArchive(t)

	if err := w.AddMemory(MemoryRecord{
		ID: "mem_1",
		Metadata: map[string]any{
			"content":      "Preferisci risposte dirette",
			"memory_layer": "semantic",
			"_created_at":  float64(1793900000),
			"_pinned":      true,
		},
	}); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var got []MemoryRecord
	if err := MemoryRecords(dir, func(r MemoryRecord) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatalf("MemoryRecords: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("read %d records, want 1", len(got))
	}
	if got[0].ID != "mem_1" {
		t.Errorf("id = %q", got[0].ID)
	}
	if got[0].Metadata["content"] != "Preferisci risposte dirette" {
		t.Errorf("content = %v", got[0].Metadata["content"])
	}
	if got[0].Metadata["_pinned"] != true {
		t.Errorf("_pinned did not survive: %v", got[0].Metadata["_pinned"])
	}
	if got[0].Metadata["_created_at"] != float64(1793900000) {
		t.Errorf("_created_at did not survive: %v", got[0].Metadata["_created_at"])
	}
}

// TestRoundTripPreservesGraph: the graph is half the value.
func TestRoundTripPreservesGraph(t *testing.T) {
	dir, w := newTestArchive(t)

	edges := []EdgeRecord{
		{Source: "mem_b", Target: "mem_a", Relation: "consolidated_into", Inverse: "derived_from", Weight: 1.0},
		{Source: "mem_a", Target: "entity::x", Relation: "mentions"},
	}
	for _, e := range SortedEdges(edges) {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	var got []EdgeRecord
	if err := EdgeRecords(dir, func(e EdgeRecord) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatalf("EdgeRecords: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d edges, want 2", len(got))
	}
	// SortedEdges orders by source then target: mem_a before mem_b.
	if got[0].Source != "mem_a" {
		t.Errorf("edges are not in a stable order: %+v", got)
	}
}

// TestExportOmitsVectors is the decision the format is built on.
func TestExportOmitsVectors(t *testing.T) {
	dir, w := newTestArchive(t)
	if err := w.AddMemory(MemoryRecord{
		ID:       "mem_1",
		Metadata: map[string]any{"content": "x", "vector": []float32{1, 2, 3}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	blob, err := os.ReadFile(filepath.Join(dir, memoriesFile))
	if err != nil {
		t.Fatal(err)
	}
	// A vector under metadata would be exported verbatim; the format's promise is
	// that vectors are not carried at all, so the archive stays small and model
	// independent.
	if strings.Contains(string(blob), "1,2,3") {
		t.Errorf("the archive carries vector data:\n%s", blob)
	}
}

// TestManifestCarriesChecksums: without them a truncated archive is only caught
// halfway through an import.
func TestManifestCarriesChecksums(t *testing.T) {
	dir, w := newTestArchive(t)
	if err := w.AddMemory(MemoryRecord{ID: "a", Metadata: map[string]any{"content": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.AddEdge(EdgeRecord{Source: "a", Target: "b", Relation: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if !strings.HasPrefix(m.Checksum.Memories, "sha256:") {
		t.Errorf("memories checksum = %q", m.Checksum.Memories)
	}
	if !strings.HasPrefix(m.Checksum.Graph, "sha256:") {
		t.Errorf("graph checksum = %q", m.Checksum.Graph)
	}
	if m.Counts.Memories != 1 || m.Counts.Edges != 1 {
		t.Errorf("counts = %+v", m.Counts)
	}
}

// TestVerifyAcceptsAGoodArchive is the happy path.
func TestVerifyAcceptsAGoodArchive(t *testing.T) {
	dir, w := newTestArchive(t)
	if err := w.AddMemory(MemoryRecord{ID: "a", Metadata: map[string]any{"content": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyContent(dir); err != nil {
		t.Fatalf("a freshly written archive failed verification: %v", err)
	}
}

// TestVerifyRejectsTruncatedArchive is the point of the checksum.
func TestVerifyRejectsTruncatedArchive(t *testing.T) {
	dir, w := newTestArchive(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := w.AddMemory(MemoryRecord{ID: id, Metadata: map[string]any{"content": "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	// Lop off the last record, as a killed process would.
	path := filepath.Join(dir, memoriesFile)
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(blob), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-2], "")), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = VerifyContent(dir)
	if err == nil {
		t.Fatal("a truncated archive passed verification")
	}
	if !strings.Contains(err.Error(), "corrupt") && !strings.Contains(err.Error(), "holds") {
		t.Errorf("unhelpful error for a truncated archive: %v", err)
	}
}

// TestReadManifestRejectsForeignDirectory keeps the importer from mistaking any
// directory for an archive.
func TestReadManifestRejectsForeignDirectory(t *testing.T) {
	if _, err := ReadManifest(t.TempDir()); err == nil {
		t.Fatal("an empty directory was accepted as an archive")
	}
}

// TestReadManifestRejectsFutureVersion: a newer format must fail loudly, not be
// half-read.
func TestReadManifestRejectsFutureVersion(t *testing.T) {
	dir := t.TempDir()
	blob := `{"format":"kektordb-archive","format_version":99,"index":"mem"}`
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("a future format version was accepted")
	}
}

// TestInternalMetadataIsDropped keeps engine state out of the user's archive.
func TestInternalMetadataIsDropped(t *testing.T) {
	dir, w := newTestArchive(t)
	if err := w.AddMemory(MemoryRecord{
		ID: "a",
		Metadata: map[string]any{
			"content":          "user data",
			"_decay_factor":    0.5,
			"_last_decay_calc": float64(1),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	var got MemoryRecord
	if err := MemoryRecords(dir, func(r MemoryRecord) error { got = r; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Metadata["_decay_factor"]; ok {
		t.Error("_decay_factor was exported: it is computed state, not the user's memory")
	}
	if got.Metadata["content"] != "user data" {
		t.Error("user content was lost")
	}
}

// TestSortedEdgesIsStable is what makes the archive diff-friendly: re-exporting an
// unchanged store must produce the same file.
func TestSortedEdgesIsStable(t *testing.T) {
	a := []EdgeRecord{
		{Source: "b", Target: "a", Relation: "r"},
		{Source: "a", Target: "z", Relation: "r"},
		{Source: "a", Target: "a", Relation: "r"},
	}
	b := []EdgeRecord{a[2], a[0], a[1]}

	sa, sb := SortedEdges(a), SortedEdges(b)
	for i := range sa {
		// EdgeRecord holds a map, so compare the fields that determine order.
		if sa[i].Source != sb[i].Source || sa[i].Target != sb[i].Target {
			t.Fatalf("sorting is not stable at %d: %s->%s vs %s->%s",
				i, sa[i].Source, sa[i].Target, sb[i].Source, sb[i].Target)
		}
	}
}
