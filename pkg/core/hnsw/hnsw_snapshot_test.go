package hnsw

import (
	"bytes"
	"encoding/gob"
	"math/rand"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestNodeGobSerialization verifica che la serializzazione gob di Node funzioni correttamente
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
	node.Deleted.Store(false)

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
	if decodedNode.Deleted.Load() != node.Deleted.Load() {
		t.Errorf("Deleted mismatch: got %v, want %v", decodedNode.Deleted.Load(), node.Deleted.Load())
	}
}

// TestNodeGobSerializationWithDeleted verifica la serializzazione del flag Deleted=true
func TestNodeGobSerializationWithDeleted(t *testing.T) {
	node := &Node{
		Id:         "test-node-deleted",
		InternalID: 99,
		VectorF32:  []float32{1.0, 2.0},
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

	if !decodedNode.Deleted.Load() {
		t.Error("Deleted should be true after deserialization")
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
	if decodedNode.VectorI8[0] != -128 || decodedNode.VectorI8[2] != 127 {
		t.Errorf("VectorI8 values mismatch: got %v", decodedNode.VectorI8)
	}
}

// TestSnapshotAndReload testa il ciclo completo di snapshot e ricaricamento
func TestSnapshotAndReload(t *testing.T) {
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", "")
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

	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()

	t.Logf("Before snapshot: %d nodes, counter=%d, entrypoint=%d, maxLevel=%d",
		len(nodes), counter, entrypoint, maxLevel)

	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		t.Fatal(err)
	}

	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms)
	if err != nil {
		t.Fatalf("Failed to load snapshot data: %v", err)
	}

	_, _, _, _, newCount, _ := newIdx.GetInfo()
	if int(newCount) != len(nodes) {
		t.Errorf("Count mismatch: got %d, want %d", newCount, len(nodes))
	}

	query := randomVector64(64)
	results1 := idx.SearchWithScores(query, 10, nil, 100)
	results2 := newIdx.SearchWithScores(query, 10, nil, 100)

	if len(results1) != len(results2) {
		t.Errorf("Search results count mismatch: got %d, want %d", len(results2), len(results1))
	}

	t.Logf("Search results: original=%d, reloaded=%d", len(results1), len(results2))
}

// TestDeletedNodeSnapshot verifica che i nodi marcati come eliminati vengano correttamente serializzati
func TestDeletedNodeSnapshot(t *testing.T) {
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", "")
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

	internalIDA, exists := idx.GetInternalID("a")
	if !exists {
		t.Fatal("Node 'a' not found")
	}

	nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms := idx.SnapshotData()

	if nodeToDelete, exists := nodes[internalIDA]; exists {
		nodeToDelete.Deleted.Store(true)
	} else {
		t.Fatal("Node 'a' not found in snapshot")
	}

	newIdx, err := New(16, 200, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		t.Fatal(err)
	}

	err = newIdx.LoadSnapshotData(nodes, extToInt, counter, entrypoint, maxLevel, quantizer, norms)
	if err != nil {
		t.Fatalf("Failed to load snapshot: %v", err)
	}

	newNodes, _, _, _, _, _, _ := newIdx.SnapshotData()
	loadedNode, exists := newNodes[internalIDA]
	if !exists {
		t.Fatal("Node 'a' not found in loaded index snapshot")
	}
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
