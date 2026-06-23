package mcp

import (
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/compiler"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// newTestServiceMinimal creates a Service with no compiler/gardener for tests
// of tools that don't need them (get_memory, delete_memory, etc).
func newTestServiceMinimal(t *testing.T) (*Service, *engine.Engine) {
	t.Helper()
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	// Index dim must match the mock embedder's output (384).
	eng.VCreate("mcp_memory", distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil)

	embedder := &mockEmbedder{}
	svc := NewService(eng, embedder, nil, nil)

	return svc, eng
}

func TestGetMemory(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "mem1", make([]float32, 384), map[string]any{
		"content": "hello world",
		"type":    "memory",
	})

	// Found case
	_, result, err := svc.GetMemory(nil, nil, GetMemoryArgs{
		MemoryID:  "mem1",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("GetMemory failed: %v", err)
	}
	if !result.Found {
		t.Error("expected found=true")
	}
	if result.MemoryID != "mem1" {
		t.Errorf("expected mem1, got %s", result.MemoryID)
	}
	if result.Metadata["content"] != "hello world" {
		t.Errorf("expected content 'hello world', got %v", result.Metadata["content"])
	}

	// Not-found case
	_, result2, _ := svc.GetMemory(nil, nil, GetMemoryArgs{
		MemoryID:  "nonexistent",
		IndexName: idx,
	})
	if result2.Found {
		t.Error("expected found=false for nonexistent memory")
	}
}

func TestGetMemories(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "a", make([]float32, 384), map[string]any{"content": "A"})
	eng.VAdd(idx, "b", make([]float32, 384), map[string]any{"content": "B"})
	eng.VAdd(idx, "c", make([]float32, 384), map[string]any{"content": "C"})

	_, result, err := svc.GetMemories(nil, nil, GetMemoriesArgs{
		MemoryIDs: []string{"a", "b", "nonexistent", "c"},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("GetMemories failed: %v", err)
	}
	if result.Found != 3 {
		t.Errorf("expected 3 found, got %d", result.Found)
	}
	if len(result.Memories) != 4 {
		t.Errorf("expected 4 entries, got %d", len(result.Memories))
	}
	// Verify a "nonexistent" entry is somewhere in the result with Found=false.
	foundNotFound := false
	for _, m := range result.Memories {
		if m.MemoryID == "nonexistent" {
			if m.Found {
				t.Error("expected Found=false for nonexistent memory")
			}
			foundNotFound = true
		}
	}
	if !foundNotFound {
		t.Error("nonexistent entry not present in result")
	}
}

func TestDeleteMemory(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "to_delete", make([]float32, 384), map[string]any{"content": "bye"})

	// Soft delete
	_, result, err := svc.DeleteMemory(nil, nil, DeleteMemoryArgs{
		MemoryID:  "to_delete",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("DeleteMemory failed: %v", err)
	}
	if result.Status != "soft_deleted" {
		t.Errorf("expected soft_deleted, got %s", result.Status)
	}
	if result.HardDelete {
		t.Error("expected HardDelete=false")
	}
}

func TestUnlinkEntities(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "src", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "tgt", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VLink(idx, "src", "tgt", "mentions", "mentioned_by", 1.0, nil)

	_, result, err := svc.UnlinkEntities(nil, nil, UnlinkEntitiesArgs{
		SourceID:  "src",
		TargetID:  "tgt",
		Relation:  "mentions",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("UnlinkEntities failed: %v", err)
	}
	if result.Status != "unlinked" {
		t.Errorf("expected unlinked, got %s", result.Status)
	}
}

func TestListTemplates(t *testing.T) {
	// With compiler
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	defer eng.Close()
	eng.VCreate("mcp_memory", distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil)

	embedder := &mockEmbedder{}
	comp := compiler.NewCompiler(eng, nil, embedder)
	svc := NewService(eng, embedder, comp, nil)

	_, result, err := svc.ListTemplates(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}
	if len(result.Templates) == 0 {
		t.Error("expected at least one built-in template")
	}
	// Verify entity_card is in the list (it's a well-known template).
	found := false
	for _, tpl := range result.Templates {
		if tpl.Name == "entity_card" {
			found = true
			if !tpl.Deterministic {
				t.Error("entity_card should be deterministic")
			}
		}
	}
	if !found {
		t.Error("entity_card template not found")
	}
}

func TestListTemplatesNoCompiler(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.ListTemplates(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("ListTemplates failed: %v", err)
	}
	if len(result.Templates) != 0 {
		t.Errorf("expected empty templates without compiler, got %d", len(result.Templates))
	}
}

func TestGetArtifactStalenessNotFound(t *testing.T) {
	svc, _, comp := newTestServiceWithCompiler(t)
	_ = comp
	_, result, err := svc.GetArtifactStaleness(nil, nil, GetArtifactStalenessArgs{
		Intent: "user_profile",
		Entity: "nonexistent",
	})
	if err != nil {
		t.Fatalf("GetArtifactStaleness failed: %v", err)
	}
	if result.Found {
		t.Error("expected Found=false for nonexistent artifact")
	}
	if result.Message == "" {
		t.Error("expected message for not-found case")
	}
}

// TestGetArtifactHistoryEmptyIndex verifies that the handler returns
// a graceful empty result (not an error) when index_name is empty
// (defaults to mcp_memory) and no artifacts exist. Regression test for
// the bug where empty index_name caused "index not found" to leak from
// the engine layer.
func TestGetArtifactHistoryEmptyIndex(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent: "user_profile",
		Entity: "nonexistent",
		// IndexName intentionally omitted
	})
	if err != nil {
		t.Fatalf("GetArtifactHistory returned error (should be graceful): %v", err)
	}
	if result.TotalVersions != 0 {
		t.Errorf("expected 0 versions, got %d", result.TotalVersions)
	}
	if result.Message == "" {
		t.Error("expected message for empty case")
	}
}

