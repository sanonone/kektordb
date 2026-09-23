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
	"math/rand"
	"os"
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

// --- C1: batching delle chiamate LLM ---

// batchedContradictionReply costruisce una risposta JSON array per n coppie.
func batchedContradictionReply(pairs int, contradict bool) string {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < pairs; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"pair": %d, "contradiction": %t, "reason": "r%d", "suggested_resolution": "", "action_required": false}`,
			i+1, contradict, i)
	}
	b.WriteString("]")
	return b.String()
}

// TestFase4C1BatchingCutsLLMCalls: con molte coppie nella finestra, le chiamate
// LLM devono essere ~pairs/contradictionBatchSize, non una per coppia.
func TestFase4C1BatchingCutsLLMCalls(t *testing.T) {
	eng := newFase4Engine(t, "fase4_batch")
	idx := "fase4_batch"

	// Vettori pseudo-casuali deterministici: producono una distribuzione di
	// similarità realistica con molte coppie nella finestra 0.70-0.95.
	// (Perturbazioni piccole su una base comune danno cos > 0.95: nessuna coppia.)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 30; i++ {
		v := make([]float32, 8)
		for d := range v {
			v[d] = rng.Float32()
		}
		eng.VAdd(idx, fmt.Sprintf("mem_%02d", i), v, map[string]any{
			"content": fmt.Sprintf("Statement %d about the same topic", i), "type": "memory",
		})
	}

	mock := &MockLLM{ContradictionReply: batchedContradictionReply(contradictionBatchSize, false)}
	g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
	g.detectContradictions(idx)

	if mock.CallCount == 0 {
		t.Fatal("nessuna chiamata LLM: il test non ha esercitato il path di batching")
	}

	// Conta le coppie analizzate: la marcatura avviene solo a verdetto ricevuto.
	ids, _, _ := eng.VGetIDsByCursor(idx, 0, 200)
	pairs := 0
	for _, id := range ids {
		if links, found := eng.VGetLinks(idx, id, "analyzed_against"); found {
			pairs += len(links)
		}
	}
	// Ogni coppia è marcata in entrambe le direzioni (link bidirezionale).
	distinctPairs := pairs / 2
	t.Logf("coppie distinte: %d, chiamate LLM: %d (rapporto %.1f coppie/chiamata)",
		distinctPairs, mock.CallCount, float64(distinctPairs)/float64(mock.CallCount))

	if distinctPairs > 0 && mock.CallCount*contradictionBatchSize < distinctPairs {
		t.Errorf("batching inefficace: %d chiamate per %d coppie (atteso <= %d)",
			mock.CallCount, distinctPairs, (distinctPairs+contradictionBatchSize-1)/contradictionBatchSize)
	}
}

// TestFase4C1AllPairsStillEvaluated: il batching non deve ridurre la copertura:
// ogni coppia nella finestra riceve un verdetto.
func TestFase4C1AllPairsStillEvaluated(t *testing.T) {
	eng := newFase4Engine(t, "fase4_batch_cov")
	idx := "fase4_batch_cov"

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 12; i++ {
		v := make([]float32, 8)
		for d := range v {
			v[d] = rng.Float32()
		}
		eng.VAdd(idx, fmt.Sprintf("p_%02d", i), v, map[string]any{
			"content": fmt.Sprintf("Fact %d", i), "type": "memory",
		})
	}

	// Conta le coppie attese (stessa logica della fase 1, senza dedup grafico).
	undirected := map[string]bool{}
	for i := 0; i < 12; i++ {
		d, err := eng.VGet(idx, fmt.Sprintf("p_%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		neigh, _ := eng.VSearchWithScores(idx, d.Vector, 5, "", 0)
		for _, nb := range neigh {
			if nb.ID == d.ID || nb.Score > 0.95 || nb.Score < 0.70 {
				continue
			}
			a, b := d.ID, nb.ID
			if a > b {
				a, b = b, a
			}
			undirected[a+"|"+b] = true
		}
	}

	mock := &MockLLM{ContradictionReply: batchedContradictionReply(contradictionBatchSize, false)}
	g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
	g.detectContradictions(idx)

	ids, _, _ := eng.VGetIDsByCursor(idx, 0, 200)
	marked := 0
	for _, id := range ids {
		if links, found := eng.VGetLinks(idx, id, "analyzed_against"); found {
			marked += len(links)
		}
	}
	markedPairs := marked / 2

	if len(undirected) == 0 {
		t.Fatal("nessuna coppia nella finestra: test vacuo")
	}
	t.Logf("coppie attese: %d, coppie marcate: %d, chiamate: %d", len(undirected), markedPairs, mock.CallCount)
	if markedPairs != len(undirected) {
		t.Errorf("copertura incompleta: %d coppie marcate su %d attese", markedPairs, len(undirected))
	}
}

// --- C3: niente marcatura senza verdetto ---

// TestFase4C3NoMarkingOnLLMFailure: se la chiamata LLM fallisce, nessuna coppia
// deve essere marcata analyzed_against: restano ri-esaminabili.
func TestFase4C3NoMarkingOnLLMFailure(t *testing.T) {
	eng := newFase4Engine(t, "fase4_fail")
	idx := "fase4_fail"

	eng.VAdd(idx, "f_a", []float32{0.5, 0.75, 0.1}, map[string]any{
		"content": "Python is a fast language", "type": "memory",
	})
	eng.VAdd(idx, "f_b", []float32{0.7, 0.3, 0.2}, map[string]any{
		"content": "Python is a slow language", "type": "memory",
	})

	mock := &failLLM{}
	g := NewGardener(eng, mock, Config{Enabled: true, Interval: time.Hour})
	g.detectContradictions(idx)

	if mock.CallCount == 0 {
		t.Fatal("LLM non chiamato: test vacuo")
	}
	ids, _, _ := eng.VGetIDsByCursor(idx, 0, 100)
	marked := 0
	for _, id := range ids {
		if links, found := eng.VGetLinks(idx, id, "analyzed_against"); found {
			marked += len(links)
		}
	}
	if marked != 0 {
		t.Errorf("analyzed_against edges = %d, want 0: un fallimento LLM non deve bruciare la coppia", marked)
	}
	if n := fase4CountReflections(t, eng, idx); n != 0 {
		t.Errorf("reflections = %d, want 0", n)
	}
}

// TestFase4C3IncompleteVerdictNotMarked: se il LLM risponde con meno verdetti
// del richiesto, le coppie senza verdetto NON vanno marcate (riprovano dopo),
// mentre quelle con verdetto sì.
func TestFase4C3IncompleteVerdictNotMarked(t *testing.T) {
	// Unit-level: il parser deve restituire solo i verdetti presenti.
	resp := `[{"pair": 1, "contradiction": false, "reason": "ok"}]`
	v := parseContradictionBatch(resp, 3)
	if len(v) != 1 {
		t.Fatalf("verdicts = %d, want 1", len(v))
	}
	if _, ok := v[0]; !ok {
		t.Error("verdict per pair 1 mancante")
	}
	if _, ok := v[1]; ok {
		t.Error("pair 2 non doveva avere verdetto (assente nella risposta)")
	}

	// Risposta con pair fuori range: ignorata.
	v2 := parseContradictionBatch(`[{"pair": 9, "contradiction": true}]`, 2)
	if _, ok := v2[8]; ok {
		t.Error("pair fuori range non doveva essere accettato")
	}

	// Array vuoto -> nessun verdetto.
	if v3 := parseContradictionBatch(`[]`, 2); len(v3) != 0 {
		t.Errorf("array vuoto: verdicts = %d, want 0", len(v3))
	}

	// Fallback oggetto singolo per batch di 1.
	v4 := parseContradictionBatch(`{"contradiction": true, "reason": "single"}`, 1)
	if it, ok := v4[0]; !ok || !it.Contradiction {
		t.Error("fallback oggetto singolo non ha prodotto il verdetto")
	}

	// Testo spazzatura -> nessun verdetto (niente panico).
	if v5 := parseContradictionBatch(`sorry, I cannot help`, 2); len(v5) != 0 {
		t.Errorf("risposta spazzatura: verdicts = %d, want 0", len(v5))
	}
}

// failLLM simula un LLM sempre in errore (rete giù, timeout).
type failLLM struct{ CallCount int }

func (f *failLLM) Chat(systemPrompt, userQuery string) (string, error) {
	f.CallCount++
	return "", fmt.Errorf("simulated network failure")
}

func (f *failLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	f.CallCount++
	return "", fmt.Errorf("simulated network failure")
}

// --- B3: gate su action_required ---

// fase4ReflectionSetup crea una reflection "vecchia" (così il punteggio
// epistemico è basso) con due memorie contraddittorie, e restituisce l'ID.
// ageDays controlla l'anzianità: memorie vecchie abbassano stability.
func fase4ReflectionSetup(t *testing.T, eng *engine.Engine, idx, reflID string, ageDays int, actionRequired any) {
	t.Helper()
	created := float64(time.Now().Unix() - int64(ageDays)*24*3600)
	reflMeta := map[string]any{
		"type":        "reflection",
		"status":      "unresolved",
		"content":     "Conflict detected: server location",
		"_created_at": created,
	}
	if actionRequired != nil {
		reflMeta["action_required"] = actionRequired
	}
	eng.VAdd(idx, reflID, []float32{0.5, 0.5, 0.5}, reflMeta)

	for j := 0; j < 2; j++ {
		memID := fmt.Sprintf("%s_m%d", reflID, j)
		eng.VAdd(idx, memID, []float32{0.5 + float32(j)*0.01, 0.5, 0.5}, map[string]any{
			"content":     fmt.Sprintf("Statement %d", j),
			"type":        "memory",
			"_created_at": created,
		})
		eng.VLink(idx, reflID, memID, "contradicts", "contradicted_by", 1.0, nil)
	}
	// Contraddizioni extra: saturano friction e abbassano il punteggio sotto
	// la soglia volatile, così il gate sul punteggio da solo si aprirebbe.
	for k := 0; k < 5; k++ {
		other := fmt.Sprintf("%s_extra_%d", reflID, k)
		eng.VAdd(idx, other, []float32{0.4 + float32(k)*0.01, 0.5, 0.5}, map[string]any{
			"content": "extra", "type": "memory", "_created_at": created,
		})
		eng.VLink(idx, other, reflID+"_m0", "contradicts", "contradicted_by", 1.0, nil)
	}
}

// TestFase4B3GateRequiresActionRequired: con action_required=false (o assente),
// una reflection che il punteggio epistemico considererebbe "volatile" NON deve
// essere auto-risolta: nessuna chiamata LLM, status invariato, nessun archivia.
func TestFase4B3GateRequiresActionRequired(t *testing.T) {
	cases := []struct {
		name           string
		actionRequired any // nil = campo assente
	}{
		{"action_required=false", false},
		{"action_required assente", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newFase4Engine(t, "fase4_b3")
			idx := "fase4_b3"
			reflID := "reflection_b3"
			fase4ReflectionSetup(t, eng, idx, reflID, 400, tc.actionRequired)

			mock := &MockLLM{Responses: []string{`{"resolvable": true, "consolidated_truth": "Merged", "clarification_question": ""}`}}
			g := NewGardener(eng, mock, Config{
				Enabled:                        true,
				Interval:                       time.Hour,
				EpistemicResolutionEnabled:     true,
				EpistemicMaxPerCycle:           3,
				EpistemicRequireActionRequired: true,
			})
			g.resolveVolatileBeliefs(idx)

			if mock.CallCount != 0 {
				t.Errorf("LLM calls = %d, want 0: senza action_required non si deve risolvere", mock.CallCount)
			}
			data, err := eng.VGet(idx, reflID)
			if err != nil {
				t.Fatal(err)
			}
			if data.Metadata["status"] != "unresolved" {
				t.Errorf("status = %v, want unresolved (nessun flip a stable)", data.Metadata["status"])
			}
			// Nessuna memoria deve essere diventata storica.
			for j := 0; j < 2; j++ {
				md, _ := eng.VGet(idx, fmt.Sprintf("%s_m%d", reflID, j))
				if hist, _ := md.Metadata["_is_historical"].(bool); hist {
					t.Errorf("memoria %d archiviata senza action_required", j)
				}
			}
		})
	}
}

// TestFase4B3GateOpensOnActionRequired: con action_required=true il percorso
// legittimo deve ancora funzionare (il gate non blocca tutto).
func TestFase4B3GateOpensOnActionRequired(t *testing.T) {
	eng := newFase4Engine(t, "fase4_b3_open")
	idx := "fase4_b3_open"
	reflID := "reflection_b3_open"
	fase4ReflectionSetup(t, eng, idx, reflID, 400, true)

	mock := &MockLLM{Responses: []string{`{"resolvable": true, "consolidated_truth": "Merged truth", "clarification_question": ""}`}}
	g := NewGardener(eng, mock, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: true,
	})
	g.resolveVolatileBeliefs(idx)

	if mock.CallCount == 0 {
		t.Fatal("LLM calls = 0: con action_required=true la risoluzione deve procedere")
	}
	data, err := eng.VGet(idx, reflID)
	if err != nil {
		t.Fatal(err)
	}
	if data.Metadata["status"] == "unresolved" {
		t.Errorf("status = unresolved, want risolto (action_required=true)")
	}
}

// TestFase4B3DisabledRestoresScoreGate: con EpistemicRequireActionRequired=false
// il gate torna a basarsi solo sul punteggio (comportamento precedente).
func TestFase4B3DisabledRestoresScoreGate(t *testing.T) {
	eng := newFase4Engine(t, "fase4_b3_off")
	idx := "fase4_b3_off"
	reflID := "reflection_b3_off"
	fase4ReflectionSetup(t, eng, idx, reflID, 400, false) // action_required=false

	mock := &MockLLM{Responses: []string{`{"resolvable": true, "consolidated_truth": "Merged", "clarification_question": ""}`}}
	g := NewGardener(eng, mock, Config{
		Enabled:                        true,
		Interval:                       time.Hour,
		EpistemicResolutionEnabled:     true,
		EpistemicMaxPerCycle:           3,
		EpistemicRequireActionRequired: false, // gate disattivato
	})
	g.resolveVolatileBeliefs(idx)

	if mock.CallCount == 0 {
		t.Error("LLM calls = 0: con il gate B3 disattivato deve valere il punteggio (che qui è volatile)")
	}
}

// --- B1: pesi del Gardener separati da quelli pubblici ---

// TestFase4B1GardenerWeightsDoNotAffectPublicAPI: i pesi del Gardener non
// devono cambiare le formule pubbliche di belief-assessment.
func TestFase4B1GardenerWeightsDoNotAffectPublicAPI(t *testing.T) {
	def := engine.DefaultEpistemicConfig()
	if def.Weights.Consensus != 0.40 || def.Weights.Stability != 0.30 || def.Weights.Friction != 0.30 {
		t.Errorf("DefaultEpistemicConfig weights changed: %+v (contratto pubblico documentato 0.40/0.30/0.30)",
			def.Weights)
	}
	gw := DefaultGardenerEpistemicWeights()
	if gw.Consensus == def.Weights.Consensus {
		t.Logf("pesi Gardener %+v vs pubblici %+v (devono differire per la ricalibrazione B1)", gw, def.Weights)
	}
	if gw.Friction <= gw.Consensus {
		t.Errorf("pesi Gardener: friction (%.2f) deve pesare più di consensus (%.2f)", gw.Friction, gw.Consensus)
	}
}

// TestFase4B1WeightsAreConfigurable: i pesi del Gardener sono configurabili e
// il default scatta solo quando tutti e tre sono zero.
func TestFase4B1WeightsAreConfigurable(t *testing.T) {
	// Default applicato quando i pesi sono tutti zero.
	dw := DefaultGardenerEpistemicWeights()
	if dw.Consensus != 0.20 || dw.Stability != 0.30 || dw.Friction != 0.50 {
		t.Errorf("default Gardener weights = %+v, want 0.20/0.30/0.50", dw)
	}

	// I pesi espliciti non vengono sovrascritti (verifica via LoadConfig).
	tmp := t.TempDir()
	cfgPath := tmp + "/cognitive.yaml"
	content := `
gardener:
  enabled: true
  epistemic_resolution_enabled: true
  epistemic_weights:
    consensus: 0.05
    stability: 0.15
    friction: 0.80
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.EpistemicWeights.Consensus != 0.05 || cfg.EpistemicWeights.Stability != 0.15 || cfg.EpistemicWeights.Friction != 0.80 {
		t.Errorf("pesi da YAML non applicati: %+v", cfg.EpistemicWeights)
	}
	if !cfg.EpistemicRequireActionRequired {
		t.Error("EpistemicRequireActionRequired deve essere true di default")
	}

	// Disattivazione esplicita del gate B3 via YAML.
	cfgPath2 := tmp + "/cognitive2.yaml"
	content2 := `
gardener:
  enabled: true
  epistemic_require_action_required: false
`
	if err := os.WriteFile(cfgPath2, []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, _, err := LoadConfig(cfgPath2)
	if err != nil {
		t.Fatalf("LoadConfig 2: %v", err)
	}
	if cfg2.EpistemicRequireActionRequired {
		t.Error("epistemic_require_action_required=false non rispettato")
	}
}
