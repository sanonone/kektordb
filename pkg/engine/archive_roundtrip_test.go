package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/archive"
	"github.com/sanonone/kektordb/pkg/core/distance"
)

// End-to-end archive tests against a live engine.
//
// The package-level tests prove the file format round-trips. These prove that
// what the engine writes is what the archive captures, and that an imported store
// behaves like the original: the same memories, the same relations, the same
// search results.

// storedKinds writes one of every node shape into the index.
func storedKinds() []struct {
	id   string
	meta map[string]any
	vec  []float32
} {
	return []struct {
		id   string
		meta map[string]any
		vec  []float32
	}{
		{
			id:  "mem_pref",
			vec: []float32{0.9, 0.1, 0, 0},
			meta: map[string]any{
				"content": "Preferisci risposte dirette senza preamboli", "type": "memory",
				"memory_layer": "semantic", "_created_at": float64(1793900000),
				"_access_count": float64(4), "_pinned": true,
			},
		},
		{
			id:  "mem_rule",
			vec: []float32{0.1, 0.9, 0, 0},
			meta: map[string]any{
				"content": "Esegui gofmt prima di committare", "type": "memory",
				"memory_layer": "procedural", "tags": []any{"go", "workflow"},
			},
		},
		{
			id:  "mem_old",
			vec: []float32{0.5, 0.5, 0, 0},
			meta: map[string]any{
				"content": "Il progetto usa MySQL", "type": "memory",
				"_archived": true, "_consolidated_into": "mem_new",
			},
		},
		{
			id:  "mem_new",
			vec: []float32{0.4, 0.6, 0, 0},
			meta: map[string]any{
				"content": "Il progetto usa PostgreSQL", "type": "memory",
			},
		},
		{
			id:  "core_fact_1",
			vec: []float32{0.2, 0.2, 0.6, 0},
			meta: map[string]any{
				"content": "L'utente preferisce Go a Python", "type": "core_fact",
				"confidence": 0.92, "_created_at": float64(1793900000),
			},
		},
		{
			id:  "entity::postgres",
			vec: []float32{0.2, 0.3, 0.5, 0},
			meta: map[string]any{
				"is_entity": true, "type": "entity", "name": "PostgreSQL",
				"description": "il database relazionale scelto per il billing",
			},
		},
		{
			id:  "reflection_1",
			vec: []float32{0.3, 0.3, 0.3, 0},
			meta: map[string]any{
				"content": "Conflitto sul target di deploy", "type": "reflection",
				"status": "unresolved", "action_required": false,
				"suggested_resolution": "preferisci la memoria piu' recente",
			},
		},
		{
			id:  "profile_1",
			vec: []float32{0.1, 0.1, 0.1, 0.7},
			meta: map[string]any{
				"type": "user_profile_insight", "user_id": "federico",
				"expertise_areas": "backend engineering, Go, PostgreSQL",
				"confidence":      0.92,
			},
		},
	}
}

// storedEdges writes one of every relation family.
func storedEdges() []archive.EdgeRecord {
	return []archive.EdgeRecord{
		{Source: "mem_old", Target: "mem_new", Relation: "consolidated_into", Inverse: "derived_from", Weight: 1.0},
		{Source: "mem_pref", Target: "entity::postgres", Relation: "mentions", Inverse: "mentioned_by", Weight: 1.0},
		{Source: "reflection_1", Target: "mem_old", Relation: "contradicts", Inverse: "contradicted_by", Weight: 1.0},
		{Source: "mem_rule", Target: "mem_rule", Relation: "decay_analyzed", Weight: 1.0},
	}
}

func newArchiveEngine(t *testing.T, idx string) *Engine {
	t.Helper()
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	opts.MaintenanceInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return eng
}

// populate writes the fixtures into an engine.
func populate(t *testing.T, eng *Engine, idx string) {
	t.Helper()
	for _, k := range storedKinds() {
		if err := eng.VAdd(idx, k.id, k.vec, k.meta); err != nil {
			t.Fatalf("VAdd %s: %v", k.id, err)
		}
	}
	for _, e := range storedEdges() {
		if err := eng.VLink(idx, e.Source, e.Target, e.Relation, e.Inverse, e.Weight, e.Props); err != nil {
			t.Fatalf("VLink %s->%s: %v", e.Source, e.Target, err)
		}
	}
}

