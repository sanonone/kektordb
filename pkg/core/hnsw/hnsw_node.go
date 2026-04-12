package hnsw

import (
	"bytes"
	"encoding/gob"
	"sync/atomic"
)

// vecData holds the vector data pointing to mmap memory.
// Only one field is populated based on precision; the others remain nil.
// Stored via atomic.Pointer[vecData] on Node to prevent data races between
// the compactor's UpdateNodePointer (writer) and concurrent search reads.
type vecData struct {
	F32 []float32
	F16 []uint16
	I8  []int8
}

// Node represents a single node within the HNSW graph. It contains the vector data,
// its connections at various layers, and metadata for identification and state.
type Node struct {
	// Id is the user-facing, external identifier for the vector.
	Id string
	// InternalID is a unique, memory-efficient identifier used for graph traversal.
	InternalID uint32
	// vec holds the vector data (pointing into mmap memory) atomically.
	// Use SetVector/GetVectorF32/GetVectorF16/GetVectorI8 for thread-safe access.
	vec atomic.Pointer[vecData]
	// Connections is a slice of slices, where the outer index represents the graph layer,
	// and the inner slice contains the list of neighbors at that layer.
	// Connections[0] holds the neighbors at the base layer (layer 0).
	// Neighbor IDs are stored as uint32 for memory efficiency.
	// Protected by fine-grained shard locks (shardsMu).
	Connections [][]uint32
	// Deleted is a flag used for soft deletes, marking the node as removed
	// from the graph without physically deleting it.
	// Uses atomic.Bool for thread-safe access.
	Deleted atomic.Bool
}

// SetVector stores the vector data atomically.
func (n *Node) SetVector(v *vecData) {
	n.vec.Store(v)
}

// GetVectorF32 returns the float32 vector slice, or nil if not set.
func (n *Node) GetVectorF32() []float32 {
	if vd := n.vec.Load(); vd != nil {
		return vd.F32
	}
	return nil
}

// GetVectorF16 returns the uint16 (float16) vector slice, or nil if not set.
func (n *Node) GetVectorF16() []uint16 {
	if vd := n.vec.Load(); vd != nil {
		return vd.F16
	}
	return nil
}

// GetVectorI8 returns the int8 vector slice, or nil if not set.
func (n *Node) GetVectorI8() []int8 {
	if vd := n.vec.Load(); vd != nil {
		return vd.I8
	}
	return nil
}

// nodeGob is a serializable representation of Node for gob encoding.
type nodeGob struct {
	Id          string
	InternalID  uint32
	Connections [][]uint32
	Deleted     bool
}

// GobEncode implements the gob.GobEncoder interface.
// It explicitly IGNORES vector slices to prevent Snapshot bloat (Zero-Copy Mmap)
// and correctly extracts the value from the atomic.Bool.
func (n *Node) GobEncode() ([]byte, error) {
	alias := nodeGob{
		Id:          n.Id,
		InternalID:  n.InternalID,
		Connections: n.Connections,
		Deleted:     n.Deleted.Load(),
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(alias); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GobDecode implements the gob.GobDecoder interface.
func (n *Node) GobDecode(data []byte) error {
	var alias nodeGob
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&alias); err != nil {
		return err
	}

	n.Id = alias.Id
	n.InternalID = alias.InternalID
	n.Connections = alias.Connections
	n.Deleted.Store(alias.Deleted)

	// vec remains nil (not stored in gob).
	// It will be re-linked to mmap memory by LoadSnapshotData.
	return nil
}
