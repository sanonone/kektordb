package archive

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Round-trip coverage for every kind of record a KektorDB store can hold.
//
// The archive format drops vectors and keeps everything else. That promise is
// only worth making if "everything else" survives verbatim across the kinds of
// value the code actually writes: strings, numbers, booleans, nested maps,
// arrays of strings, nulls, unicode, and the timestamps that drive decay.

// recordKind is one shape of stored memory, taken from what the code produces.
type recordKind struct {
	name     string
	metadata map[string]any
}

// everyRecordKind mirrors the node types the engine writes.
func everyRecordKind() []recordKind {
	return []recordKind{
		{
			name: "plain memory",
			metadata: map[string]any{
				"content":      "Preferisci risposte dirette senza preamboli",
				"type":         "memory",
				"memory_layer": "semantic",
				"_created_at":  float64(1793900000),
			},
		},
		{
			name: "memory with tags array",
			metadata: map[string]any{
				"content": "Go is great for systems programming",
				"type":    "memory",
				"tags":    []any{"golang", "systems", "performance"},
			},
		},
		{
			name: "memory with links",
			metadata: map[string]any{
				"content": "The project uses Postgres for billing",
				"type":    "memory",
				"links":   []any{"entity::postgres", "entity::billing"},
			},
		},
		{
			name: "entity node, zero vector",
			metadata: map[string]any{
				"is_entity":   true,
				"type":        "entity",
				"name":        "PostgreSQL",
				"description": "the relational database chosen for billing",
			},
		},
		{
			name: "session node",
			metadata: map[string]any{
				"type":       "session",
				"session_id": "sess_abc123",
				"agent_id":   "claude-code",
				"user_id":    "federico",
				"status":     "active",
			},
		},
		{
			name: "core fact with provenance",
			metadata: map[string]any{
				"content":      "The user prefers Go over Python for backend work",
				"type":         "core_fact",
				"confidence":   0.92,
				"_created_at":  float64(1793900000),
				"extracted_at": float64(1793910000),
			},
		},
		{
			name: "retired memory",
			metadata: map[string]any{
				"content":            "The project uses MySQL",
				"type":               "memory",
				"_archived":          true,
				"_consolidated_into": "mem_successor",
				"_is_historical":     true,
			},
		},
		{
			name: "evolved memory with superseded text",
			metadata: map[string]any{
				"content":            "the original claim",
				"type":               "evolved_memory",
				"_consolidated_into": "mem_master",
				"_superseded_by":     "the corrected claim, as free text",
			},
		},
		{
			name: "reflection with action flag",
			metadata: map[string]any{
				"content":              "Conflict detected: two claims about the deployment target",
				"type":                 "reflection",
				"status":               "unresolved",
				"action_required":      false,
				"suggested_resolution": "prefer the newer memory",
				"confidence":           0.7,
			},
		},
		{
			name: "user profile insight",
			metadata: map[string]any{
				"type":            "user_profile_insight",
				"user_id":         "federico",
				"expertise_areas": "backend engineering, Go, PostgreSQL",
				"style":           "direct, no preamble",
				"confidence":      0.92,
			},
		},
		{
			name: "pinned procedural rule",
			metadata: map[string]any{
				"content":        "Always run gofmt before committing",
				"type":           "memory",
				"memory_layer":   "procedural",
				"_pinned":        true,
				"_access_count":  float64(12),
				"_last_accessed": float64(1794000000),
			},
		},
		{
			name: "unicode and quotes",
			metadata: map[string]any{
				"content": `L'utente dice: "preferisco le risposte brevi" — è una regola`,
				"type":    "memory",
				"note":    "caratteri accentati: à è ì ò ù, emoji: ✓",
			},
		},
		{
			name: "numeric edge cases",
			metadata: map[string]any{
				"content":   "numbers",
				"zero":      float64(0),
				"negative":  float64(-42.5),
				"very_big":  float64(1e15),
				"tiny":      float64(1e-15),
				"boolean_f": false,
				"boolean_t": true,
			},
		},
		{
			name: "nested map metadata",
			metadata: map[string]any{
				"content": "nested",
				"config": map[string]any{
					"model":      "all-MiniLM-L6-v2",
					"dimensions": float64(384),
					"nested":     map[string]any{"level": float64(2)},
				},
			},
		},
		{
			name: "null and empty values",
			metadata: map[string]any{
				"content": "edge",
				"empty":   "",
				"nothing": nil,
			},
		},
		{
			name: "large content",
			metadata: map[string]any{
				"content": longString(8000),
				"type":    "chunk",
			},
		},
	}
}

func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return string(b)
}

