package hnsw

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/types"
)

func TestMinHeapCorrectness(t *testing.T) {
	candidates := []types.Candidate{
		{Id: 1, Distance: 5.0},
		{Id: 2, Distance: 2.0},
		{Id: 3, Distance: 8.0},
		{Id: 4, Distance: 2.0}, // Duplicate distance to test implicit stability
	}

	h := newMinHeap(len(candidates))
	for _, c := range candidates {
		h.Push(c)
	}

	// The nearest (smaller distance) must be at the top
	expetedOrder := []float64{2.0, 2.0, 5.0, 8.0}

	for i, expectedDist := range expetedOrder {
		c := h.Pop()
		if c.Distance != expectedDist {
			t.Errorf("MinHeap Pop %d: got distance %f, want %f", i, c.Distance, expectedDist)
		}
	}
}

func TestMaxHeapCorrectness(t *testing.T) {
	candidates := []types.Candidate{
		{Id: 1, Distance: 5.0},
		{Id: 2, Distance: 8.0},
		{Id: 3, Distance: 2.0},
		{Id: 4, Distance: 8.0},
	}

	h := newMaxHeap(len(candidates))
	for _, c := range candidates {
		h.Push(c)
	}

	expectedOrder := []float64{8.0, 8.0, 5.0, 2.0}

	for i, expectedDist := range expectedOrder {
		c := h.Pop()
		if c.Distance != expectedDist {
			t.Errorf("MaxHeap Pop %d: got distance %f, want %f", i, c.Distance, expectedDist)
		}
	}
}
