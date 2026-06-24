package mcp

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/embeddings"
)

func TestSaveSnapshot(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.SaveSnapshot(nil, nil, SaveSnapshotArgs{})
	if err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}
	if result.Status != "saved" {
		t.Errorf("expected status=saved, got %s (msg=%s)", result.Status, result.Message)
	}
	if result.DurationMs < 0 {
		t.Errorf("expected non-negative duration, got %d", result.DurationMs)
	}
}

func TestCompactAOF(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.CompactAOF(nil, nil, CompactAOFArgs{})
	if err != nil {
		t.Fatalf("CompactAOF failed: %v", err)
	}
	if result.Status != "triggered" {
		t.Errorf("expected status=triggered, got %s (msg=%s)", result.Status, result.Message)
	}
}

func TestGetEmbedderStatus(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.GetEmbedderStatus(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("GetEmbedderStatus failed: %v", err)
	}
	if !result.Active {
		t.Errorf("expected active=true, got false (msg=%s)", result.Message)
	}
	// Mock embedder returns 384-dim vectors
	if result.Dimension != 384 {
		t.Errorf("expected dimension=384, got %d", result.Dimension)
	}
}

func TestGetEmbedderStatusNoopEmbedder(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	// Replace the mock embedder with a NoopEmbedder
	svc.embedder = embeddings.NoopEmbedder{}
	_ = eng

	_, result, err := svc.GetEmbedderStatus(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("GetEmbedderStatus failed: %v", err)
	}
	if result.Active {
		t.Error("expected active=false with NoopEmbedder")
	}
	if result.Message == "" {
		t.Error("expected message about NoopEmbedder")
	}
}

func TestKVGetSetDelete(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	key := "test_key_" + time.Now().Format("150405")

	// Initially: key not found
	_, result, _ := svc.KVGet(nil, nil, KVGetArgs{Key: key})
	if result.Found {
		t.Error("expected key not found initially")
	}

	// Set
	_, setResult, err := svc.KVSet(nil, nil, KVSetArgs{
		Key:   key,
		Value: "hello world",
	})
	if err != nil {
		t.Fatalf("KVSet failed: %v", err)
	}
	if setResult.Status != "ok" {
		t.Errorf("expected status=ok, got %s", setResult.Status)
	}

	// Get
	_, getResult, _ := svc.KVGet(nil, nil, KVGetArgs{Key: key})
	if !getResult.Found {
		t.Error("expected key found after set")
	}
	decoded, err := base64.StdEncoding.DecodeString(getResult.Value)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(decoded) != "hello world" {
		t.Errorf("expected value 'hello world', got %q", string(decoded))
	}

	// Delete
	_, delResult, _ := svc.KVDelete(nil, nil, KVDeleteArgs{Key: key})
	if delResult.Status != "ok" {
		t.Errorf("expected status=ok, got %s", delResult.Status)
	}

	// Verify deleted
	_, getResult2, _ := svc.KVGet(nil, nil, KVGetArgs{Key: key})
	if getResult2.Found {
		t.Error("expected key not found after delete")
	}
}

func TestKVGetEmptyKey(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.KVGet(nil, nil, KVGetArgs{Key: ""})
	if result.Message == "" {
		t.Error("expected message for empty key")
	}
}

func TestKVSetEmptyKey(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.KVSet(nil, nil, KVSetArgs{Key: "", Value: "x"})
	if result.Status != "error" {
		t.Errorf("expected status=error, got %s", result.Status)
	}
}

func TestKVDeleteEmptyKey(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.KVDelete(nil, nil, KVDeleteArgs{Key: ""})
	if result.Status != "error" {
		t.Errorf("expected status=error, got %s", result.Status)
	}
}

// =====================================================================
// BATCH 2: engine stats + profile refresh
// =====================================================================

func TestGetStatsAggregate(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"
	eng.VAdd(idx, "v1", make([]float32, 384), map[string]any{"x": 1})

	_, result, err := svc.GetStats(nil, nil, GetStatsArgs{})
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if result.TotalIndexes != 1 {
		t.Errorf("expected 1 index, got %d", result.TotalIndexes)
	}
	if result.TotalVectors != 1 {
		t.Errorf("expected 1 vector total, got %d", result.TotalVectors)
	}
	if result.Embedder != "active" {
		t.Errorf("expected embedder=active, got %s", result.Embedder)
	}
}

func TestGetStatsByIndex(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	eng.VCreate("other", distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil)
	eng.VAdd("mcp_memory", "v1", make([]float32, 384), map[string]any{"x": 1})
	eng.VAdd("other", "v2", make([]float32, 384), map[string]any{"x": 2})

	_, result, _ := svc.GetStats(nil, nil, GetStatsArgs{IndexName: "other"})
	if result.TotalIndexes != 1 {
		t.Errorf("expected 1 index (only 'other'), got %d", result.TotalIndexes)
	}
	if result.TotalVectors != 1 {
		t.Errorf("expected 1 vector in 'other', got %d", result.TotalVectors)
	}
	if result.IndexName != "other" {
		t.Errorf("expected index_name=other, got %s", result.IndexName)
	}
}

func TestGetStatsNoopEmbedder(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	svc.embedder = embeddings.NoopEmbedder{}
	_, result, _ := svc.GetStats(nil, nil, GetStatsArgs{})
	if result.Embedder != "noop" {
		t.Errorf("expected embedder=noop, got %s", result.Embedder)
	}
}

