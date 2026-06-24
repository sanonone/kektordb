package mcp

import (
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// =============================================================================
// Fase 3 P2: 6 final tools closing the MCP interface
// =============================================================================

// -----------------------------------------------------------------------------
// find_path
// -----------------------------------------------------------------------------

func TestFindPathHappyPath(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	// Build a small chain: a -> b -> c
	eng.VAdd(idx, "a", makeVec(0.1), map[string]any{"content": "a"})
	eng.VAdd(idx, "b", makeVec(0.2), map[string]any{"content": "b"})
	eng.VAdd(idx, "c", makeVec(0.3), map[string]any{"content": "c"})
	eng.VLink(idx, "a", "b", "related_to", "related_to", 1.0, nil)
	eng.VLink(idx, "b", "c", "related_to", "related_to", 1.0, nil)

	_, result, err := svc.FindPath(nil, nil, FindPathArgs{
		SourceID:  "a",
		TargetID:  "c",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("FindPath failed: %v", err)
	}
	if !result.Found {
		t.Errorf("expected path to be found, msg=%s", result.Message)
	}
	if len(result.Path) != 3 {
		t.Errorf("expected path length 3, got %d (%v)", len(result.Path), result.Path)
	}
	if result.StepCount != 2 {
		t.Errorf("expected step_count 2, got %d", result.StepCount)
	}
}

func TestFindPathSameNode(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.FindPath(nil, nil, FindPathArgs{
		SourceID:  "x",
		TargetID:  "x",
		IndexName: "mcp_memory",
	})
	if err != nil {
		t.Fatalf("FindPath failed: %v", err)
	}
	if !result.Found {
		t.Errorf("expected found=true for src==tgt")
	}
	if result.Message != "source equals target" {
		t.Errorf("expected message 'source equals target', got '%s'", result.Message)
	}
}

func TestFindPathNoPath(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.FindPath(nil, nil, FindPathArgs{
		SourceID:  "ghost1",
		TargetID:  "ghost2",
		IndexName: "mcp_memory",
		MaxDepth:  3,
	})
	if err != nil {
		t.Fatalf("FindPath failed: %v", err)
	}
	if result.Found {
		t.Errorf("expected not found, got result=%+v", result)
	}
	if result.Message == "" {
		t.Errorf("expected message describing no path")
	}
}

func TestFindPathEmptySourceID(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.FindPath(nil, nil, FindPathArgs{
		TargetID:  "x",
		IndexName: "mcp_memory",
	})
	if !strings.Contains(result.Message, "source_id") {
		t.Errorf("expected source_id required message, got '%s'", result.Message)
	}
}

func TestFindPathDepthClamp(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	// max_depth=100 should be clamped internally to 10; we verify that
	// the call doesn't error out and returns a clean result.
	_, result, err := svc.FindPath(nil, nil, FindPathArgs{
		SourceID:  "ghost1",
		TargetID:  "ghost2",
		IndexName: "mcp_memory",
		MaxDepth:  100,
	})
	if err != nil {
		t.Fatalf("FindPath failed: %v", err)
	}
	if result.Found {
		t.Errorf("expected not found")
	}
}

// -----------------------------------------------------------------------------
// reinforce_memory
// -----------------------------------------------------------------------------

func TestReinforceMemoryHappyPath(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "mem1", makeVec(0.1), map[string]any{"type": "memory"})
	eng.VAdd(idx, "mem2", makeVec(0.2), map[string]any{"type": "memory"})

	_, result, err := svc.ReinforceMemory(nil, nil, ReinforceMemoryArgs{
		MemoryIDs: []string{"mem1", "mem2"},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("ReinforceMemory failed: %v", err)
	}
	if result.Requested != 2 {
		t.Errorf("expected requested=2, got %d", result.Requested)
	}
	if result.Reinforced != 2 {
		t.Errorf("expected reinforced=2, got %d", result.Reinforced)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("expected no skipped, got %v", result.Skipped)
	}
}

func TestReinforceMemoryEmptyIDs(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.ReinforceMemory(nil, nil, ReinforceMemoryArgs{
		MemoryIDs: []string{},
		IndexName: "mcp_memory",
	})
	if !strings.Contains(result.Message, "memory_ids") {
		t.Errorf("expected memory_ids required message, got '%s'", result.Message)
	}
}

func TestReinforceMemorySkippedMissing(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "real", makeVec(0.1), map[string]any{"type": "memory"})

	_, result, err := svc.ReinforceMemory(nil, nil, ReinforceMemoryArgs{
		MemoryIDs: []string{"real", "ghost1", "ghost2"},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("ReinforceMemory failed: %v", err)
	}
	if result.Requested != 3 {
		t.Errorf("expected requested=3, got %d", result.Requested)
	}
	if result.Reinforced != 1 {
		t.Errorf("expected reinforced=1, got %d", result.Reinforced)
	}
	if len(result.Skipped) != 2 {
		t.Errorf("expected 2 skipped, got %d (%v)", len(result.Skipped), result.Skipped)
	}
}

func TestReinforceMemoryDedupe(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "mem1", makeVec(0.1), map[string]any{"type": "memory"})

	_, result, _ := svc.ReinforceMemory(nil, nil, ReinforceMemoryArgs{
		MemoryIDs: []string{"mem1", "mem1", "mem1"},
		IndexName: idx,
	})
	if result.Requested != 1 {
		t.Errorf("expected dedupe to 1, got requested=%d", result.Requested)
	}
}

func TestReinforceMemoryIndexNotFound(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.ReinforceMemory(nil, nil, ReinforceMemoryArgs{
		MemoryIDs: []string{"x"},
		IndexName: "nope_idx",
	})
	if !strings.Contains(result.Message, "not found") {
		t.Errorf("expected not found message, got '%s'", result.Message)
	}
}

// -----------------------------------------------------------------------------
// list_sessions
// -----------------------------------------------------------------------------

func TestListSessionsEmpty(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	_, result, err := svc.ListSessions(nil, nil, ListSessionsArgs{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 sessions, got %d", result.Total)
	}
	if len(result.Sessions) != 0 {
		t.Errorf("expected empty slice, got %d items", len(result.Sessions))
	}
}

func TestListSessionsMultiple(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	// Start 3 sessions
	_, r1, _ := svc.StartSession(nil, nil, StartSessionArgs{UserID: "alice", AgentID: "a1"})
	_, r2, _ := svc.StartSession(nil, nil, StartSessionArgs{UserID: "bob", AgentID: "a2"})
	_, r3, _ := svc.StartSession(nil, nil, StartSessionArgs{UserID: "alice", AgentID: "a3"})
	_ = r3
	_ = r1
	_ = r2

	_, result, err := svc.ListSessions(nil, nil, ListSessionsArgs{})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("expected 3 sessions, got %d", result.Total)
	}
}

func TestListSessionsUserFilter(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	svc.StartSession(nil, nil, StartSessionArgs{UserID: "alice", AgentID: "a1"})
	svc.StartSession(nil, nil, StartSessionArgs{UserID: "bob", AgentID: "a2"})
	svc.StartSession(nil, nil, StartSessionArgs{UserID: "alice", AgentID: "a3"})

	_, result, err := svc.ListSessions(nil, nil, ListSessionsArgs{UserID: "alice"})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected 2 sessions for alice, got %d", result.Total)
	}
	for _, s := range result.Sessions {
		if s.UserID != "alice" {
			t.Errorf("filter leak: session with user_id=%s returned", s.UserID)
		}
	}
}

