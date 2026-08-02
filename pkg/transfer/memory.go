// Package transfer implements cross-index memory transfer logic shared by the
// HTTP API and the MCP service.
package transfer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

// Args configures a memory transfer between two indexes.
type Args struct {
	SourceIndex    string
	TargetIndex    string
	Query          string
	Limit          int
	WithGraph      bool
	TransferReason string
}

// Result reports the outcome of a memory transfer.
type Result struct {
	TransferredCount int      `json:"transferred_count"`
	SkippedCount     int      `json:"skipped_count"`
	TransferredIDs   []string `json:"transferred_ids"`
	Message          string   `json:"message"`
}

// TransferMemory transfers memories from one index to another.
// It preserves metadata, handles dimension mismatches (zero-vector fallback),
// merges metadata for existing nodes, and optionally copies graph topology.
func TransferMemory(ctx context.Context, eng *engine.Engine, embedder embeddings.Embedder, args Args) (Result, error) {
	// 1. Validation
	if args.SourceIndex == "" || args.TargetIndex == "" {
		return Result{}, fmt.Errorf("source_index and target_index are required")
	}
	if args.SourceIndex == args.TargetIndex {
		return Result{}, fmt.Errorf("source and target cannot be the same")
	}
	if args.Query == "" {
		return Result{}, fmt.Errorf("query is required")
	}

	// 2. Limit validation (hard cap 500)
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// 3. Check indices exist
	if !eng.IndexExists(args.SourceIndex) {
		return Result{}, fmt.Errorf("source index '%s' not found", args.SourceIndex)
	}
	if !eng.IndexExists(args.TargetIndex) {
		return Result{}, fmt.Errorf("target index '%s' not found", args.TargetIndex)
	}

	// 4. Search in source index
	if embedder == nil {
		return Result{}, fmt.Errorf("embedder not available")
	}
	vec, err := embedder.Embed(args.Query)
	if err != nil {
		return Result{}, fmt.Errorf("embedding error: %w", err)
	}

	sourceIDs, err := eng.VSearch(args.SourceIndex, vec, limit, "", "", 0, 0.5, nil)
	if err != nil {
		return Result{}, fmt.Errorf("search failed: %w", err)
	}

	if len(sourceIDs) == 0 {
		return Result{
			TransferredCount: 0,
			Message:          "No memories found matching query",
		}, nil
	}

	// 5. Fetch source data
	sourceData, err := eng.VGetMany(args.SourceIndex, sourceIDs)
	if err != nil {
		return Result{}, fmt.Errorf("failed to fetch source data: %w", err)
	}

	// 6. Get target dimension
	targetDim := getIndexDimension(eng, args.TargetIndex)

	// 7. Prepare transfer
	var toTransfer []types.BatchObject
	var transferredIDs []string
	skippedCount := 0
	idMapping := make(map[string]string) // sourceID -> targetID (for graph copy)

	for _, item := range sourceData {
		// Prepare metadata with provenance
		newMeta := make(map[string]any)
		for k, v := range item.Metadata {
			newMeta[k] = v
		}

		// Add provenance
		newMeta["_transferred_from"] = fmt.Sprintf("%s::%s", args.SourceIndex, item.ID)
		newMeta["_transferred_at"] = time.Now().Format(time.RFC3339)

		// Add transfer reason if provided
		if args.TransferReason != "" {
			newMeta["_transfer_reason"] = args.TransferReason
		}

		// Handle vector (dimension check)
		var newVec []float32
		if len(item.Vector) == targetDim && targetDim > 0 {
			newVec = item.Vector
		} else {
			// Dimension mismatch -> zero-vector (entity node)
			newVec = nil
		}

		// Check if ID already exists in target
		_, err := eng.VGet(args.TargetIndex, item.ID)
		if err == nil {
			// ID exists - merge metadata using VSetMetadata
			mergeMeta := make(map[string]any)
			for k, v := range newMeta {
				mergeMeta[k] = v
			}
			if err := eng.VSetMetadata(args.TargetIndex, item.ID, mergeMeta); err != nil {
				skippedCount++
				continue
			}
			transferredIDs = append(transferredIDs, item.ID)
			idMapping[item.ID] = item.ID
		} else {
			// New node - add to batch
			toTransfer = append(toTransfer, types.BatchObject{
				Id:       item.ID,
				Vector:   newVec,
				Metadata: newMeta,
			})
			transferredIDs = append(transferredIDs, item.ID)
			idMapping[item.ID] = item.ID
		}
	}

	// 8. Batch insert new nodes
	if len(toTransfer) > 0 {
		// Check if target index is empty and we need to bootstrap dimension
		targetDim := getIndexDimension(eng, args.TargetIndex)
		if targetDim == 0 {
			// Target is empty - ensure first item has a valid vector
			// Find first item with non-nil vector
			bootstrapIdx := -1
			for i, item := range toTransfer {
				if len(item.Vector) > 0 {
					bootstrapIdx = i
					break
				}
			}

			if bootstrapIdx > 0 {
				// Move bootstrap item to front
				toTransfer[0], toTransfer[bootstrapIdx] = toTransfer[bootstrapIdx], toTransfer[0]
			} else if bootstrapIdx == -1 {
				// All items are zero-vectors - need to create a bootstrap vector
				// Use embedder to generate a vector from the query
				bootstrapVec, err := embedder.Embed(args.Query)
				if err != nil {
					return Result{}, fmt.Errorf("cannot bootstrap target index: %w", err)
				}
				// Assign to first item
				toTransfer[0].Vector = bootstrapVec
			}
		}

		if err := eng.VAddBatch(args.TargetIndex, toTransfer); err != nil {
			return Result{}, fmt.Errorf("batch transfer failed: %w", err)
		}
	}

	// 9. Deep copy graph topology if requested
	if args.WithGraph && len(transferredIDs) > 0 {
		transferGraphTopology(eng, args.SourceIndex, args.TargetIndex, sourceIDs, idMapping)
	}

	// 10. Create agent proxy node
	agentNodeID := fmt.Sprintf("agent::%s", args.SourceIndex)
	createAgentProxyNode(eng, args.TargetIndex, agentNodeID, args.SourceIndex, transferredIDs)

	return Result{
		TransferredCount: len(transferredIDs),
		SkippedCount:     skippedCount,
		TransferredIDs:   transferredIDs,
		Message:          fmt.Sprintf("Transferred %d memories from %s to %s", len(transferredIDs), args.SourceIndex, args.TargetIndex),
	}, nil
}

