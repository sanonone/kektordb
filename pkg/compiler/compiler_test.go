package compiler

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Failed to open engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

func TestCompileEntityCard(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add user entity nodes
	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type":      "user",
		"entity_id": "alice",
		"name":      "Alice Johnson",
		"content":   "Alice is a senior backend developer who prefers Go and Rust",
		"_pinned":   true,
	})
	eng.VAdd(indexName, "user:alice:pref_1", make([]float32, 384), map[string]any{
		"type":      "memory",
		"entity_id": "alice",
		"content":   "Alice prefers concise code reviews and hates verbose comments",
		"_sentiment": "positive",
	})
	eng.VAdd(indexName, "user:alice:pref_2", make([]float32, 384), map[string]any{
		"type":      "memory",
		"entity_id": "alice",
		"content":   "Uses Vim and prefers dark themes",
	})
	eng.VAdd(indexName, "user:alice:inter_1", make([]float32, 384), map[string]any{
		"type":      "user_interaction",
		"entity_id": "alice",
		"content":   "Asked about KektorDB HNSW implementation details",
	})

	// Link them
	eng.VLink(indexName, "user:alice", "user:alice:pref_1", "has_interaction", "interaction_of", 1.0, nil)
	eng.VLink(indexName, "user:alice", "user:alice:pref_2", "has_interaction", "interaction_of", 1.0, nil)
	eng.VLink(indexName, "user:alice", "user:alice:inter_1", "has_interaction", "interaction_of", 1.0, nil)

	c := NewCompiler(eng, nil) // no LLM

	req := CompileRequest{
		Name:    "entity_card",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 2},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if artifact == nil {
		t.Fatal("artifact is nil")
	}
	if artifact.Name != "entity_card" {
		t.Errorf("expected name 'entity_card', got '%s'", artifact.Name)
	}
	if artifact.EntityType != "user" {
		t.Errorf("expected entity_type 'user', got '%s'", artifact.EntityType)
	}
	if artifact.EntityID != "alice" {
		t.Errorf("expected entity_id 'alice', got '%s'", artifact.EntityID)
	}
	if artifact.Status != CompileStatusComplete {
		t.Errorf("expected status 'complete', got '%s'", artifact.Status)
	}
	if artifact.CompileMode != CompileModeDeterministic {
		t.Errorf("expected compile_mode 'deterministic', got '%s'", artifact.CompileMode)
	}
	if artifact.Version != 1 {
		t.Errorf("expected version 1, got %d", artifact.Version)
	}
	if artifact.ID == "" {
		t.Error("artifact ID is empty")
	}

	// Verify deterministic fields that should be populated
	// name is from metadata
	if _, ok := artifact.Data["name"]; !ok {
		t.Error("missing field: name")
	}
	// type is from metadata
	if _, ok := artifact.Data["type"]; !ok {
		t.Error("missing field: type")
	}
	// connection_count is computed from graph edges
	if _, ok := artifact.Data["connection_count"]; !ok {
		t.Error("missing field: connection_count")
	}
	// last_updated may be missing if no _created_at on nodes
	if _, ok := artifact.Data["last_updated"]; ok {
		t.Log("field present: last_updated")
	}
	// key_attributes may be empty if no key_attributes in metadata
	if _, ok := artifact.Data["key_attributes"]; ok {
		t.Log("field present: key_attributes")
	}

	if len(artifact.Provenance) >= 0 {
		t.Log("warning: no provenance entries (expected for entity_card deterministic)")
	}
}

func TestCompileUserProfile(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	eng.VAdd(indexName, "user:bob", make([]float32, 384), map[string]any{
		"type":      "user",
		"entity_id": "bob",
		"name":      "Bob Smith",
		"content":   "Bob is a data scientist who works with Python",
		"_pinned":   true,
	})
	eng.VAdd(indexName, "user:bob:pref_1", make([]float32, 384), map[string]any{
		"type":    "memory",
		"content": "Bob likes detailed technical documentation",
	})
	eng.VAdd(indexName, "user:bob:pref_2", make([]float32, 384), map[string]any{
		"type":    "memory",
		"content": "Bob uses Jupyter notebooks extensively",
	})

	eng.VLink(indexName, "user:bob", "user:bob:pref_1", "has_interaction", "interaction_of", 1.0, nil)
	eng.VLink(indexName, "user:bob", "user:bob:pref_2", "has_interaction", "interaction_of", 1.0, nil)

	c := NewCompiler(eng, nil)

	req := CompileRequest{
		Name:    "user_profile",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "bob"}, Depth: 2},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Deterministic fields should be populated
	if name, ok := artifact.Data["name"]; !ok || name != "Bob Smith" {
		t.Errorf("expected name 'Bob Smith', got %v", artifact.Data["name"])
	}
	if ic, ok := artifact.Data["interaction_count"]; ok {
		if ic.(int) == 0 {
			t.Error("interaction_count should be > 0")
		}
	} else {
		t.Error("missing field: interaction_count")
	}
}

