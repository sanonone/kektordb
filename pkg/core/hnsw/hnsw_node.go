// Package hnsw provides the implementation of the Hierarchical Navigable Small World
// graph algorithm for efficient approximate nearest neighbor search.
//
// This file defines the Node struct, which is the fundamental building block of the
// HNSW graph. Each node represents a vector and its connections to other nodes
// across multiple layers.
package hnsw

import "sync/atomic"

// Node represents a single node within the HNSW graph. It contains the vector data,
// its connections at various layers, and metadata for identification and state.
type Node struct {
	// Id is the user-facing, external identifier for the vector.
	Id string
	// InternalID is a unique, memory-efficient identifier used for graph traversal.
	InternalID uint32
	// VectorF32 stores float32 vectors.
	// NOTE: Once published, this should be treated as immutable.
	VectorF32 []float32
	// VectorF16 stores float16 vectors (as uint16).
	// NOTE: Once published, this should be treated as immutable.
	VectorF16 []uint16
	// VectorI8 stores int8 vectors.
	// NOTE: Once published, this should be treated as immutable.
	VectorI8 []int8

	// Connections is a slice of slices, where the outer index represents the graph layer,
	// and the inner slice contains the list of neighbors at that layer.
	// Connections[0] holds the neighbors at the base layer (layer 0).
	// Neighbor IDs are stored as uint32 for memory efficiency.
	// Protected by fine-grained shard locks (shardsMu).
	Connections [][]uint32
	// Deleted is a flag used for soft deletes, marking the node as removed
	// without physically deleting it from the graph.
	// Uses atomic.Bool for thread-safe access.
	Deleted atomic.Bool
}
