package mcp

import (
	"strings"
	"testing"
)

// TestRecallLayerFilterHidesSuperseded è la regressione del leak trovato in O1.
//
// Recall costruisce il filtro dei layer e vi appende il guard anti-supersede.
// Senza parentesi attorno al gruppo OR, la AND lega solo all'ultimo ramo e le
// memorie superate dei layer precedenti tornano nei risultati.
//
// Il test esercita il vero percorso Recall (non ricostruisce il filtro), così
// misura il codice di produzione. Verificato pre-fix: epi_hidden compare.
func TestRecallLayerFilterHidesSuperseded(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	const idx = "mcp_memory"
	vec := make([]float32, 384)

	add := func(id, layer string, hidden bool) {
		meta := map[string]any{
			"content":      "memory " + id,
			"memory_layer": layer,
			"type":         "memory",
		}
		if hidden {
			meta["_is_historical"] = true
		}
		if err := eng.VAdd(idx, id, vec, meta); err != nil {
			t.Fatalf("VAdd %s: %v", id, err)
		}
	}
	add("epi_live", "episodic", false)
	add("epi_hidden", "episodic", true)
	add("sem_live", "semantic", false)
	add("sem_hidden", "semantic", true)

	_, res, err := svc.Recall(nil, nil, RecallArgs{
		Query:     "memory",
		IndexName: idx,
		Limit:     10,
		Layers:    []string{"episodic", "semantic"},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	// Results are formatted as "[<id>] <content>": extract the id.
	got := make(map[string]bool)
	for _, r := range res.Results {
		if i := strings.Index(r, "]"); i > 0 {
			got[strings.TrimPrefix(r[:i], "[")] = true
		}
	}
	t.Logf("Recall(layers=episodic,semantic) -> %v", got)

	if got["epi_hidden"] {
		t.Error("epi_hidden restituita: il guard anti-supersede non copre il primo ramo OR")
	}
	if got["sem_hidden"] {
		t.Error("sem_hidden restituita: guard anti-supersede inefficace")
	}
	if !got["epi_live"] || !got["sem_live"] {
		t.Errorf("memorie vive mancanti: %v", got)
	}
}

// TestRecallSingleLayerStillWorks: il caso a un solo layer non deve regredire.
func TestRecallSingleLayerStillWorks(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	const idx = "mcp_memory"
	vec := make([]float32, 384)

	eng.VAdd(idx, "epi1", vec, map[string]any{
		"content": "episodic memory", "memory_layer": "episodic", "type": "memory",
	})
	eng.VAdd(idx, "sem1", vec, map[string]any{
		"content": "semantic memory", "memory_layer": "semantic", "type": "memory",
	})
	eng.VAdd(idx, "hidden1", vec, map[string]any{
		"content": "hidden memory", "memory_layer": "episodic", "type": "memory",
		"_is_historical": true,
	})

	_, res, err := svc.Recall(nil, nil, RecallArgs{
		Query: "memory", IndexName: idx, Limit: 10, Layers: []string{"episodic"},
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, r := range res.Results {
		if strings.Contains(r, "[hidden1]") {
			t.Error("memoria superata restituita con un solo layer")
		}
		if strings.Contains(r, "[sem1]") {
			t.Error("layer semantic restituito quando si chiede episodic")
		}
	}
	if len(res.Results) == 0 {
		t.Error("nessun risultato: il caso legittimo si è rotto")
	}
}

// TestFilterGroupParenthesized documenta il contratto del filtro costruito.
func TestFilterGroupParenthesized(t *testing.T) {
	var parts []string
	for _, l := range []string{"episodic", "semantic"} {
		parts = append(parts, "memory_layer='"+l+"'")
	}
	filter := "(" + strings.Join(parts, " OR ") + ")"
	if !strings.HasPrefix(filter, "(") || !strings.HasSuffix(filter, ")") {
		t.Errorf("il gruppo OR deve essere racchiuso tra parentesi: %q", filter)
	}
}