// TestGetArtifactHistoryNonexistentIndex verifies that the handler
// returns a friendly error message when index_name doesn't exist.
func TestGetArtifactHistoryNonexistentIndex(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent:     "user_profile",
		Entity:     "alice",
		IndexName:  "nonexistent_index",
	})
	if err != nil {
		t.Fatalf("GetArtifactHistory returned error (should be graceful): %v", err)
	}
	if !strings.Contains(result.Message, "nonexistent_index") {
		t.Errorf("expected message to mention index name, got: %s", result.Message)
	}
	if result.TotalVersions != 0 {
		t.Errorf("expected 0 versions for nonexistent index, got %d", result.TotalVersions)
	}
}

// TestGetArtifactStalenessEmptyIndex verifies the same fix for
// GetArtifactStaleness (which had the same latent bug masked by
// the generic "artifact not found" error path).
func TestGetArtifactStalenessEmptyIndex(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.GetArtifactStaleness(nil, nil, GetArtifactStalenessArgs{
		Intent: "user_profile",
		Entity: "nonexistent",
		// IndexName intentionally omitted
	})
	if err != nil {
		t.Fatalf("GetArtifactStaleness returned error (should be graceful): %v", err)
	}
	if result.Found {
		t.Error("expected Found=false")
	}
	if result.Message == "" {
		t.Error("expected message for empty case")
	}
}

// TestGetArtifactStalenessNonexistentIndex verifies the same fix
// with a nonexistent index_name.
func TestGetArtifactStalenessNonexistentIndex(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.GetArtifactStaleness(nil, nil, GetArtifactStalenessArgs{
		Intent:     "user_profile",
		Entity:     "alice",
		IndexName:  "nonexistent_index",
	})
	if err != nil {
		t.Fatalf("GetArtifactStaleness returned error (should be graceful): %v", err)
	}
	if !strings.Contains(result.Message, "nonexistent_index") {
		t.Errorf("expected message to mention index name, got: %s", result.Message)
	}
	if result.Found {
		t.Error("expected Found=false for nonexistent index")
	}
}

func TestTriggerReflectionNoGardener(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.TriggerReflection(nil, nil, TriggerReflectionArgs{
		IndexName: "mcp_memory",
	})
	if err != nil {
		t.Fatalf("TriggerReflection failed: %v", err)
	}
	if result.Status != "error" {
		t.Errorf("expected error status when gardener is nil, got %s", result.Status)
	}
}

func TestSearchWithScores(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "a", make([]float32, 384), map[string]any{"content": "alpha"})
	eng.VAdd(idx, "b", make([]float32, 384), map[string]any{"content": "beta"})

	_, result, err := svc.SearchWithScores(nil, nil, SearchWithScoresArgs{
		Query:     "anything",
		IndexName: idx,
		K:         5,
	})
	if err != nil {
		t.Fatalf("SearchWithScores failed: %v", err)
	}
	if len(result.Results) == 0 {
		t.Error("expected at least one result")
	}
	// All results should have a score in [0, 1]
	for _, r := range result.Results {
		if r.Score < 0 || r.Score > 1 {
			t.Errorf("score out of [0,1]: %f", r.Score)
		}
	}
}

func TestListIndexes(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	eng.VCreate("other_idx", distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil)

	_, result, err := svc.ListIndexes(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("ListIndexes failed: %v", err)
	}
	if len(result.Indexes) < 2 {
		t.Errorf("expected at least 2 indexes, got %d", len(result.Indexes))
	}
	// Verify mcp_memory and other_idx are both in the list.
	names := make(map[string]bool, len(result.Indexes))
	for _, info := range result.Indexes {
		names[info.Name] = true
	}
	if !names["mcp_memory"] {
		t.Error("mcp_memory not in list")
	}
	if !names["other_idx"] {
		t.Error("other_idx not in list")
	}
}

func TestListIndexesNoIndexes(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	defer eng.Close()
	embedder := &mockEmbedder{}
	svc := NewService(eng, embedder, nil, nil)

	_, result, err := svc.ListIndexes(nil, nil, struct{}{})
	if err != nil {
		t.Fatalf("ListIndexes failed: %v", err)
	}
	if len(result.Indexes) != 0 {
		t.Errorf("expected 0 indexes, got %d", len(result.Indexes))
	}
}
