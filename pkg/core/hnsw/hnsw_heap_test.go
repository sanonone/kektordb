package hnsw

import (
	"container/heap"
	"github.com/sanonone/kektordb/pkg/core/types"
	"testing"
)

func TestMinHeapCorrectness(t *testing.T) {
	candidates := []types.Candidate{
		{Id: 1, Distance: 5.0},
		{Id: 2, Distance: 2.0},
		{Id: 3, Distance: 8.0},
		{Id: 4, Distance: 2.0}, // Duplicate distance to test implicit stability
	}

	h := new(minHeap)
	for _, c := range candidates {
		heap.Push(h, c)
	}

	// The nearest (smaller distance) must be at the top
	expetedOrder := []float64{2.0, 2.0, 5.0, 8.0}

	for i, expectedDist := range expetedOrder {
		c := heap.Pop(h).(types.Candidate)
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

	h := new(maxHeap)
	for _, c := range candidates {
		heap.Push(h, c)
	}

	expectedOrder := []float64{8.0, 8.0, 5.0, 2.0}

	for i, expectedDist := range expectedOrder {
		c := heap.Pop(h).(types.Candidate)
		if c.Distance != expectedDist {
			if c.Distance != expectedDist {
				t.Errorf("MaxHeap Pop %d: got distance %f, want %f", i, c.Distance, expectedDist)
			}
		}
	}
}
