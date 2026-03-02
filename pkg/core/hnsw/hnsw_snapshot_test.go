package hnsw

import (
	"bytes"
	"encoding/gob"
	"math/rand"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestNodeGobSerialization verifica che la serializzazione custom
// EVITI di salvare i vettori, ma salvi tutto il resto.
func TestNodeGobSerialization(t *testing.T) {
	node := &Node{
		Id:         "test-node-1",
		InternalID: 42,
		VectorF32:  []float32{0.1, 0.2, 0.3, 0.4}, // Questo non deve essere salvato!
		Connections: [][]uint32{
			{1, 2, 3},
			{4, 5},
		},
	}
	node.Deleted.Store(true)

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(node); err != nil {
		t.Fatalf("Failed to encode node: %v", err)
	}

	dec := gob.NewDecoder(&buf)
	var decodedNode Node
	if err := dec.Decode(&decodedNode); err != nil {
		t.Fatalf("Failed to decode node: %v", err)
	}

	if decodedNode.Id != node.Id {
		t.Errorf("Id mismatch")
	}
	if decodedNode.Deleted.Load() != true {
		t.Errorf("Deleted flag mismatch")
	}
	// LA VERA PROVA: Il vettore F32 DEVE essere nullo dopo il decode!
	if len(decodedNode.VectorF32) != 0 {
		t.Errorf("VectorF32 dovvrebbe essere vuoto (ignorato da gob), ma ha len %d", len(decodedNode.VectorF32))
	}
}

// TestSnapshotAndReload testa il ciclo completo:
// Gob ignora i vettori -> Load li ripesca dal file .bin
func TestSnapshotAndReload(t *testing.T) {
	arenaDir := t.TempDir()

	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	// Inserisci dati (Dimensione 64)
	for i := 0; i < 100; i++ {
		vec := randomVector64(64)
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		_, err := idx.Add(id, vec)
		if err != nil {
			t.Fatalf("Failed to add vector %d: %v", i, err)
		}
	}

	// 1. Estrazione Dati per lo Snapshot
	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()
	arenaState := idx.GetArenaState()
	vectorDim := idx.GetDimension() // Prendi la dimensione

	// Chiudi per permettere riapertura su Windows
	idx.Close()

	// 2. Riavvio simulato
	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Ricaricamento: Notare l'aggiunta di 'vectorDim' alla fine
	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, arenaState, vectorDim)
	if err != nil {
		t.Fatalf("Failed to load snapshot data: %v", err)
	}
	defer newIdx.Close()

	// 4. Verifica: I vettori sono tornati?
	query := randomVector64(64)
	results2 := newIdx.SearchWithScores(query, 10, nil, 100)

	if len(results2) == 0 {
		t.Errorf("La ricerca ha fallito: i vettori Mmap non sono stati ripristinati correttamente.")
	}
	t.Logf("Search results on reloaded index: %d", len(results2))
}

// TestDeletedNodeSnapshot (Semplificato)
func TestDeletedNodeSnapshot(t *testing.T) {
	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	idx.Add("a", randomVector64(32))
	internalIDA, _ := idx.GetInternalIDUnlocked("a")

	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()
	arenaState := idx.GetArenaState()
	vectorDim := idx.GetDimension()

	if nodeToDelete, exists := nodes[internalIDA]; exists {
		nodeToDelete.Deleted.Store(true)
	}

	idx.Close()

	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, arenaState, vectorDim)

	newNodes, _, _, _, _, _, _ := newIdx.SnapshotData()
	if !newNodes[internalIDA].Deleted.Load() {
		t.Error("Il nodo dovrebbe essere ancora eliminato dopo il reload")
	}
}

func randomVector64(d int) []float32 {
	v := make([]float32, d)
	for i := 0; i < d; i++ {
		v[i] = rand.Float32()
	}
	return v
}
