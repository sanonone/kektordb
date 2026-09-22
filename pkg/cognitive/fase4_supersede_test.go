package cognitive

// Regressione Fase 4 / passo A: i rilevatori in background non devono
// analizzare memorie già superate (_is_historical) o archiviate (_archived).
// Sono nascoste dalla retrieval di default, quindi ri-analizzarle brucia
// chiamate LLM e può generare reflection/consolidamenti su materiale che il
// sistema ha già deciso essere obsoleto.
//
// Verificato pre-fix: TestFase4DetectContradictionsSkipsObsolete fallisce
// (i nodi storici venivano scansionati e producevano una reflection).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// newFase4Engine crea un engine con indice pronto per i test Fase 4.
func newFase4Engine(t *testing.T, idx string) *engine.Engine {
	t.Helper()
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	return eng
}

// countReflections conta i nodi reflection_* presenti nell'indice.
func fase4CountReflections(t *testing.T, eng *engine.Engine, idx string) int {
	t.Helper()
	ids, _, err := eng.VGetIDsByCursor(idx, 0, 500)
	if err != nil {
		t.Fatalf("VGetIDsByCursor: %v", err)
	}
	n := 0
	for _, id := range ids {
		if strings.HasPrefix(id, "reflection_") {
			n++
		}
	}
	return n
}

const fase4ContradictionReply = `{"contradiction": true, "reason": "opposite claims", "suggested_resolution": "keep newer", "action_required": true}`

// TestFase4DetectContradictionsSkipsObsolete: due memorie vicine ma ENTRAMBE
// già superate/archiviate non devono produrre nessuna chiamata LLM né reflection.
func TestFase4DetectContradictionsSkipsObsolete(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
	}{
		{"both historical", map[string]any{"_is_historical": true}},
		{"both archived", map[string]any{"_archived": true}},
		{"one historical one archived", nil}, // gestito sotto
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newFase4Engine(t, "fase4_obsolete")
			idx := "fase4_obsolete"

			metaA := map[string]any{"content": "Python is a fast language", "type": "memory"}
			metaB := map[string]any{"content": "Python is a slow language", "type": "memory"}
			if tc.meta != nil {
				for k, v := range tc.meta {
					metaA[k] = v
					metaB[k] = v
				}
			} else {
				metaA["_is_historical"] = true
				metaB["_archived"] = true
			}
			eng.VAdd(idx, "obs_a", []float32{0.5, 0.75, 0.1}, metaA)
			eng.VAdd(idx, "obs_b", []float32{0.7, 0.3, 0.2}, metaB)

			mock := &MockLLM{ContradictionReply: fase4ContradictionReply}
			g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
			g.detectContradictions(idx)

			if mock.CallCount != 0 {
				t.Errorf("LLM calls = %d, want 0 (nodi obsoleti non devono essere analizzati)", mock.CallCount)
			}
			if n := fase4CountReflections(t, eng, idx); n != 0 {
				t.Errorf("reflections = %d, want 0", n)
			}
		})
	}
}

// TestFase4DetectContradictionsPairsLiveMemoryOnly: una memoria VIVA vicina a
// una storica non deve produrre reflection: la coppia non è risolvibile in modo
// utile (metà della coppia è già nascosta).
func TestFase4DetectContradictionsPairsLiveMemoryOnly(t *testing.T) {
	eng := newFase4Engine(t, "fase4_mixed")
	idx := "fase4_mixed"

	eng.VAdd(idx, "live_a", []float32{0.5, 0.75, 0.1}, map[string]any{
		"content": "Python is a fast language", "type": "memory",
	})
	eng.VAdd(idx, "hist_b", []float32{0.7, 0.3, 0.2}, map[string]any{
		"content": "Python is a slow language", "type": "memory", "_is_historical": true,
	})

	mock := &MockLLM{ContradictionReply: fase4ContradictionReply}
	g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
	g.detectContradictions(idx)

	if mock.CallCount != 0 {
		t.Errorf("LLM calls = %d, want 0: la coppia vivo+storico non va analizzata", mock.CallCount)
	}
	if n := fase4CountReflections(t, eng, idx); n != 0 {
		t.Errorf("reflections = %d, want 0", n)
	}
}

