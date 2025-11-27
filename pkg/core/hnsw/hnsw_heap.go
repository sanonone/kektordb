// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// graph algorithm for efficient approximate nearest neighbor search.
//
// This file defines the min-heap and max-heap data structures used extensively
// during the graph traversal and construction phases of the HNSW algorithm.
// OPTIMIZED VERSION: Uses value semantics (no pointers) for CPU Cache locality and zero GC overhead.
package hnsw

import (
	"github.com/sanonone/kektordb/pkg/core/types"
)

// --- MIN HEAP (Coda di priorità: il più vicino è in cima) ---
// Usato per gestire la lista dei candidati da esplorare.

// minHeap is a specialized heap for candidates.
// NOTE: Stores values directly, not pointers.
type minHeap []types.Candidate

func (h minHeap) Len() int { return len(h) }

// Peek returns the top element without removing it.
// Returns a copy of the struct (cheap, ~16 bytes).
// If empty, returns a zero-value Candidate (check Len() before calling if unsure).
func (h minHeap) Peek() types.Candidate {
	if len(h) == 0 {
		return types.Candidate{}
	}
	return h[0]
}

// Push adds an element by value.
func (h *minHeap) Push(x types.Candidate) {
	*h = append(*h, x)
	h.up(len(*h) - 1)
}

// Pop removes the top element. Returns types.Candidate value.
func (h *minHeap) Pop() types.Candidate {
	old := *h
	n := len(old)
	x := old[0]       // Save the root (min)
	old[0] = old[n-1] // Move last to root

	*h = old[0 : n-1]

	if len(*h) > 0 {
		h.down(0, len(*h))
	}
	return x
}

func (h minHeap) up(j int) {
	for {
		i := (j - 1) / 2 // parent
		if i == j || !(h[j].Distance < h[i].Distance) {
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func (h minHeap) down(i0, n int) {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 {
			break
		}
		j := j1 // left child
		j2 := j1 + 1
		if j2 < n && h[j2].Distance < h[j1].Distance {
			j = j2 // right child
		}
		if !(h[j].Distance < h[i].Distance) {
			break
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

// newMinHeap initializes a heap with capacity.
func newMinHeap(capacity int) *minHeap {
	h := make(minHeap, 0, capacity)
	return &h
}

// --- MAX HEAP (Keep the k best: the worst is on top) ---
// Used to keep the final results and decide which ones to discard.

type maxHeap []types.Candidate

func (h maxHeap) Len() int { return len(h) }

func (h maxHeap) Peek() types.Candidate {
	if len(h) == 0 {
		return types.Candidate{}
	}
	return h[0]
}

func (h *maxHeap) Push(x types.Candidate) {
	*h = append(*h, x)
	h.up(len(*h) - 1)
}

func (h *maxHeap) Pop() types.Candidate {
	old := *h
	n := len(old)
	x := old[0]       // Save root (max)
	old[0] = old[n-1] // Move last to root
	*h = old[0 : n-1]

	if len(*h) > 0 {
		h.down(0, len(*h))
	}
	return x
}

func (h maxHeap) up(j int) {
	for {
		i := (j - 1) / 2
		if i == j || !(h[j].Distance > h[i].Distance) { // Greater than for MaxHeap
			break
		}
		h[i], h[j] = h[j], h[i]
		j = i
	}
}

func (h maxHeap) down(i0, n int) {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 {
			break
		}
		j := j1
		j2 := j1 + 1
		if j2 < n && h[j2].Distance > h[j1].Distance { // Greater than
			j = j2
		}
		if !(h[j].Distance > h[i].Distance) {
			break
		}
		h[i], h[j] = h[j], h[i]
		i = j
	}
}

func newMaxHeap(capacity int) *maxHeap {
	h := make(maxHeap, 0, capacity)
	return &h
}