// -----------------------------------------------------------------------------
// create_index
// -----------------------------------------------------------------------------

func TestCreateIndexHappyPath(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	_, result, err := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "test_idx",
		Metric:    "cosine",
		Dimension: 384,
	})
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}
	if result.Status != "created" {
		t.Errorf("expected status=created, got '%s' (msg=%s)", result.Status, result.Message)
	}
	if !eng.IndexExists("test_idx") {
		t.Errorf("index 'test_idx' should exist after creation")
	}
}

func TestCreateIndexEmptyName(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Metric:    "cosine",
		Dimension: 384,
	})
	if !strings.Contains(result.Message, "name is required") {
		t.Errorf("expected name required message, got '%s'", result.Message)
	}
}

func TestCreateIndexInvalidName(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "bad name with spaces!",
		Metric:    "cosine",
		Dimension: 384,
	})
	if !strings.Contains(result.Message, "invalid name") {
		t.Errorf("expected invalid name message, got '%s'", result.Message)
	}
}

func TestCreateIndexInvalidMetric(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "good_name",
		Metric:    "hamming",
		Dimension: 384,
	})
	if !strings.Contains(result.Message, "invalid metric") {
		t.Errorf("expected invalid metric message, got '%s'", result.Message)
	}
}

func TestCreateIndexInvalidDimension(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "good_name",
		Metric:    "cosine",
		Dimension: 0,
	})
	if !strings.Contains(result.Message, "dimension") {
		t.Errorf("expected dimension error, got '%s'", result.Message)
	}
}

