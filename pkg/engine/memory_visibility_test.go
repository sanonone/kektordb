package engine

// Test di O1: il guard di visibilità memoria si applica alle ricerche su indici
// memory (MemoryConfig.Enabled), è aggirabile con IncludeObsolete, e NON tocca
// gli indici non-memory.

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

func visibilitySetup(t *testing.T, idx string, memory bool) *Engine {
	t.Helper()
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	var memCfg *hnsw.MemoryConfig
	if memory {
		memCfg = &hnsw.MemoryConfig{Enabled: true, DecayHalfLife: hnsw.Duration(30 * 24 * 3600 * 1e9)}
	}
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, memCfg); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}
	eng.VAdd(idx, "live", v, map[string]any{"content": "live", "type": "memory"})
	eng.VAdd(idx, "superseded", v, map[string]any{"content": "old", "type": "memory", "_is_historical": true})
	eng.VAdd(idx, "archived", v, map[string]any{"content": "gone", "type": "memory", "_archived": true})
	return eng
}

func contains(res []string, id string) bool { return slices.Contains(res, id) }

// TestMemoryIndexHidesObsoleteByDefault: su indice memory le memorie superate
// non compaiono senza opt-in.
func TestMemoryIndexHidesObsoleteByDefault(t *testing.T) {
	eng := visibilitySetup(t, "mem_hidden", true)
	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}

	res, err := eng.VSearch("mem_hidden", v, 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("VSearch default -> %v", res)
	if contains(res, "superseded") {
		t.Error("memoria superata restituita di default su indice memory")
	}
	if contains(res, "archived") {
		t.Error("memoria archiviata restituita di default su indice memory")
	}
	if !contains(res, "live") {
		t.Error("memoria viva mancante")
	}

	// Con il filtro dell'utente (caso che rompeva la concatenazione di stringhe).
	res2, err := eng.VSearch("mem_hidden", v, 10, "type='memory' OR content='x'", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch con filtro OR: %v", err)
	}
	t.Logf("VSearch con filtro OR -> %v", res2)
	if contains(res2, "superseded") || contains(res2, "archived") {
		t.Error("filtro OR dell'utente ha aggirato il guard di visibilità")
	}
	if !contains(res2, "live") {
		t.Error("memoria viva mancante col filtro OR")
	}
}

// TestMemoryIndexIncludeObsolete: l'opt-in restituisce tutto (audit/debug).
func TestMemoryIndexIncludeObsolete(t *testing.T) {
	eng := visibilitySetup(t, "mem_audit", true)
	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}

	res, err := eng.VSearchWithOptions("mem_audit", v, 10, "", "", 0, 0.5, nil, SearchOptions{IncludeObsolete: true})
	if err != nil {
		t.Fatalf("VSearchWithOptions: %v", err)
	}
	t.Logf("VSearch include_obsolete -> %v", res)
	for _, want := range []string{"live", "superseded", "archived"} {
		if !contains(res, want) {
			t.Errorf("con IncludeObsolete manca %q in %v", want, res)
		}
	}
}

// TestNonMemoryIndexUnchanged: un indice generico non cambia comportamento.
func TestNonMemoryIndexUnchanged(t *testing.T) {
	eng := visibilitySetup(t, "plain_vec", false)
	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}

	res, err := eng.VSearch("plain_vec", v, 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("indice non-memory -> %v", res)
	for _, want := range []string{"live", "superseded", "archived"} {
		if !contains(res, want) {
			t.Errorf("indice non-memory: manca %q in %v (comportamento cambiato!)", want, res)
		}
	}
}

// TestSearchWithScoresHidesObsolete: stesso contratto per search-with-scores.
func TestSearchWithScoresHidesObsolete(t *testing.T) {
	eng := visibilitySetup(t, "mem_scores", true)
	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}

	res, err := eng.VSearchWithScores("mem_scores", v, 10, "", 0)
	if err != nil {
		t.Fatalf("VSearchWithScores: %v", err)
	}
	ids := make([]string, len(res))
	for i, r := range res {
		ids[i] = r.ID
	}
	t.Logf("VSearchWithScores default -> %v", ids)
	if contains(ids, "superseded") || contains(ids, "archived") {
		t.Errorf("superate restituite da VSearchWithScores: %v", ids)
	}

	res2, err := eng.VSearchWithScoresOptions("mem_scores", v, 10, "", 0, SearchOptions{IncludeObsolete: true})
	if err != nil {
		t.Fatalf("VSearchWithScoresOptions: %v", err)
	}
	if len(res2) != 3 {
		t.Errorf("con IncludeObsolete attesi 3 risultati, ottenuti %d", len(res2))
	}
}

// TestGraphTraversalStaysRaw: il traversal NON filtra (scelta deliberata): la
// topologia e le catene di evoluzione restano ispezionabili.
func TestGraphTraversalStaysRaw(t *testing.T) {
	eng := visibilitySetup(t, "mem_graph", true)
	v := make([]float32, 8)
	for i := range v {
		v[i] = 0.5
	}

	// Le ricerche filtrano il seed...
	res, err := eng.VSearch("mem_graph", v, 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	if contains(res, "superseded") {
		t.Error("il seed non doveva includere la memoria superata")
	}

	// ...ma il grafo resta navigabile verso i nodi superati.
	if err := eng.VSetMetadata("mem_graph", "live", map[string]any{"_probe": "x"}); err != nil {
		t.Fatalf("VSetMetadata: %v", err)
	}
	if err := eng.VLink("mem_graph", "live", "superseded", "superseded_by", "evolves_from", 1.0, nil); err != nil {
		t.Fatalf("VLink: %v", err)
	}
	edges, found := eng.VGetEdges("mem_graph", "live", "superseded_by", 0)
	if !found || len(edges) == 0 {
		t.Fatal("l'arco verso una memoria superata deve restare visibile (grafo neutro)")
	}
	t.Logf("arco live -> %s presente: topologia grezza preservata", edges[0].TargetID)
}
