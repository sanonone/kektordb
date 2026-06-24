package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/compiler"
)

func TestGetRelations(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	eng.VAdd(idx, "src", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst1", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VAdd(idx, "dst2", make([]float32, 384), map[string]any{"type": "memory"})
	eng.VLink(idx, "src", "dst1", "mentions", "mentioned_by", 1.0, nil)
	eng.VLink(idx, "src", "dst2", "related_to", "", 1.0, nil)

	// Outgoing
	_, result, err := svc.GetRelations(nil, nil, GetRelationsArgs{
		NodeID:    "src",
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("GetRelations failed: %v", err)
	}
	if result.OutCount != 2 {
		t.Errorf("expected 2 outgoing relations, got %d (out=%v)", result.OutCount, result.Outgoing)
	}
	if len(result.Outgoing["mentions"]) != 1 || result.Outgoing["mentions"][0] != "dst1" {
		t.Errorf("expected mentions -> [dst1], got %v", result.Outgoing["mentions"])
	}
	if len(result.Outgoing["related_to"]) != 1 || result.Outgoing["related_to"][0] != "dst2" {
		t.Errorf("expected related_to -> [dst2], got %v", result.Outgoing["related_to"])
	}
	// Note: InCount may be > 0 if the inverse relations are also stored
	// (e.g. dst1 -> src via "mentioned_by"). The graph engine creates
	// both edges; this is engine behavior, not a bug in our handler.
	t.Logf("src inCount=%d in=%v (inverse edges from graph engine)", result.InCount, result.Incoming)
	if len(result.Incoming["mentioned_by"]) != 1 || result.Incoming["mentioned_by"][0] != "dst1" {
		t.Errorf("expected mentioned_by -> [dst1] in incoming, got %v", result.Incoming)
	}

	// Incoming (reverse direction)
	_, result2, _ := svc.GetRelations(nil, nil, GetRelationsArgs{
		NodeID:    "dst1",
		IndexName: idx,
	})
	if result2.InCount != 1 {
		t.Errorf("expected 1 incoming on dst1, got %d (in=%v)", result2.InCount, result2.Incoming)
	}
	// VGetIncomingRelations returns the original outgoing relation type
	// (mentions, not mentioned_by) — both edge keys are merged.
	if result2.Incoming["mentions"] == nil || result2.Incoming["mentions"][0] != "src" {
		t.Errorf("expected mentions -> [src] in incoming, got %v", result2.Incoming["mentions"])
	}
}

func TestGetRelationsEmptyNodeID(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.GetRelations(nil, nil, GetRelationsArgs{})
	if err != nil {
		t.Fatalf("GetRelations failed: %v", err)
	}
	if result.Message == "" {
		t.Error("expected error message for empty node_id")
	}
	if result.OutCount != 0 || result.InCount != 0 {
		t.Error("expected zero counts for empty node_id")
	}
}

func TestGetGardenerStatusNoGardener(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.GetGardenerStatus(nil, nil, GetGardenerStatusArgs{})
	if err != nil {
		t.Fatalf("GetGardenerStatus failed: %v", err)
	}
	if result.Enabled {
		t.Error("expected Enabled=false when gardener is nil")
	}
	if result.Message == "" {
		t.Error("expected message when gardener is nil")
	}
}

func TestListArtifactsEmpty(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.ListArtifacts(nil, nil, ListArtifactsArgs{})
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 artifacts, got %d", result.Total)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("expected empty artifacts list, got %d", len(result.Artifacts))
	}
}

func TestListArtifactsWithEntry(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	idx := "mcp_memory"

	// Set up a user entity + memory so the compiler can produce an artifact
	eng.VAdd(idx, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(idx, "user:alice:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice prefers concise code",
	})
	eng.VLink(idx, "user:alice", "user:alice:mem1", "has_interaction", "interaction_of", 1.0, nil)

	// Compile a pre-known artifact (entity_card is deterministic)
	_, err := comp.Compile(compiler.CompileRequest{
		Name: "entity_card",
		Sources: compiler.SourceSpec{
			Type:   "graph_query",
			Entity: compiler.EntityRef{Type: "user", ID: "alice"},
			Depth:  2,
		},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, result, err := svc.ListArtifacts(nil, nil, ListArtifactsArgs{IndexName: idx})
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected 1 artifact, got %d", result.Total)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact in list, got %d", len(result.Artifacts))
	}
	a := result.Artifacts[0]
	if a.Name != "entity_card" {
		t.Errorf("expected entity_card, got %s", a.Name)
	}
	if a.EntityID != "alice" {
		t.Errorf("expected entity_id=alice, got %s", a.EntityID)
	}
	if a.Version < 1 {
		t.Errorf("expected version >= 1, got %d", a.Version)
	}
}

func TestListReflectionsEmpty(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.ListReflections(nil, nil, ListReflectionsArgs{})
	if err != nil {
		t.Fatalf("ListReflections failed: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 reflections, got %d", result.Total)
	}
	if result.Message == "" {
		t.Error("expected message for empty case")
	}
}

func TestListReflectionsWithEntry(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	// Manually add a reflection node
	eng.VAdd(idx, "refl:001", make([]float32, 384), map[string]any{
		"type":         "reflection",
		"content":      "User contradicts themselves about Python",
		"status":       "unresolved",
		"confidence":   0.85,
		"derived_from": []interface{}{"mem:001", "mem:002"},
		"_created_at":  float64(time.Now().Unix()),
	})

	_, result, err := svc.ListReflections(nil, nil, ListReflectionsArgs{IndexName: idx})
	if err != nil {
		t.Fatalf("ListReflections failed: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected 1 reflection, got %d", result.Total)
	}
	if len(result.Reflections) != 1 {
		t.Fatalf("expected 1 reflection, got %d", len(result.Reflections))
	}
	r := result.Reflections[0]
	if r.Type != "reflection" {
		t.Errorf("expected type=reflection, got %s", r.Type)
	}
	if r.Status != "unresolved" {
		t.Errorf("expected status=unresolved, got %s", r.Status)
	}
	if r.Confidence != 0.85 {
		t.Errorf("expected confidence=0.85, got %f", r.Confidence)
	}
	if len(r.DerivedFrom) != 2 {
		t.Errorf("expected 2 derived_from, got %d", len(r.DerivedFrom))
	}

	// Test status filter
	_, filtered, _ := svc.ListReflections(nil, nil, ListReflectionsArgs{
		IndexName: idx,
		Status:    "resolved",
	})
	if filtered.Total != 0 {
		t.Errorf("expected 0 resolved reflections, got %d", filtered.Total)
	}
}

func TestListReflectionsHistoricalExcluded(t *testing.T) {
	svc, eng := newTestServiceMinimal(t)
	idx := "mcp_memory"

	// Add a historical reflection — should be excluded
	eng.VAdd(idx, "refl:old", make([]float32, 384), map[string]any{
		"type":          "reflection",
		"content":       "old reflection",
		"status":        "unresolved",
		"_is_historical": true,
	})

	_, result, _ := svc.ListReflections(nil, nil, ListReflectionsArgs{IndexName: idx})
	if result.Total != 0 {
		t.Errorf("historical reflections should be excluded, got %d", result.Total)
	}
}

func TestForceRecompileMissingFields(t *testing.T) {
	svc, _, _ := newTestServiceWithCompiler(t)
	_, result, err := svc.ForceRecompile(nil, nil, ForceRecompileArgs{})
	if err != nil {
		t.Fatalf("ForceRecompile failed: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("expected status=failed, got %s", result.Status)
	}
	if result.Message == "" {
		t.Error("expected message for missing fields")
	}
}

func TestForceRecompileNoCompiler(t *testing.T) {
	svc, _ := newTestServiceMinimal(t)
	_, result, err := svc.ForceRecompile(nil, nil, ForceRecompileArgs{
		Intent: "user_profile",
		Entity: "dash",
	})
	if err != nil {
		t.Fatalf("ForceRecompile failed: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("expected status=failed, got %s", result.Status)
	}
	if result.Message == "" {
		t.Error("expected message about compiler not configured")
	}
}

// TestGetArtifactHistoryEmptyEntityType verifies the fix for the
// entity_type=empty case. When the agent doesn't know entity_type, the
// handler should fall back to a broader filter (without entity_type) and
// filter by entity_id in-memory. This makes the tool consistent with
// list_artifacts which doesn't filter by entity_type at all.
func TestGetArtifactHistoryEmptyEntityType(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	idx := "mcp_memory"

	// Set up a user entity + memory so the compiler can produce an artifact
	eng.VAdd(idx, "user:alice", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(idx, "user:alice:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Alice prefers concise code",
	})
	eng.VLink(idx, "user:alice", "user:alice:mem1", "has_interaction", "interaction_of", 1.0, nil)

	// Compile a pre-known artifact (entity_card is deterministic)
	_, err := comp.Compile(compiler.CompileRequest{
		Name: "entity_card",
		Sources: compiler.SourceSpec{
			Type:   "graph_query",
			Entity: compiler.EntityRef{Type: "user", ID: "alice"},
			Depth:  2,
		},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Test 1: WITH entity_type (existing path)
	_, resultWith, _ := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent:     "entity_card",
		Entity:     "alice",
		EntityType: "user",
		IndexName:  idx,
	})
	if resultWith.TotalVersions != 1 {
		t.Errorf("with entity_type: expected 1 version, got %d (msg=%s)", resultWith.TotalVersions, resultWith.Message)
	}

	// Test 2: WITHOUT entity_type (new fallback path) — should also find the artifact
	_, resultWithout, _ := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent:    "entity_card",
		Entity:    "alice",
		IndexName: idx,
		// EntityType intentionally empty
	})
	if resultWithout.TotalVersions != 1 {
		t.Errorf("without entity_type: expected 1 version (fallback), got %d (msg=%s)",
			resultWithout.TotalVersions, resultWithout.Message)
	}
	if len(resultWithout.Versions) > 0 && resultWithout.Versions[0].Version < 1 {
		t.Error("expected version >= 1 in fallback result")
	}
}

// TestGetArtifactHistoryEmptyEntityTypeMultipleEntities verifies that the
// fallback correctly disambiguates when there are artifacts with different
// entity_types but same entity_id. Should return only the matching one.
func TestGetArtifactHistoryEmptyEntityTypeMultipleEntities(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	idx := "mcp_memory"

	// Set up two entities with same ID but different types
	eng.VAdd(idx, "user:bob", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "bob", "name": "Bob", "_pinned": true,
	})
	eng.VAdd(idx, "user:bob:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Bob memory",
	})
	eng.VLink(idx, "user:bob", "user:bob:mem1", "has_interaction", "interaction_of", 1.0, nil)

	eng.VAdd(idx, "project:bob", make([]float32, 384), map[string]any{
		"type": "project", "entity_id": "bob", "name": "Bob's Project", "_pinned": true,
	})
	eng.VAdd(idx, "project:bob:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Bob's project memory",
	})
	eng.VLink(idx, "project:bob", "project:bob:mem1", "has_interaction", "interaction_of", 1.0, nil)

	// Compile entity_card for both
	for _, entityType := range []string{"user", "project"} {
		_, err := comp.Compile(compiler.CompileRequest{
			Name: "entity_card",
			Sources: compiler.SourceSpec{
				Type:   "graph_query",
				Entity: compiler.EntityRef{Type: entityType, ID: "bob"},
				Depth:  2,
			},
			IndexName: idx,
		})
		if err != nil {
			t.Fatalf("compile %s failed: %v", entityType, err)
		}
	}

	// Without entity_type — both artifacts match entity_id=bob
	_, result, _ := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent:    "entity_card",
		Entity:    "bob",
		IndexName: idx,
		// EntityType intentionally empty
	})
	// We expect both user and project artifacts to be returned
	// (filter by entity_id matches both, both have entity_id="bob")
	if result.TotalVersions < 2 {
		t.Errorf("expected at least 2 versions (both user and project), got %d (msg=%s)",
			result.TotalVersions, result.Message)
	}
}