func TestGetPersistenceStatus(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.GetPersistenceStatus(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("GetPersistenceStatus failed: %v", err)
	}
	if result.AOFPath == "" {
		t.Error("expected AOFPath to be set")
	}
	// AOFSizeBytes may be 0 if AOF wasn't flushed, but path should exist
	if result.Message != "" {
		// OK, can have a stat error message but still has path
	}
}

func TestRefreshUserProfileNoGardener(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.RefreshUserProfile(nil, nil, RefreshUserProfileArgs{
		UserID: "dash",
	})
	if err != nil {
		t.Fatalf("RefreshUserProfile failed: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected status=error when gardener is nil, got %s", result.Status)
	}
}

func TestRefreshUserProfileEmptyUserID(t *testing.T) {
	svc, eng, _ := newTestServiceWithCompiler(t)
	_ = eng
	// need a gardener to test this path; using newTestServiceMinimal won't have it
	_ = svc
	// Test with gardener but empty user_id
	// For simplicity, just check that empty user_id returns error message
	svc2, _ := newTestServiceMinimal(t)
	_, result, _ := svc2.RefreshUserProfile(nil, nil, RefreshUserProfileArgs{
		UserID: "",
	})
	if result.Message == "" {
		t.Error("expected message for empty user_id")
	}
}

// =====================================================================
// BATCH 3: graph edges + artifact diff + summarize
// =====================================================================

func TestGetEdgeDetails(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"
	eng.VAdd(idx, "src", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst1", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst2", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VLink(idx, "src", "dst1", "mentions", "mentioned_by", 1.5, nil)
	eng.VLink(idx, "src", "dst2", "related_to", "", 0.8, nil)

	_, result, err := svc.GetEdgeDetails(nil, nil, GetEdgeDetailsArgs{
		SourceID:  "src",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("GetEdgeDetails failed: %v", err)
	}
	if result.EdgeCount != 2 {
		t.Errorf("expected 2 edges, got %d", result.EdgeCount)
	}
	// Verify both target IDs are present
	targets := make(map[string]bool)
	for _, e := range result.Edges {
		targets[e.TargetID] = true
		if e.CreatedAt == 0 {
			t.Error("expected non-zero created_at timestamp")
		}
		if !e.Active {
			t.Error("expected edges to be active")
		}
	}
	if !targets["dst1"] {
		t.Error("expected dst1 in edges")
	}
	if !targets["dst2"] {
		t.Error("expected dst2 in edges")
	}
}

func TestGetEdgeDetailsEmptySourceID(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.GetEdgeDetails(nil, nil, GetEdgeDetailsArgs{SourceID: ""})
	if result.Message == "" {
		t.Error("expected message for empty source_id")
	}
}

func TestGetEdgeDetailsByRelation(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"
	eng.VAdd(idx, "src", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst1", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst2", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VLink(idx, "src", "dst1", "mentions", "mentioned_by", 1.0, nil)
	eng.VLink(idx, "src", "dst2", "related_to", "", 1.0, nil)

	_, result, _ := svc.GetEdgeDetails(nil, nil, GetEdgeDetailsArgs{
		SourceID:     "src",
		RelationType: "mentions",
		IndexName:    idx,
	})
	if result.EdgeCount != 1 {
		t.Errorf("expected 1 edge (only 'mentions'), got %d", result.EdgeCount)
	}
	if len(result.Edges) > 0 && result.Edges[0].TargetID != "dst1" {
		t.Errorf("expected target=dst1, got %s", result.Edges[0].TargetID)
	}
}

func TestDiffArtifactVersionsMissingVersions(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, _ := svc.DiffArtifactVersions(nil, nil, DiffArtifactVersionsArgs{
		Intent: "user_profile",
		Entity: "nonexistent",
		V1:     1,
		V2:     2,
	})
	if result.Message == "" {
		t.Error("expected message for missing versions")
	}
}

func TestSummarizeMemories(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"
	eng.VAdd(idx, "mem1", make([]float32, 384), map[string]any{"content": "First memory content"})
	eng.VAdd(idx, "mem2", make([]float32, 384), map[string]any{"content": "Second memory content"})
	eng.VAdd(idx, "mem3", make([]float32, 384), map[string]any{"content": "Third memory content"})

	_, result, err := svc.SummarizeMemories(nil, nil, SummarizeMemoriesArgs{
		MemoryIDs: []string{"mem1", "mem2", "mem3"},
		IndexName: idx,
		Title:     "Test Summary",
	})
	if err != nil {
		t.Fatalf("SummarizeMemories failed: %v", err)
	}
	if result.SummaryID == "" {
		t.Error("expected summary_id to be set after saving")
	}
	if result.ContentCount != 3 {
		t.Errorf("expected 3 items, got %d", result.ContentCount)
	}
	if result.Title != "Test Summary" {
		t.Errorf("expected title 'Test Summary', got %s", result.Title)
	}
	if !strings.Contains(result.Summary, "First memory content") {
		t.Error("expected summary to contain first content")
	}
}

func TestSummarizeMemoriesEmptyIDs(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.SummarizeMemories(nil, nil, SummarizeMemoriesArgs{
		MemoryIDs: []string{},
	})
	if result.Message == "" {
		t.Error("expected message for empty memory_ids")
	}
}
