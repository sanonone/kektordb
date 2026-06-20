package mcp

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/compiler"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func newTestServiceWithCompiler(t *testing.T) (*Service, *engine.Engine, *compiler.Compiler) {
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

	eng.VCreate("mcp_memory", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	embedder := &mockEmbedder{}
	comp := compiler.NewCompiler(eng, nil, embedder)
	svc := NewService(eng, embedder, comp, nil)

	return svc, eng, comp
}

func TestRequestKnowledgeCacheHit(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	indexName := "mcp_memory"

	// Add a user entity
	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice Johnson", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice prefers concise code",
	})
	eng.VLink(indexName, "user:alice", "user:alice:mem1", "has_interaction", "interaction_of", 1.0, nil)

	// Pre-compile an entity_card
	_, err := comp.Compile(compiler.CompileRequest{
		Name: "entity_card",
		Sources: compiler.SourceSpec{
			Type:   "graph_query",
			Entity: compiler.EntityRef{Type: "user", ID: "alice"},
			Depth:  2,
		},
		IndexName: indexName,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Request knowledge
	_, result, err := svc.RequestKnowledge(nil, nil, RequestKnowledgeArgs{
		Intent:     "entity_card",
		Entity:     "alice",
		EntityType: "user",
	})
	if err != nil {
		t.Fatalf("RequestKnowledge failed: %v", err)
	}

	if !result.Found {
		t.Error("expected found=true for pre-compiled artifact")
	}
	if result.ArtifactName != "entity_card" {
		t.Errorf("expected entity_card, got %s", result.ArtifactName)
	}
	if result.Status != "complete" {
		t.Errorf("expected status complete, got %s", result.Status)
	}
	if result.Data == nil {
		t.Error("data should not be nil")
	}
}

func TestRequestKnowledgeCacheMiss(t *testing.T) {
	svc, eng, _ := newTestServiceWithCompiler(t)
	indexName := "mcp_memory"

	// Add some memory nodes (no pre-compiled artifact)
	eng.VAdd(indexName, "user:bob", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "bob", "name": "Bob Smith", "_pinned": true,
	})
	eng.VAdd(indexName, "user:bob:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Bob uses Python and Jupyter",
	})
	eng.VLink(indexName, "user:bob", "user:bob:mem1", "has_interaction", "interaction_of", 1.0, nil)

	// Request knowledge (no artifact exists)
	_, result, err := svc.RequestKnowledge(nil, nil, RequestKnowledgeArgs{
		Intent:     "user_profile",
		Entity:     "bob",
		EntityType: "user",
	})
	if err != nil {
		t.Fatalf("RequestKnowledge failed: %v", err)
	}

	if result.Found {
		t.Error("expected found=false for missing artifact")
	}
	if result.Status != "not_found" {
		t.Errorf("expected status not_found, got %s", result.Status)
	}
	if len(result.FallbackResults) == 0 {
		t.Error("expected fallback results")
	}
}

func TestRequestKnowledgeNoCompiler(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	defer eng.Close()

	eng.VCreate("mcp_memory", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	svc := NewService(eng, &mockEmbedder{}, nil, nil) // no compiler

	eng.VAdd("mcp_memory", "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "_pinned": true,
	})

	_, result, err := svc.RequestKnowledge(nil, nil, RequestKnowledgeArgs{
		Intent:     "entity_card",
		Entity:     "alice",
		EntityType: "user",
	})
	if err != nil {
		t.Fatalf("RequestKnowledge should not error without compiler: %v", err)
	}

	if result.Found {
		t.Error("expected found=false without compiler")
	}
}

func TestRequestKnowledgeEntityAutoDetection(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:charlie", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "charlie", "name": "Charlie", "_pinned": true,
	})

	// Compile with entity_type=user
	_, err := comp.Compile(compiler.CompileRequest{
		Name: "entity_card",
		Sources: compiler.SourceSpec{
			Type:   "graph_query",
			Entity: compiler.EntityRef{Type: "user", ID: "charlie"},
			Depth:  1,
		},
		IndexName: indexName,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Request with colon-prefixed entity (should auto-split into type+id)
	_, result, err := svc.RequestKnowledge(nil, nil, RequestKnowledgeArgs{
		Intent: "entity_card",
		Entity: "user:charlie",
	})
	if err != nil {
		t.Fatalf("RequestKnowledge failed: %v", err)
	}

	if !result.Found {
		t.Error("expected found=true with auto-detected entity type")
	}
}