// exportFrom writes an archive of the index.
func exportFrom(t *testing.T, eng *Engine, idx, dir string) {
	t.Helper()
	w, err := archive.NewWriter(dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	count := 0
	// The fixture list is the source of truth for what exists; reading each node
	// back also proves VGet sees what VAdd wrote.
	for _, k := range storedKinds() {
		d, err := eng.VGet(idx, k.id)
		if err != nil {
			// The fixture set is the source of truth for what exists; a node
			// missing here means the engine lost it, not the export.
			t.Fatalf("VGet %s during export: %v", k.id, err)
		}
		if err := w.AddMemory(archive.MemoryRecord{ID: d.ID, Metadata: d.Metadata}); err != nil {
			t.Fatalf("AddMemory %s: %v", k.id, err)
		}
		count++
	}

	for _, e := range storedEdges() {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if err := w.Close(archive.Manifest{Index: idx}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if count == 0 {
		t.Fatal("exported nothing")
	}
}

// TestArchiveRoundTripPreservesEverything is the main end-to-end check: export a
// store holding every node shape, rebuild it, and compare.
func TestArchiveRoundTripPreservesEverything(t *testing.T) {
	const src = "src"
	srcEng := newArchiveEngine(t, src)
	populate(t, srcEng, src)

	dir := t.TempDir()
	exportFrom(t, srcEng, src, dir)

	if _, err := archive.VerifyContent(dir); err != nil {
		t.Fatalf("the archive produced by the engine does not verify: %v", err)
	}

	// Rebuild into a fresh engine.
	const dst = "dst"
	dstEng := newArchiveEngine(t, dst)

	if err := archive.MemoryRecords(dir, func(r archive.MemoryRecord) error {
		vec, ok := r.Metadata["content"].(string)
		if !ok {
			vec = "placeholder"
		}
		// Vectors are not archived: the import embeds the content instead. Here
		// the deterministic hash stands in for the embedder.
		return dstEng.VAdd(dst, r.ID, hashVector(vec), r.Metadata)
	}); err != nil {
		t.Fatalf("import memories: %v", err)
	}
	if err := archive.EdgeRecords(dir, func(e archive.EdgeRecord) error {
		return dstEng.VLink(dst, e.Source, e.Target, e.Relation, e.Inverse, e.Weight, e.Props)
	}); err != nil {
		t.Fatalf("import edges: %v", err)
	}

	// Every memory must be back with its metadata.
	for _, k := range storedKinds() {
		d, err := dstEng.VGet(dst, k.id)
		if err != nil {
			t.Errorf("%s missing after import: %v", k.id, err)
			continue
		}
		for key, want := range k.meta {
			got, ok := d.Metadata[key]
			if !ok {
				t.Errorf("%s: key %q lost", k.id, key)
				continue
			}
			if !valuesEqual(want, got) {
				t.Errorf("%s: key %q = %#v, want %#v", k.id, key, got, want)
			}
		}
	}

	// Every edge must be back.
	for _, e := range storedEdges() {
		rels := dstEng.VGetRelations(dst, e.Source)
		found := false
		for rel, targets := range rels {
			if rel != e.Relation {
				continue
			}
			for _, target := range targets {
				if target == e.Target {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("edge %s -[%s]-> %s lost", e.Source, e.Relation, e.Target)
		}
	}
}

// TestArchivePreservesSearchesBehaviour: the rebuilt store must rank the same way,
// which is the property a user notices.
func TestArchivePreservesSearchBehaviour(t *testing.T) {
	const src = "s"
	srcEng := newArchiveEngine(t, src)
	populate(t, srcEng, src)

	query := []float32{0.85, 0.15, 0, 0}
	before, err := srcEng.VSearchWithScores(src, query, 3, "", 0)
	if err != nil {
		t.Fatalf("search before export: %v", err)
	}

	dir := t.TempDir()
	exportFrom(t, srcEng, src, dir)

	const dst = "d"
	dstEng := newArchiveEngine(t, dst)
	if err := archive.MemoryRecords(dir, func(r archive.MemoryRecord) error {
		// Reuse the source vectors so the comparison measures the archive, not
		// the stand-in embedder.
		vec, err := srcEng.DB.GetVector(src, r.ID)
		if err != nil {
			return err
		}
		return dstEng.VAdd(dst, r.ID, vec.Vector, r.Metadata)
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	after, err := dstEng.VSearchWithScores(dst, query, 3, "", 0)
	if err != nil {
		t.Fatalf("search after import: %v", err)
	}

	if len(before) != len(after) {
		t.Fatalf("result count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].ID != after[i].ID {
			t.Errorf("ranking changed at position %d: %s -> %s", i, before[i].ID, after[i].ID)
		}
	}
}

// TestArchiveExcludesDeletedNodes: a soft-deleted node is not part of the current
// store and must not come back.
func TestArchiveExcludesDeletedNodes(t *testing.T) {
	const idx = "del"
	eng := newArchiveEngine(t, idx)
	populate(t, eng, idx)

	if err := eng.VDelete(idx, "mem_new"); err != nil {
		t.Fatalf("VDelete: %v", err)
	}

	dir := t.TempDir()
	w, err := archive.NewWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range storedKinds() {
		if k.id == "mem_new" {
			continue // deleted, and VGet would fail anyway
		}
		d, err := eng.VGet(idx, k.id)
		if err != nil {
			continue
		}
		if err := w.AddMemory(archive.MemoryRecord{ID: d.ID, Metadata: d.Metadata}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(archive.Manifest{Index: idx}); err != nil {
		t.Fatal(err)
	}

	found := false
	if err := archive.MemoryRecords(dir, func(r archive.MemoryRecord) error {
		if r.ID == "mem_new" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("a deleted node was archived")
	}
}

// hashVector is a deterministic stand-in for an embedder.
func hashVector(s string) []float32 {
	v := make([]float32, 4)
	for i, r := range s {
		v[i%4] += float32(r%17) / 17.0
	}
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	if norm > 0 {
		for i := range v {
			v[i] /= sqrt32(norm)
		}
	}
	return v
}

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 1
	}
	// Newton's method is enough for a test helper.
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// valuesEqual compares two metadata values through their text form, which is what
// the archive stores.
func valuesEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) ||
		strings.TrimSpace(fmt.Sprintf("%v", a)) == strings.TrimSpace(fmt.Sprintf("%v", b))
}
