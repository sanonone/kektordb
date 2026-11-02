package cognitive

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestTransferEdgesForRetirement verifica che archiviare una memoria non la
// scolleghi dal grafo: gli edge semantici devono migrare al successore, quelli
// temporali e di provenienza no.
//
// Il difetto che questo test previene: senza trasferimento, archiviare una
// memoria la lascia raggiungibile solo per id, mentre chi la puntava (entità,
// fatti correlati) resta agganciato a un nodo che nessuna ricerca restituisce.
func TestTransferEdgesForRetirement(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	defer eng.Close()

	idx := "retire_edges"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	v := make([]float32, 384)
	for i := range v {
		v[i] = 0.01 * float32(i)
	}
	for _, id := range []string{"source", "successor", "entity", "other"} {
		if err := eng.VAdd(idx, id, v, map[string]any{"content": id}); err != nil {
			t.Fatalf("VAdd %s: %v", id, err)
		}
	}

	// Edge semantico in uscita: source -> other (deve migrare)
	eng.VLink(idx, "source", "other", "related_to", "", 1.0, nil)
	// Edge semantico in entrata: entity -> source (deve migrare)
	eng.VLink(idx, "entity", "source", "mentions", "", 1.0, nil)
	// Edge temporale: source -> successor via "next" (NON deve migrare)
	eng.VLink(idx, "source", "other", "next", "", 1.0, nil)
	// Provenienza: successor -> source (NON deve migrare)
	eng.VLink(idx, "successor", "source", "extracted_from", "", 1.0, nil)

	g := &Gardener{eng: eng}
	g.transferEdgesForRetirement(idx, "source", "successor")

	// 1. l'edge semantico in uscita ora parte dal successore
	out, ok := eng.VGetLinks(idx, "successor", "related_to")
	if !ok || len(out) == 0 {
		t.Errorf("related_to non trasferito al successore: %v (ok=%v)", out, ok)
	}

	// 2. l'edge in entrata ora punta al successore
	in, ok := eng.VGetIncoming(idx, "successor", "mentions")
	if !ok || len(in) == 0 {
		t.Errorf("mentions in entrata non trasferito: %v (ok=%v)", in, ok)
	}

	// 3. gli edge temporali NON sono stati trasferiti
	next, _ := eng.VGetLinks(idx, "successor", "next")
	for _, target := range next {
		if target == "other" {
			t.Error("l'edge temporale 'next' non deve essere trasferito: un fatto estratto e' atemporale")
		}
	}

	// 4. extracted_from resta dov'e'
	prov, ok := eng.VGetLinks(idx, "successor", "extracted_from")
	found := false
	for _, target := range prov {
		if target == "source" {
			found = true
		}
	}
	if !ok || !found {
		t.Errorf("extracted_from deve restare sul successore verso la sorgente: %v", prov)
	}
}