func TestCreateIndexAlreadyExists(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	// mcp_memory is created by newTestServiceMinimal
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "mcp_memory",
		Metric:    "cosine",
		Dimension: 384,
	})
	if result.Status != "exists" {
		t.Errorf("expected status=exists, got '%s' (msg=%s)", result.Status, result.Message)
	}
}

func TestCreateIndexDimMismatch(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	// mockEmbedder returns 384-dim vectors; ask for 768
	_, result, _ := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "wrong_dim",
		Metric:    "cosine",
		Dimension: 768,
	})
	if !strings.Contains(result.Message, "conflicts with embedder") {
		t.Errorf("expected embedder dim conflict, got '%s'", result.Message)
	}
}

func TestCreateIndexEuclidean(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	_, result, err := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "eucl_idx",
		Metric:    "euclidean",
		Dimension: 384,
	})
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}
	if result.Status != "created" {
		t.Errorf("expected status=created, got '%s'", result.Status)
	}
	if result.Metric != "euclidean" {
		t.Errorf("expected metric=euclidean, got '%s'", result.Metric)
	}
	if !eng.IndexExists("eucl_idx") {
		t.Errorf("eucl_idx should exist")
	}
}

func TestCreateIndexWithAutoLink(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	_, result, err := svc.CreateIndex(nil, nil, CreateIndexArgs{
		Name:      "linked_idx",
		Metric:    "cosine",
		Dimension: 384,
		AutoLinks: []AutoLinkRuleInput{
			{Field: "category", Relation: "belongs_to", CreateNode: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}
	if result.Status != "created" {
		t.Errorf("expected status=created, got '%s' (msg=%s)", result.Status, result.Message)
	}
	if !eng.IndexExists("linked_idx") {
		t.Errorf("linked_idx should exist")
	}
}

// -----------------------------------------------------------------------------
// delete_index
// -----------------------------------------------------------------------------

func TestDeleteIndexPreview(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	// mcp_memory exists; ask for preview (confirm=false)
	_, result, err := svc.DeleteIndex(nil, nil, DeleteIndexArgs{
		Name:    "mcp_memory",
		Confirm: false,
	})
	if err != nil {
		t.Fatalf("DeleteIndex failed: %v", err)
	}
	if result.Status != "preview" {
		t.Errorf("expected status=preview, got '%s' (msg=%s)", result.Status, result.Message)
	}
	if result.ArenaPath == "" {
		t.Errorf("expected non-empty arena path in preview")
	}
	// Index must still exist after preview
	if !svc.engine.IndexExists("mcp_memory") {
		t.Errorf("preview must not delete the index")
	}
}

func TestDeleteIndexConfirm(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	// Create a new index to delete
	eng.VCreate("to_delete", distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil)
	// Add a vector so preview shows count > 0
	eng.VAdd("to_delete", "v1", makeVec(0.1), map[string]any{"content": "x"})

	_, result, err := svc.DeleteIndex(nil, nil, DeleteIndexArgs{
		Name:    "to_delete",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("DeleteIndex failed: %v", err)
	}
	if result.Status != "deleted" {
		t.Errorf("expected status=deleted, got '%s' (msg=%s)", result.Status, result.Message)
	}
	if eng.IndexExists("to_delete") {
		t.Errorf("index 'to_delete' should not exist after delete")
	}
}

func TestDeleteIndexNotFound(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.DeleteIndex(nil, nil, DeleteIndexArgs{
		Name:    "ghost_idx",
		Confirm: true,
	})
	if result.Status != "not_found" {
		t.Errorf("expected status=not_found, got '%s' (msg=%s)", result.Status, result.Message)
	}
}

func TestDeleteIndexEmptyName(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.DeleteIndex(nil, nil, DeleteIndexArgs{
		Name: "",
	})
	if !strings.Contains(result.Message, "name is required") {
		t.Errorf("expected name required message, got '%s'", result.Message)
	}
}

// -----------------------------------------------------------------------------
// extract_subgraph
// -----------------------------------------------------------------------------

func TestExtractSubgraphHappyPath(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	// Build a small graph: root - mid - leaf
	eng.VAdd(idx, "root", makeVec(0.1), map[string]any{"content": "root"})
	eng.VAdd(idx, "mid", makeVec(0.2), map[string]any{"content": "mid"})
	eng.VAdd(idx, "leaf", makeVec(0.3), map[string]any{"content": "leaf"})
	eng.VLink(idx, "root", "mid", "related_to", "related_to", 1.0, nil)
	eng.VLink(idx, "mid", "leaf", "related_to", "related_to", 1.0, nil)

	_, result, err := svc.ExtractSubgraph(nil, nil, ExtractSubgraphArgs{
		RootID:    "root",
		IndexName: idx,
		Depth:     2,
	})
	if err != nil {
		t.Fatalf("ExtractSubgraph failed: %v", err)
	}
	if result.NodeCount != 3 {
		t.Errorf("expected 3 nodes, got %d", result.NodeCount)
	}
	if result.EdgeCount < 2 {
		t.Errorf("expected at least 2 edges, got %d", result.EdgeCount)
	}
	if result.Depth != 2 {
		t.Errorf("expected depth=2, got %d", result.Depth)
	}
}

func TestExtractSubgraphEmptyRootID(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.ExtractSubgraph(nil, nil, ExtractSubgraphArgs{
		RootID: "",
	})
	if !strings.Contains(result.Message, "root_id is required") {
		t.Errorf("expected root_id required message, got '%s'", result.Message)
	}
}

func TestExtractSubgraphDepthClamp(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "root", makeVec(0.1), map[string]any{"content": "root"})

	// Depth 100 should be clamped to 5 internally; no error
	_, result, err := svc.ExtractSubgraph(nil, nil, ExtractSubgraphArgs{
		RootID:    "root",
		IndexName: idx,
		Depth:     100,
	})
	if err != nil {
		t.Fatalf("ExtractSubgraph failed: %v", err)
	}
	if result.NodeCount != 1 {
		t.Errorf("expected 1 node (just root), got %d", result.NodeCount)
	}
}

func TestExtractSubgraphIndexNotFound(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.ExtractSubgraph(nil, nil, ExtractSubgraphArgs{
		RootID:    "x",
		IndexName: "ghost",
	})
	if !strings.Contains(result.Message, "not found") {
		t.Errorf("expected not found message, got '%s'", result.Message)
	}
}

func TestExtractSubgraphRootNotFound(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, _ := svc.ExtractSubgraph(nil, nil, ExtractSubgraphArgs{
		RootID:    "ghost",
		IndexName: "mcp_memory",
		Depth:     2,
	})
	// Engine behavior: returns the root as a node even if its metadata is empty.
	// We verify that edges=0 (no neighbors reachable from a non-existent node).
	if result.EdgeCount != 0 {
		t.Errorf("expected 0 edges for non-existent root, got %d", result.EdgeCount)
	}
}

// makeVec builds a deterministic 384-dim vector for tests.
func makeVec(seed float32) []float32 {
	v := make([]float32, 384)
	for i := range v {
		v[i] = seed
	}
	return v
}
