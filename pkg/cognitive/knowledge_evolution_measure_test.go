package cognitive

import (
	"fmt"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// countingLLM risponde come un LLM ma conta le chiamate, per misurare quanto
// costa il detector senza dipendere da un provider esterno.
type countingLLM struct {
	calls int
	reply string
}

func (c *countingLLM) Chat(system, user string) (string, error) {
	c.calls++
	return c.reply, nil
}
func (c *countingLLM) ChatWithImages(system, user string, imgs [][]byte) (string, error) {
	return c.Chat(system, user)
}

// TestKnowledgeEvolutionProducesReflection misura il detector: dato un'entita'
// con alta centralita' e conoscenza che cresce nel tempo, produce una reflection
// utile o solo una chiamata LLM sprecata?
//
// Serve a decidere se tenere la feature: oggi girando sempre, e nessuno sa
// cosa produca.
func TestKnowledgeEvolutionProducesReflection(t *testing.T) {
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close()

	idx := "evolution_measure"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.01 * float32(i)
	}

	nowUnix := float64(time.Now().Unix())

	// Una memoria prima dell'entita': stabilisce la dimensione dell'indice, cosi'
	// il VAdd con vettore vuoto non fallisce per dimensione sconosciuta.
	eng.VAdd(idx, "seed", vec, map[string]any{"content": "seed", "_created_at": nowUnix - 50*86400})

	// Entita' ad alta centralita' (>= 15 edge in entrata).
	eng.VAdd(idx, "entity_QC", nil, map[string]any{
		"name": "Quantum Computing", "type": "entity",
		"content": "Quantum Computing", "_created_at": nowUnix - 40*86400,
	})

	// 20 memorie collegate in tre finestre temporali: 30gg fa, 15gg fa, oggi.
	for i := 0; i < 20; i++ {
		var age float64
		switch {
		case i < 5:
			age = 32 * 86400 // prima finestra
		case i < 12:
			age = 17 * 86400 // seconda
		default:
			age = 2 * 86400 // recente
		}
		id := fmt.Sprintf("mem_qc_%02d", i)
		eng.VAdd(idx, id, vec, map[string]any{
			"content":     fmt.Sprintf("Memory %d about quantum computing", i),
			"_created_at": nowUnix - age,
		})
		eng.VLink(idx, id, "entity_QC", "mentions", "mentioned_in", 1.0, nil)
	}

	client := &countingLLM{reply: `{"timeline":"...","competency_level":"intermediate","knowledge_gaps":["error correction"],"next_steps":"study surface codes"}`}
	g := &Gardener{
		eng:              eng,
		llm:              client,
		scanCursors:      make(map[string]uint32),
		cursorGeneration: make(map[string]uint64),
		cfg:              Config{Mode: "advanced"},
	}

	g.detectKnowledgeEvolution(idx)

	t.Logf("entita' ad alta centralita': chiamate LLM=%d", client.calls)

	if client.calls == 0 {
		t.Fatal("il detector non ha chiamato l'LLM: con 20 edge in entrata avrebbe dovuto analizzare l'entita'")
	}

	// Il detector produce un nodo `knowledge_evolution`, non una `reflection`.
	// Verificarlo sul tipo giusto e' il punto: contando le reflection si
	// concluderebbe "non produce nulla" mentre produce.
	nodes, _ := eng.VFilter(idx, "type='knowledge_evolution'", 10)
	t.Logf("nodi knowledge_evolution creati: %d", len(nodes))
	if len(nodes) == 0 {
		t.Fatal("ha chiamato l'LLM ma non ha salvato nessun nodo: chiamata sprecata")
	}

	d, _ := eng.VGet(idx, nodes[0])
	meta := d.Metadata
	t.Logf("  topic=%v competenza=%v", meta["topic"], meta["competency_level"])
	t.Logf("  next_steps=%.100v", meta["content"])
	if gaps, ok := meta["knowledge_gaps"]; ok {
		t.Logf("  knowledge_gaps=%v", gaps)
	}
	if meta["content"] == "" || meta["content"] == nil {
		t.Error("il nodo esiste ma next_steps e' vuoto")
	}

	// Il contatore delle riflessioni NON viene incrementato da questo detector:
	// un'osservazione da riportare, perche' le statistiche direbbero zero.
	if g.totalReflections.Load() == 0 {
		t.Log("NOTA: totalReflections non viene incrementato (le stats non vedono questo detector)")
	}
}
