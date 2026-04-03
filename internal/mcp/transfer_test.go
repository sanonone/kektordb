package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/core/distance"
)

func TestTransferMemory(t *testing.T) {
	svc, eng, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Create two indexes
	sourceIdx := "researcher_memory"
	targetIdx := "writer_memory"

	eng.VCreate(sourceIdx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VCreate(targetIdx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Save some memories in source index
	saveArgs1 := SaveMemoryArgs{
		IndexName: sourceIdx,
		Content:   "Go 1.22 has new loop variable semantics that prevent closure bugs",
		Tags:      []string{"go", "programming"},
	}
	_, saveResult1, err := svc.SaveMemory(ctx, req, saveArgs1)
	if err != nil {
		t.Fatalf("Failed to save memory 1: %v", err)
	}

	saveArgs2 := SaveMemoryArgs{
		IndexName: sourceIdx,
		Content:   "The new slices package in Go 1.21 provides efficient sorting",
		Tags:      []string{"go", "standard-library"},
	}
	_, saveResult2, err := svc.SaveMemory(ctx, req, saveArgs2)
	if err != nil {
		t.Fatalf("Failed to save memory 2: %v", err)
	}

	t.Logf("Created memories: %s, %s", saveResult1.MemoryID, saveResult2.MemoryID)

	// Create a link between the two memories in source
	eng.VLink(sourceIdx, saveResult1.MemoryID, saveResult2.MemoryID, "related_to", "", 1.0, nil)

	// Test 1: Basic transfer
	transferArgs := TransferMemoryArgs{
		SourceIndex:    sourceIdx,
		TargetIndex:    targetIdx,
		Query:          "Go programming features",
		Limit:          10,
		WithGraph:      false,
		TransferReason: "Sharing Go knowledge with writer agent",
	}

	_, transferResult, err := svc.TransferMemory(ctx, req, transferArgs)
	if err != nil {
		t.Fatalf("TransferMemory failed: %v", err)
	}

	if transferResult.TransferredCount == 0 {
		t.Error("Expected some memories to be transferred")
	}

	t.Logf("Transferred %d memories", transferResult.TransferredCount)

	// Verify memories exist in target
	for _, id := range transferResult.TransferredIDs {
		data, err := eng.VGet(targetIdx, id)
		if err != nil {
			t.Errorf("Transferred memory %s not found in target: %v", id, err)
			continue
		}

		// Check provenance metadata
		if from, ok := data.Metadata["_transferred_from"].(string); !ok || from == "" {
			t.Errorf("Memory %s missing _transferred_from metadata", id)
		}

		// Check transfer reason
		if reason, ok := data.Metadata["_transfer_reason"].(string); !ok || reason != transferArgs.TransferReason {
			t.Errorf("Memory %s missing or incorrect _transfer_reason", id)
		}
	}

	// Test 2: Transfer with graph topology
	transferArgs2 := TransferMemoryArgs{
		SourceIndex: sourceIdx,
		TargetIndex: targetIdx,
		Query:       "Go features",
		Limit:       10,
		WithGraph:   true,
	}

	_, result2, err := svc.TransferMemory(ctx, req, transferArgs2)
	if err != nil {
		t.Fatalf("TransferMemory with graph failed: %v", err)
	}

	t.Logf("Transfer with graph: %d memories", result2.TransferredCount)

	// Verify agent proxy node exists
	agentNodeID := "agent::" + sourceIdx
	_, err = eng.VGet(targetIdx, agentNodeID)
	if err != nil {
		t.Errorf("Agent proxy node %s not found: %v", agentNodeID, err)
	}

	// Test 3: Same source and target (should fail)
	transferArgs3 := TransferMemoryArgs{
		SourceIndex: sourceIdx,
		TargetIndex: sourceIdx,
		Query:       "test",
	}

	_, _, err = svc.TransferMemory(ctx, req, transferArgs3)
	if err == nil {
		t.Error("Expected error for same source and target")
	}

	// Test 4: Non-existent source index
	transferArgs4 := TransferMemoryArgs{
		SourceIndex: "nonexistent",
		TargetIndex: targetIdx,
		Query:       "test",
	}

	_, _, err = svc.TransferMemory(ctx, req, transferArgs4)
	if err == nil {
		t.Error("Expected error for non-existent source index")
	}
}

func TestTransferMemoryMerge(t *testing.T) {
	svc, eng, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Create two indexes
	sourceIdx := "agent_a_memory"
	targetIdx := "agent_b_memory"

	eng.VCreate(sourceIdx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VCreate(targetIdx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Save memory in source with specific ID
	saveArgs := SaveMemoryArgs{
		IndexName: sourceIdx,
		Content:   "Machine learning is transforming software engineering",
		Tags:      []string{"ml", "ai"},
	}
	_, saveResult, err := svc.SaveMemory(ctx, req, saveArgs)
	if err != nil {
		t.Fatalf("Failed to save memory: %v", err)
	}

	// First transfer
	transferArgs := TransferMemoryArgs{
		SourceIndex:    sourceIdx,
		TargetIndex:    targetIdx,
		Query:          "machine learning",
		Limit:          10,
		TransferReason: "First transfer",
	}

	_, _, err = svc.TransferMemory(ctx, req, transferArgs)
	if err != nil {
		t.Fatalf("First transfer failed: %v", err)
	}

	// Verify first transfer
	data1, _ := eng.VGet(targetIdx, saveResult.MemoryID)
	if data1.Metadata["_transfer_reason"] != "First transfer" {
		t.Error("First transfer reason not set correctly")
	}

	// Second transfer (same memory) with different reason - should merge
	transferArgs2 := TransferMemoryArgs{
		SourceIndex:    sourceIdx,
		TargetIndex:    targetIdx,
		Query:          "machine learning",
		Limit:          10,
		TransferReason: "Second transfer - updated",
	}

	_, result2, err := svc.TransferMemory(ctx, req, transferArgs2)
	if err != nil {
		t.Fatalf("Second transfer failed: %v", err)
	}

	// Verify merge - should update metadata but keep ID
	data2, _ := eng.VGet(targetIdx, saveResult.MemoryID)
	if data2.Metadata["_transfer_reason"] != "Second transfer - updated" {
		t.Error("Metadata not merged correctly on second transfer")
	}

	// Log result
	t.Logf("Second transfer result: %d transferred, %d skipped", result2.TransferredCount, result2.SkippedCount)
}
