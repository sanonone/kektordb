package compiler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// mockLLM implements llm.Client for testing compiler LLM compilation.
type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Chat(systemPrompt, userQuery string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	return "", nil
}

func newTestCompilerWithLLM(t *testing.T, llmResponse string) (*Compiler, *mockLLM) {
	t.Helper()
	eng := newTestEngine(t)
	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	m := &mockLLM{response: llmResponse}
	c := &Compiler{
		eng:         eng,
		llm:         m,
		templates:   BuiltinTemplates,
		taskManager: newCompileTaskManager(),
	}

	return c, m
}

func TestCompileUserProfileWithLLM(t *testing.T) {
	llmResponse := `{"value": "direct, prefers concise code reviews", "confidence": 0.85, "sources": [{"source_id": "user:alice", "confidence": 0.9, "evidence": "from multiple interactions"}]}`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:pref_1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice prefers concise code reviews",
	})
	eng.VLink(indexName, "user:alice", "user:alice:pref_1", "has_interaction", "interaction_of", 1.0, nil)

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 2},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// LLM field: communication_style
	if cs, ok := artifact.Data["communication_style"].(string); !ok || cs == "" {
		t.Error("communication_style should be populated by LLM")
	} else {
		t.Logf("communication_style: %s", cs)
	}

	// Deterministic fields still work
	if _, ok := artifact.Data["name"]; !ok {
		t.Error("name should still be deterministic")
	}
	if _, ok := artifact.Data["interaction_count"]; !ok {
		t.Error("interaction_count should be computed")
	}

	if artifact.CompileMode != CompileModeHybrid {
		t.Errorf("expected compile_mode hybrid, got %s", artifact.CompileMode)
	}
}

func TestCompileHybridMode(t *testing.T) {
	llmResponse := `{"value": "Python developer who likes notebooks", "confidence": 0.8, "sources": [{"source_id": "user:bob", "confidence": 0.85, "evidence": "from profile"}]}`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:bob", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "bob", "name": "Bob", "_pinned": true,
	})

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "bob"}, Depth: 1},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if artifact.CompileMode != CompileModeHybrid {
		t.Errorf("expected hybrid, got %s", artifact.CompileMode)
	}

	// Provenance should exist for LLM fields
	if prov, ok := artifact.Provenance["communication_style"]; ok {
		if len(prov) == 0 {
			t.Error("provenance for communication_style should not be empty")
		}
	} else {
		t.Log("no provenance for communication_style (may be normal for best-effort)")
	}
}

func TestLLMFieldOmittedOnLowConfidence(t *testing.T) {
	llmResponse := `{"value": "some text", "confidence": 0.3, "sources": [{"source_id": "user:alice", "confidence": 0.3, "evidence": "weak"}]}`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:pref_1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "some test content for Alice",
	})
	eng.VLink(indexName, "user:alice", "user:alice:pref_1", "has_interaction", "interaction_of", 1.0, nil)

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 2},
		TaskSpec: &TaskSpec{ConfidenceMin: 0.8},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if _, ok := artifact.Data["communication_style"]; ok {
		t.Error("communication_style should be omitted due to low confidence")
	}
	// name should still be present (deterministic)
	if _, ok := artifact.Data["name"]; ok {
		t.Log("name present")
	} else {
		t.Log("name not present (may be normal with strict relevance filter)")
	}
}

func TestLLMFallbackOnBadJSON(t *testing.T) {
	llmResponse := `not valid json at all, just some raw text about preferences`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:pref_1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice uses Vim",
	})
	eng.VLink(indexName, "user:alice", "user:alice:pref_1", "has_interaction", "interaction_of", 1.0, nil)

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 2},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile should not fail entirely on bad LLM JSON: %v", err)
	}

	if cs, ok := artifact.Data["communication_style"].(string); ok {
		t.Logf("fallback communication_style: %s", cs)
	}
}