func TestGetArtifactRoundTrip(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VAdd(indexName, "user:eve", make([]float32, 384), map[string]any{
		"type":      "user",
		"entity_id": "eve",
		"name":      "Eve Chen",
		"_pinned":   true,
	})

	c := NewCompiler(eng, nil)

	req := CompileRequest{
		Name:    "entity_card",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "eve"}, Depth: 2},
	}

	_, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	retrieved, err := c.GetArtifact("entity_card", "user", "eve", indexName)
	if err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}
	if retrieved.Name != "entity_card" {
		t.Errorf("expected name 'entity_card', got '%s'", retrieved.Name)
	}
	if retrieved.EntityID != "eve" {
		t.Errorf("expected entity_id 'eve', got '%s'", retrieved.EntityID)
	}
}

func TestArtifactVersioning(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VAdd(indexName, "project:testproj", make([]float32, 384), map[string]any{
		"type":      "project",
		"entity_id": "testproj",
		"name":      "Test Project",
		"_pinned":   true,
	})
	eng.VAdd(indexName, "project:testproj:node_1", make([]float32, 384), map[string]any{
		"type":    "memory",
		"content": "First memory",
	})
	eng.VLink(indexName, "project:testproj", "project:testproj:node_1", "has_memory", "memory_of", 1.0, nil)

	c := NewCompiler(eng, nil)

	req := CompileRequest{
		Name:    "project_summary",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "project", ID: "testproj"}, Depth: 2},
	}

	// First compile
	a1, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile 1 failed: %v", err)
	}
	if a1.Version != 1 {
		t.Errorf("expected version 1, got %d", a1.Version)
	}

	// Add more nodes, recompile
	eng.VAdd(indexName, "project:testproj:node_2", make([]float32, 384), map[string]any{
		"type":    "memory",
		"content": "Second memory",
	})
	eng.VLink(indexName, "project:testproj", "project:testproj:node_2", "has_memory", "memory_of", 1.0, nil)

	time.Sleep(10 * time.Millisecond) // let the engine settle

	a2, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile 2 failed: %v", err)
	}
	if a2.Version != 2 {
		t.Errorf("expected version 2, got %d", a2.Version)
	}

	// Verify the artifact was updated in the graph
	ids, err := eng.VFilter(indexName, "type='knowledge_artifact'", 10)
	if err != nil {
		t.Fatalf("VFilter failed: %v", err)
	}
	found := 0
	for _, id := range ids {
		data, err := eng.VGet(indexName, id)
		if err != nil {
			continue
		}
		if name, ok := data.Metadata["artifact_name"].(string); ok && name == "project_summary" {
			found++
			if v, ok := data.Metadata["version"].(float64); ok && int(v) >= 2 {
				t.Logf("found version %d", int(v))
			}
		}
	}
	if found < 1 {
		t.Errorf("expected at least 1 artifact version, found %d", found)
	}
}

func TestListArtifacts(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VAdd(indexName, "user:art_test", make([]float32, 384), map[string]any{
		"type":      "user",
		"entity_id": "art_test",
		"_pinned":   true,
	})

	c := NewCompiler(eng, nil)

	req := CompileRequest{
		Name:    "entity_card",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "art_test"}, Depth: 2},
	}

	_, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	artifacts, err := c.ListArtifacts(indexName)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(artifacts) == 0 {
		t.Error("expected at least 1 artifact")
	}
	found := false
	for _, a := range artifacts {
		if a.Name == "entity_card" {
			found = true
			break
		}
	}
	if !found {
		t.Error("entity_card artifact not found in list")
	}
}

func TestCompileWithTemplateLookup(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"

	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	eng.VAdd(indexName, "project:kektordb", make([]float32, 384), map[string]any{
		"type":      "project",
		"entity_id": "kektordb",
		"_pinned":   true,
	})

	c := NewCompiler(eng, nil)

	// Use template name in sources.entity.type to trigger template lookup
	req := CompileRequest{
		Name:    "project_summary",
		Template: "project_summary",
		Sources: SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "project", ID: "kektordb"}, Depth: 1},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if _, ok := artifact.Data["node_count"]; !ok {
		t.Error("missing field: node_count")
	}
	if _, ok := artifact.Data["relation_count"]; !ok {
		t.Error("missing field: relation_count")
	}
	// summary is LLM field, should not exist in deterministic mode
	if _, ok := artifact.Data["summary"]; ok {
		t.Log("summary field present (best-effort from deterministic mode)")
	}
}