// TestFase4DetectContradictionsStillWorksOnLiveMemories: la correzione non deve
// rompere il caso legittimo (due memorie vive e contraddittorie).
func TestFase4DetectContradictionsStillWorksOnLiveMemories(t *testing.T) {
	eng := newFase4Engine(t, "fase4_live")
	idx := "fase4_live"

	eng.VAdd(idx, "mem_fast", []float32{0.5, 0.75, 0.1}, map[string]any{
		"content": "Python is a fast language", "type": "working_memory",
	})
	eng.VAdd(idx, "mem_slow", []float32{0.7, 0.3, 0.2}, map[string]any{
		"content": "Python is a slow language", "type": "working_memory",
	})

	mock := &MockLLM{ContradictionReply: fase4ContradictionReply}
	g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
	g.detectContradictions(idx)

	if mock.CallCount == 0 {
		t.Fatal("LLM calls = 0: il caso legittimo (due memorie vive) deve ancora essere analizzato")
	}
	if n := fase4CountReflections(t, eng, idx); n != 1 {
		t.Fatalf("reflections = %d, want 1 (regressione: caso vivo non rilevato)", n)
	}
}

// TestFase4RedundantClustersSkipObsolete: lo stesso principio per il percorso
// di consolidamento (findRedundantClusters): un cluster di memorie già superate
// non deve essere riconsolidato.
func TestFase4RedundantClustersSkipObsolete(t *testing.T) {
	eng := newFase4Engine(t, "fase4_clusters")
	idx := "fase4_clusters"

	// 5 memorie quasi identiche (cluster abbondante) ma TUTTE storiche.
	now := float64(time.Now().Unix())
	for i := 0; i < 5; i++ {
		eng.VAdd(idx, fmt.Sprintf("hist_%d", i), []float32{0.5, 0.5, 0.5}, map[string]any{
			"content":        fmt.Sprintf("Duplicate obsolete fact %d", i),
			"type":           "memory",
			"_is_historical": true,
			"_created_at":    now,
		})
	}

	g := NewGardener(eng, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	clusters := g.findRedundantClusters(idx, 0.90, 2)
	if len(clusters) != 0 {
		t.Errorf("clusters = %d, want 0: memorie superate non vanno riconsolidate", len(clusters))
	}

	// Controprova: con memorie vive il cluster deve essere trovato.
	eng2 := newFase4Engine(t, "fase4_clusters_live")
	for i := 0; i < 5; i++ {
		eng2.VAdd("fase4_clusters_live", fmt.Sprintf("live_%d", i), []float32{0.5, 0.5, 0.5}, map[string]any{
			"content":     fmt.Sprintf("Duplicate live fact %d", i),
			"type":        "memory",
			"_created_at": now,
		})
	}
	g2 := NewGardener(eng2, &MockLLM{}, Config{Enabled: true, Interval: time.Hour})
	if clusters := g2.findRedundantClusters("fase4_clusters_live", 0.90, 2); len(clusters) == 0 {
		t.Error("clusters = 0 per memorie vive: la correzione ha rotto il caso legittimo")
	}
}

// TestFase4EpistemicMaxPerCycleBounds verifies EpistemicMaxPerCycle actually
// bounds how many reflections are processed per cycle (cost control).
func TestFase4EpistemicMaxPerCycleBounds(t *testing.T) {
	eng := newFase4Engine(t, "fase4_bounded")
	idx := "fase4_bounded"

	for i := 0; i < 10; i++ {
		reflID := fmt.Sprintf("reflection_%d", i)
		eng.VAdd(idx, reflID, []float32{0.5, 0.5, 0.5}, map[string]any{
			"type":        "reflection",
			"status":      "unresolved",
			"content":     fmt.Sprintf("Conflict %d", i),
			"_created_at": float64(time.Now().Unix() - int64(i*60)),
		})
		for j := 0; j < 2; j++ {
			memID := fmt.Sprintf("mem_%d_%d", i, j)
			eng.VAdd(idx, memID, []float32{0.5 + float32(j)*0.01, 0.5, 0.5}, map[string]any{
				"content":     fmt.Sprintf("Statement %d-%d", i, j),
				"type":        "memory",
				"_created_at": float64(time.Now().Unix() - int64(i*3600)),
			})
			eng.VLink(idx, reflID, memID, "contradicts", "contradicted_by", 1.0, nil)
		}
	}

	mock := &MockLLM{
		Responses: []string{`{"resolvable": true, "consolidated_truth": "Merged truth", "clarification_question": ""}`},
	}
	g := NewGardener(eng, mock, Config{
		Enabled:                    true,
		Interval:                   time.Hour,
		EpistemicResolutionEnabled: true,
		EpistemicMaxPerCycle:       3,
	})
	g.resolveVolatileBeliefs(idx)

	if mock.CallCount > 3 {
		t.Errorf("MaxPerCycle non rispettato: %d chiamate LLM > 3", mock.CallCount)
	}
	// Le reflection non processate devono restare unresolved.
	pending, _ := eng.VFilter(idx, "type='reflection' AND status='unresolved'", 100)
	if len(pending) < 7 {
		t.Errorf("reflection ancora unresolved = %d, want >=7 (le non processate non vanno toccate)", len(pending))
	}
}