// getIndexDimension returns the vector dimension of an index.
func getIndexDimension(eng *engine.Engine, indexName string) int {
	idx, ok := eng.DB.GetVectorIndex(indexName)
	if !ok {
		return 0
	}
	if hnswIdx, ok := idx.(*hnsw.Index); ok {
		return hnswIdx.GetDimension()
	}
	return 0
}

// transferGraphTopology copies graph edges between transferred nodes.
func transferGraphTopology(eng *engine.Engine, sourceIdx, targetIdx string, sourceIDs []string, idMapping map[string]string) {
	// Create set for O(1) lookup
	sourceSet := make(map[string]bool)
	for _, id := range sourceIDs {
		sourceSet[id] = true
	}

	// For each node, get its relations in source
	for _, sourceID := range sourceIDs {
		// Get all relations from source node
		outgoing := eng.VGetRelations(sourceIdx, sourceID)
		for relType, targets := range outgoing {
			for _, targetID := range targets {
				// Only copy if target is also in transferred set
				if sourceSet[targetID] {
					newSourceID := idMapping[sourceID]
					newTargetID := idMapping[targetID]
					if newSourceID != "" && newTargetID != "" {
						if err := eng.VLink(targetIdx, newSourceID, newTargetID, relType, "", 1.0, nil); err != nil {
							slog.Warn("transfer: failed to copy graph link during transfer",
								"source", newSourceID, "target", newTargetID, "error", err)
						}
					}
				}
			}
		}
	}
}

// createAgentProxyNode creates a proxy node representing the source agent.
func createAgentProxyNode(eng *engine.Engine, targetIdx, agentID, sourceIdx string, transferredIDs []string) {
	// Check if proxy already exists
	_, err := eng.VGet(targetIdx, agentID)
	if err != nil {
		// Create new proxy node (zero-vector entity)
		meta := map[string]any{
			"type":        "agent_proxy",
			"agent_name":  sourceIdx,
			"description": fmt.Sprintf("Proxy node representing agent %s", sourceIdx),
		}
		if err := eng.VAdd(targetIdx, agentID, nil, meta); err != nil {
			slog.Warn("transfer: failed to create agent proxy node during transfer", "agent", agentID, "error", err)
		}
	}

	// Link all transferred nodes to this agent
	for _, id := range transferredIDs {
		if err := eng.VLink(targetIdx, id, agentID, "transferred_from_agent", "", 1.0, nil); err != nil {
			slog.Warn("transfer: failed to link transferred node to agent proxy", "node", id, "agent", agentID, "error", err)
		}
	}
}