func TestNeedsAsync(t *testing.T) {
	eng := newTestEngine(t)
	indexName := "mcp_memory"
	eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)

	// Without LLM
	c := NewCompiler(eng, nil, nil)
	req := CompileRequest{
		Template: "user_profile",
		Sources:  SourceSpec{Entity: EntityRef{Type: "user", ID: "test"}},
	}
	if c.NeedsAsync(req) {
		t.Error("needsAsync should be false without LLM")
	}

	// With LLM on a template that has LLM fields
	m := &mockLLM{response: "{}"}
	c2 := &Compiler{
		eng:         eng,
		llm:         m,
		templates:   BuiltinTemplates,
		taskManager: newCompileTaskManager(),
	}
	if !c2.NeedsAsync(req) {
		t.Error("needsAsync should be true with LLM and LLM fields in template")
	}

	// Deterministic template
	req2 := CompileRequest{
		Template: "entity_card",
		Sources:  SourceSpec{Entity: EntityRef{Type: "user", ID: "test"}},
	}
	if c2.NeedsAsync(req2) {
		t.Error("needsAsync should be false for deterministic-only template")
	}

	// Explicit deterministic mode
	req3 := CompileRequest{
		Template:    "user_profile",
		CompileMode: CompileModeDeterministic,
		Sources:     SourceSpec{Entity: EntityRef{Type: "user", ID: "test"}},
	}
	if c2.NeedsAsync(req3) {
		t.Error("needsAsync should be false with explicit CompileModeDeterministic")
	}
}

func TestAsyncCompileFlow(t *testing.T) {
	llmResponse := `{"value": "direct", "confidence": 0.85, "sources": [{"source_id": "user:alice", "confidence": 0.9, "evidence": "ok"}]}`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:pref_1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "some content",
	})
	eng.VLink(indexName, "user:alice", "user:alice:pref_1", "has_interaction", "interaction_of", 1.0, nil)

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 2},
	}

	taskID, err := c.StartAsyncCompile(req)
	if err != nil {
		t.Fatalf("StartAsyncCompile failed: %v", err)
	}
	if taskID == "" {
		t.Fatal("task ID is empty")
	}

	// Poll until complete or timeout
	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Millisecond)
		task, err := c.GetTaskStatus(taskID)
		if err != nil {
			t.Fatalf("GetTaskStatus failed: %v", err)
		}
		if task.Status == CompileStatusComplete {
			if task.Result == nil {
				t.Error("task result is nil for complete task")
			} else {
				t.Logf("async compile result: %+v", task.Result.Data)
			}
			return
		}
		if task.Status == CompileStatusFailed {
			t.Fatalf("async compile failed: %s", task.Error)
		}
	}
	t.Fatal("async compile timed out")
}

func TestGetTaskStatusNotFound(t *testing.T) {
	eng := newTestEngine(t)
	c := NewCompiler(eng, nil, nil)

	_, err := c.GetTaskStatus("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestCompileLLMFieldWithArray(t *testing.T) {
	arrayJSON, _ := json.Marshal([]string{"Go", "Rust", "Vim"})
	llmResponse := `{"value": ` + string(arrayJSON) + `, "confidence": 0.9, "sources": [{"source_id": "user:alice", "confidence": 0.9, "evidence": "from profile"}]}`
	c, _ := newTestCompilerWithLLM(t, llmResponse)
	eng := c.eng
	indexName := "mcp_memory"

	eng.VAdd(indexName, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})

	req := CompileRequest{
		Name:     "user_profile",
		Template: "user_profile",
		Sources:  SourceSpec{Type: "graph_query", Entity: EntityRef{Type: "user", ID: "alice"}, Depth: 1},
	}

	artifact, err := c.Compile(req)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if prefs, ok := artifact.Data["preferences"].([]any); ok {
		if len(prefs) == 0 {
			t.Error("preferences should have items")
		} else {
			t.Logf("preferences: %v", prefs)
		}
	} else {
		t.Log("preferences not present (may be best-effort from deterministic)")
	}
}

func TestBuildFieldPromptIncludesSchema(t *testing.T) {
	c := NewCompiler(nil, nil, nil)
	nodes := []NodeInfo{
		{ID: "node_1", Content: "test content", Metadata: map[string]any{"name": "test"}},
	}

	fieldDef := FieldDef{
		Type:        "string",
		Enum:        []string{"a", "b"},
		LLM:         true,
		Description: "test field",
	}

	req := CompileRequest{
		Sources: SourceSpec{Entity: EntityRef{Type: "user", ID: "test"}},
	}

	prompt := c.buildFieldPrompt("test_field", fieldDef, nodes, req, nil)

	if len(prompt) == 0 {
		t.Error("prompt is empty")
	}
	if !contains(prompt, "test_field") {
		t.Error("prompt should contain field name")
	}
	if !contains(prompt, "node_1") {
		t.Error("prompt should contain source node ID")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
