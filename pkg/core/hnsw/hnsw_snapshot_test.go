package hnsw

import (
	"bytes"
	"encoding/gob"
	"math/rand"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// Per far funzionare gob con Node (che ora ha campi atomici come Deleted o entrypoint),
// potremmo dover scrivere GobEncode/GobDecode custom.
// Per questo test, verifichiamo la serializzazione dei campi base (vettori).
func TestNodeGobSerialization(t *testing.T) {
	node := &Node{
		Id:         "test-node-1",
		InternalID: 42,
		VectorF32:  []float32{0.1, 0.2, 0.3, 0.4},
		Connections: [][]uint32{
			{1, 2, 3},
			{4, 5},
		},
	}
	// node.Deleted.Store(false)

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
		t.Errorf("Id mismatch: got %s, want %s", decodedNode.Id, node.Id)
	}
	if decodedNode.InternalID != node.InternalID {
		t.Errorf("InternalID mismatch: got %d, want %d", decodedNode.InternalID, node.InternalID)
	}
	if len(decodedNode.VectorF32) != len(node.VectorF32) {
		t.Errorf("VectorF32 length mismatch: got %d, want %d", len(decodedNode.VectorF32), len(node.VectorF32))
	}
}

// TestNodeGobSerializationF16 verifica la serializzazione con VectorF16
func TestNodeGobSerializationF16(t *testing.T) {
	node := &Node{
		Id:         "test-node-f16",
		InternalID: 100,
		VectorF16:  []uint16{1000, 2000, 3000},
		Connections: [][]uint32{
			{10, 20},
		},
	}

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

	if len(decodedNode.VectorF16) != 3 {
		t.Errorf("VectorF16 length mismatch: got %d, want 3", len(decodedNode.VectorF16))
	}
	if decodedNode.VectorF32 != nil || decodedNode.VectorI8 != nil {
		t.Error("Other vector fields should be nil")
	}
}

// TestNodeGobSerializationI8 verifica la serializzazione con VectorI8
func TestNodeGobSerializationI8(t *testing.T) {
	node := &Node{
		Id:         "test-node-i8",
		InternalID: 200,
		VectorI8:   []int8{-128, 0, 127, 50},
	}

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

	if len(decodedNode.VectorI8) != 4 {
		t.Errorf("VectorI8 length mismatch: got %d, want 4", len(decodedNode.VectorI8))
	}
}

// TestSnapshotAndReload testa il ciclo completo di snapshot e ricaricamento
func TestSnapshotAndReload(t *testing.T) {
	// USA UNA TEMP DIR PER L'ARENA MMAP
	arenaDir := t.TempDir()

	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		vec := randomVector64(64)
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		_, err := idx.Add(id, vec)
		if err != nil {
			t.Fatalf("Failed to add vector %d: %v", i, err)
		}
	}

	// 1. Snapshot dei dati
	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()

	// NUOVO: Recupera anche lo stato dell'Arena (SlotTable)
	arenaState := idx.GetArenaState()

	t.Logf("Before snapshot: %d nodes, counter=%d, entrypoint=%d, maxLevel=%d",
		len(nodes), counter, entrypoint, maxLevel)

	// CHIUDI l'indice vecchio (Sgancia la memoria Mmap per Windows!)
	idx.Close()

	// 2. Riavvio simulato: Nuovo indice, stessa directory Mmap
	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Ricaricamento: Aggiungi arenaState alla fine
	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, arenaState)
	if err != nil {
		t.Fatalf("Failed to load snapshot data: %v", err)
	}
	defer newIdx.Close() // Pulizia finale

	_, _, _, _, newCount, _ := newIdx.GetInfo()
	if int(newCount) != len(nodes) {
		t.Errorf("Count mismatch: got %d, want %d", newCount, len(nodes))
	}

	// 4. Verifica Ricerca (Se i puntatori mmap non sono stati ripristinati, questo darà Segfault!)
	query := randomVector64(64)
	results2 := newIdx.SearchWithScores(query, 10, nil, 100)

	if len(results2) == 0 {
		t.Errorf("Search returned 0 results after reload")
	}

	t.Logf("Search results on reloaded index: %d", len(results2))
}

// TestDeletedNodeSnapshot verifica che i nodi marcati come eliminati vengano correttamente serializzati
func TestDeletedNodeSnapshot(t *testing.T) {
	arenaDir := t.TempDir()

	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		vec := randomVector64(32)
		_, err := idx.Add(string(rune('a'+i)), vec)
		if err != nil {
			t.Fatal(err)
		}
	}

	internalIDA, exists := idx.GetInternalIDUnlocked("a") // Usa Unlocked o il metodo corretto
	if !exists {
		t.Fatal("Node 'a' not found")
	}

	// Estrai i nodi e marca eliminato manualmente per il test
	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()
	arenaState := idx.GetArenaState()

	if nodeToDelete, exists := nodes[internalIDA]; exists {
		// A seconda di come hai implementato Deleted:
		// nodeToDelete.Deleted = true
		nodeToDelete.Deleted.Store(true)
	} else {
		t.Fatal("Node 'a' not found in snapshot")
	}

	idx.Close()

	// Ricarica
	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", arenaDir)
	if err != nil {
		t.Fatal(err)
	}

	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms, arenaState)
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}
	defer newIdx.Close()

	// Riestrai per verificare
	newNodes, _, _, _, _, _, _ := newIdx.SnapshotData()
	loadedNode, exists := newNodes[internalIDA]
	if !exists {
		t.Fatal("Node 'a' not found in loaded index snapshot")
	}

	// Verifica che lo stato sia rimasto
	if !loadedNode.Deleted.Load() {
		t.Error("Node should still be marked as deleted after reload")
	}
}

// randomVector64 genera un vettore casuale di dimensione d
func randomVector64(d int) []float32 {
	v := make([]float32, d)
	for i := 0; i < d; i++ {
		v[i] = rand.Float32()
	}
	return v
}