// TestEveryRecordKindSurvivesRoundTrip is the main coverage test.
func TestEveryRecordKindSurvivesRoundTrip(t *testing.T) {
	kinds := everyRecordKind()

	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	for i, k := range kinds {
		if err := w.AddMemory(MemoryRecord{ID: fmt.Sprintf("rec_%d", i), Metadata: k.metadata}); err != nil {
			t.Fatalf("AddMemory(%s): %v", k.name, err)
		}
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back into a map by ID.
	got := map[string]MemoryRecord{}
	if err := MemoryRecords(dir, func(r MemoryRecord) error {
		got[r.ID] = r
		return nil
	}); err != nil {
		t.Fatalf("MemoryRecords: %v", err)
	}

	if len(got) != len(kinds) {
		t.Fatalf("round trip lost records: wrote %d, read %d", len(kinds), len(got))
	}

	for i, k := range kinds {
		id := fmt.Sprintf("rec_%d", i)
		t.Run(k.name, func(t *testing.T) {
			rec, ok := got[id]
			if !ok {
				t.Fatalf("%s: record missing after round trip", id)
			}
			compareMetadata(t, k.metadata, rec.Metadata)
		})
	}
}

// compareMetadata checks every key survived with an equivalent value.
//
// JSON round-trips numbers through float64 and turns typed slices into []any, so
// the comparison is by JSON representation rather than by Go type: what matters
// is that the value a reader sees is the same one that was stored.
func compareMetadata(t *testing.T, want, got map[string]any) {
	t.Helper()

	// `vector` and `embedding` are dropped on purpose by the format.
	skip := map[string]bool{"vector": true, "embedding": true}

	for k, wv := range want {
		if skip[k] {
			continue
		}
		gv, ok := got[k]
		if !ok {
			t.Errorf("key %q was lost", k)
			continue
		}
		if !jsonEqual(wv, gv) {
			t.Errorf("key %q changed:\n  want %#v\n  got  %#v", k, wv, gv)
		}
	}
}

// jsonEqual compares two values through their JSON encoding, which is the
// representation the archive actually stores.
func jsonEqual(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ja) == string(jb)
}

// TestEdgesWithPropertiesSurvive: the supersede path writes props on the edge.
func TestEdgesWithPropertiesSurvive(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}

	edge := EdgeRecord{
		Source:    "mem_old",
		Target:    "mem_new",
		Relation:  "superseded_by",
		Inverse:   "evolves_from",
		Weight:    1.0,
		Props:     map[string]any{"reason": "contradiction resolved", "confidence": 0.88},
		CreatedAt: 1793900000000000000,
	}
	if err := w.AddEdge(edge); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	var got EdgeRecord
	if err := EdgeRecords(dir, func(e EdgeRecord) error { got = e; return nil }); err != nil {
		t.Fatalf("EdgeRecords: %v", err)
	}

	if got.Source != edge.Source || got.Target != edge.Target || got.Relation != edge.Relation {
		t.Errorf("edge identity changed: %+v", got)
	}
	if got.Inverse != edge.Inverse {
		t.Errorf("inverse = %q, want %q", got.Inverse, edge.Inverse)
	}
	if got.Weight != edge.Weight {
		t.Errorf("weight = %v, want %v", got.Weight, edge.Weight)
	}
	if got.CreatedAt != edge.CreatedAt {
		t.Errorf("created_at = %d, want %d", got.CreatedAt, edge.CreatedAt)
	}
	if !jsonEqual(edge.Props, got.Props) {
		t.Errorf("props changed:\n  want %#v\n  got  %#v", edge.Props, got.Props)
	}
}

// TestSelfEdgesSurvive: the dedup markers the detectors write are self-edges.
func TestSelfEdgesSurvive(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	// sentiment_analyzed, decay_analyzed, entities_analyzed are all self-edges.
	for _, rel := range []string{"sentiment_analyzed", "decay_analyzed", "entities_analyzed"} {
		if err := w.AddEdge(EdgeRecord{Source: "mem_1", Target: "mem_1", Relation: rel}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	count := 0
	if err := EdgeRecords(dir, func(e EdgeRecord) error {
		if e.Source != e.Target {
			t.Errorf("self-edge became %s -> %s", e.Source, e.Target)
		}
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("read %d self-edges, want 3", count)
	}
}

// TestUnicodeEdgesSurvive checks the JSON encoder is not mangling non-ASCII in
// IDs and relation names, which non-English stores will contain.
func TestUnicodeEdgesSurvive(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddEdge(EdgeRecord{
		Source:   "entity::Marco Rossi",
		Target:   "entity::Città di Milano",
		Relation: "lavora_per",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	var got EdgeRecord
	if err := EdgeRecords(dir, func(e EdgeRecord) error { got = e; return nil }); err != nil {
		t.Fatal(err)
	}
	if got.Source != "entity::Marco Rossi" || got.Target != "entity::Città di Milano" {
		t.Errorf("unicode IDs changed: %q -> %q", got.Source, got.Target)
	}
	if got.Relation != "lavora_per" {
		t.Errorf("relation = %q", got.Relation)
	}
}

// TestManyRecords keeps the format sane at a realistic size, and catches a
// pagination or buffering mistake that a five-record test would miss.
func TestManyRecords(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}

	const n = 5000
	for i := 0; i < n; i++ {
		if err := w.AddMemory(MemoryRecord{
			ID:       fmt.Sprintf("mem_%d", i),
			Metadata: map[string]any{"content": fmt.Sprintf("memory number %d", i), "index": float64(i)},
		}); err != nil {
			t.Fatalf("AddMemory %d: %v", i, err)
		}
	}
	if err := w.Close(Manifest{Index: "mem"}); err != nil {
		t.Fatal(err)
	}

	m, err := VerifyContent(dir)
	if err != nil {
		t.Fatalf("VerifyContent on a %d-record archive: %v", n, err)
	}
	if m.Counts.Memories != n {
		t.Errorf("manifest claims %d memories, want %d", m.Counts.Memories, n)
	}

	count := 0
	if err := MemoryRecords(dir, func(MemoryRecord) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Errorf("read %d records, want %d", count, n)
	}
}
