package core

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestDeleteMetadataCleanup verifies that DeleteMetadata properly cleans all indexes
func TestDeleteMetadataCleanup(t *testing.T) {
	db := NewDB()

	// Create a test index
	err := db.CreateVectorIndex("test_idx", distance.Cosine, 16, 200, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add metadata for a node
	nodeID := uint32(42)
	metadata := map[string]any{
		"color": "red",
		"size":  100.5,
		"tags":  "blue green",
	}

	err = db.AddMetadata("test_idx", nodeID, metadata)
	if err != nil {
		t.Fatalf("Failed to add metadata: %v", err)
	}

	// Verify metadata exists
	retrievedMeta := db.GetMetadataForNode("test_idx", nodeID)
	if len(retrievedMeta) == 0 {
		t.Error("Metadata should exist before deletion")
	}

	// Delete metadata
	err = db.DeleteMetadata("test_idx", nodeID)
	if err != nil {
		t.Fatalf("Failed to delete metadata: %v", err)
	}

	// Verify metadata is deleted
	retrievedMeta = db.GetMetadataForNode("test_idx", nodeID)
	if len(retrievedMeta) != 0 {
		t.Errorf("Metadata should be empty after deletion, got: %v", retrievedMeta)
	}

	// Test idempotency: deleting non-existent metadata should not error
	err = db.DeleteMetadata("test_idx", uint32(999))
	if err != nil {
		t.Errorf("Deleting non-existent metadata should not error, got: %v", err)
	}

	// Test deleting from non-existent index
	err = db.DeleteMetadata("non_existent_idx", nodeID)
	if err == nil {
		t.Error("Deleting from non-existent index should return error")
	}
}

// TestDeleteMetadataMultipleNodes verifies cleanup with multiple nodes
func TestDeleteMetadataMultipleNodes(t *testing.T) {
	db := NewDB()

	err := db.CreateVectorIndex("multi_idx", distance.Cosine, 16, 200, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add metadata for multiple nodes
	for i := uint32(1); i <= 5; i++ {
		metadata := map[string]any{
			"id":    i,
			"color": "red",
		}
		err := db.AddMetadata("multi_idx", i, metadata)
		if err != nil {
			t.Fatalf("Failed to add metadata for node %d: %v", i, err)
		}
	}

	// Delete middle node
	err = db.DeleteMetadata("multi_idx", uint32(3))
	if err != nil {
		t.Fatalf("Failed to delete metadata: %v", err)
	}

	// Verify node 3 is deleted
	meta := db.GetMetadataForNode("multi_idx", uint32(3))
	if len(meta) != 0 {
		t.Error("Node 3 metadata should be deleted")
	}

	// Verify other nodes still exist
	for i := uint32(1); i <= 5; i++ {
		if i == 3 {
			continue
		}
		meta := db.GetMetadataForNode("multi_idx", i)
		if len(meta) == 0 {
			t.Errorf("Node %d metadata should still exist", i)
		}
	}
}
