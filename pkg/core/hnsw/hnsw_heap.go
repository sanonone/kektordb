// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// graph algorithm for efficient approximate nearest neighbor search.
//
// This file defines the min-heap and max-heap data structures used extensively
// during the graph traversal and construction phases of the HNSW algorithm. These
// heaps are built on Go's standard container/heap package and are specialized
// for managing search candidates.
package hnsw

import (
	"container/heap"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// minHeap is a min-heap of candidates, ordered by distance. The candidate
// with the smallest distance (the nearest neighbor) is always at the top.
// This heap is used to store nodes that are yet to be visited during a search,
// ensuring that the algorithm always explores the most promising candidate next.
type minHeap []*types.Candidate

// Len returns the size of the heap.
func (h minHeap) Len() int { return len(h) }

// Less returns true if the candidate at index i has a smaller distance than the one at index j.
func (h minHeap) Less(i, j int) bool { return h[i].Distance < h[j].Distance }

// Swap swaps the elements at indices i and j.
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push adds an element to the heap. It uses a pointer receiver to modify the underlying slice.
func (h *minHeap) Push(x any) { *h = append(*h, x.(*types.Candidate)) }

// Pop removes and returns the element with the highest priority (the smallest distance) from the heap.
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return x
}

// maxHeap is a max-heap of candidates, ordered by distance. The candidate
// with the largest distance (the farthest neighbor) is always at the top.
// This heap is used to maintain the set of the k best nodes found so far.
// The root element is the "worst" of the best, making it easy to replace
// when a closer neighbor is discovered.
type maxHeap []*types.Candidate

// Len returns the size of the heap.
func (h maxHeap) Len() int { return len(h) }

// Less returns true if the candidate at index i has a larger distance than the one at index j,
// giving it a higher priority in the max-heap.
func (h maxHeap) Less(i, j int) bool { return h[i].Distance > h[j].Distance } // distanza più grande = priorità maggiore quindi deve salire in cima
// Swap swaps the elements at indices i and j.
func (h maxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push adds an element to the heap. It uses a pointer receiver to modify the underlying slice.
func (h *maxHeap) Push(x any) { *h = append(*h, x.(*types.Candidate)) }

// Pop removes and returns the element with the highest priority (the largest distance) from the heap.
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return x
}

// newMinHeap creates a new min-heap with a specified initial capacity.
func newMinHeap(capacity int) *minHeap {
	// Create a slice with length 0 but the given capacity to pre-allocate memory.
	h := make(minHeap, 0, capacity)
	// Initialize the slice as a heap. While it starts empty.
	heap.Init(&h)
	return &h
}

// newMaxHeap creates a new max-heap with a specified initial capacity.
func newMaxHeap(capacity int) *maxHeap {
	h := make(maxHeap, 0, capacity)
	heap.Init(&h)
	return &h
}