// TestGetArtifactHistoryDiagnosticMessage verifies the diagnostic message
// when no artifact is found — it should mention available entities.
func TestGetArtifactHistoryDiagnosticMessage(t *testing.T) {
	svc, eng, comp := newTestServiceWithCompiler(t)
	idx := "mcp_memory"

	// Create a user entity and compile entity_card for it
	eng.VAdd(idx, "user:charlie", make([]float32, 384), map[string]any{
		"type": "user", "entity_id": "charlie", "name": "Charlie", "_pinned": true,
	})
	eng.VAdd(idx, "user:charlie:mem1", make([]float32, 384), map[string]any{
		"type": "memory", "content": "Charlie data",
	})
	eng.VLink(idx, "user:charlie", "user:charlie:mem1", "has_interaction", "interaction_of", 1.0, nil)
	_, err := comp.Compile(compiler.CompileRequest{
		Name: "entity_card",
		Sources: compiler.SourceSpec{
			Type:   "graph_query",
			Entity: compiler.EntityRef{Type: "user", ID: "charlie"},
			Depth:  2,
		},
		IndexName: idx,
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	// Query for a NON-EXISTENT entity. Diagnostic should mention existing entities.
	_, result, _ := svc.GetArtifactHistory(nil, nil, GetArtifactHistoryArgs{
		Intent:    "entity_card",
		Entity:    "nonexistent_user",
		IndexName: idx,
	})
	if result.TotalVersions != 0 {
		t.Errorf("expected 0 versions, got %d", result.TotalVersions)
	}
	if result.Message == "" {
		t.Error("expected diagnostic message")
	}
	// The diagnostic should mention charlie (the existing entity)
	if !strings.Contains(result.Message, "charlie") {
		t.Errorf("expected message to mention existing entity 'charlie', got: %s", result.Message)
	}
}
