package compiler

import (
	"testing"
)

func TestEnrichProvenanceFiltersInvalidSources(t *testing.T) {
	c := NewCompiler(nil, nil)

	llmSources := []Provenance{
		{SourceID: "node_1", Confidence: 0.9, Evidence: "valid", Role: "primary"},
		{SourceID: "node_nonexistent", Confidence: 0.8, Evidence: "fake", Role: "primary"},
	}

	actualNodes := []NodeInfo{
		{ID: "node_1"},
		{ID: "node_2"},
	}

	result := c.enrichProvenance(llmSources, actualNodes)

	if len(result) != 1 {
		t.Errorf("expected 1 source after filtering, got %d", len(result))
	}
	if result[0].SourceID != "node_1" {
		t.Errorf("expected node_1, got %s", result[0].SourceID)
	}
}

func TestEnrichProvenanceAddsAutoWhenAllInvalid(t *testing.T) {
	c := NewCompiler(nil, nil)

	llmSources := []Provenance{
		{SourceID: "node_fake_1", Confidence: 0.9, Evidence: "fake"},
		{SourceID: "node_fake_2", Confidence: 0.8, Evidence: "fake"},
	}

	actualNodes := []NodeInfo{
		{ID: "node_real_1"},
	}

	result := c.enrichProvenance(llmSources, actualNodes)

	if len(result) != 1 {
		t.Fatalf("expected 1 auto-provenance, got %d", len(result))
	}
	if result[0].SourceID != "node_real_1" {
		t.Errorf("expected auto-provenance from node_real_1, got %s", result[0].SourceID)
	}
	if result[0].Role != "inferred" {
		t.Errorf("expected role 'inferred', got '%s'", result[0].Role)
	}
}

func TestEnrichProvenanceAddsAutoWhenEmpty(t *testing.T) {
	c := NewCompiler(nil, nil)

	actualNodes := []NodeInfo{
		{ID: "node_1"},
	}

	result := c.enrichProvenance(nil, actualNodes)
	if len(result) != 1 {
		t.Fatalf("expected auto-provenance for nil sources, got %d", len(result))
	}

	result2 := c.enrichProvenance([]Provenance{}, actualNodes)
	if len(result2) != 1 {
		t.Fatalf("expected auto-provenance for empty sources, got %d", len(result2))
	}
}

func TestValidateProvenanceAddsAutoForMissingFields(t *testing.T) {
	c := NewCompiler(nil, nil)

	artifact := &Artifact{
		Data:          map[string]any{"name": "Alice", "age": 30},
		Provenance:    map[string][]Provenance{},
		SourceNodeIDs: []string{"node_1"},
	}

	c.validateProvenance(artifact)

	if prov, ok := artifact.Provenance["name"]; !ok || len(prov) == 0 {
		t.Error("validateProvenance should add provenance for name")
	}
	if prov, ok := artifact.Provenance["age"]; !ok || len(prov) == 0 {
		t.Error("validateProvenance should add provenance for age")
	}
}

func TestValidateProvenancePreservesExisting(t *testing.T) {
	c := NewCompiler(nil, nil)

	existingProv := []Provenance{
		{SourceID: "node_1", Confidence: 0.95, Evidence: "from metadata", Role: "primary"},
	}

	artifact := &Artifact{
		Data:       map[string]any{"name": "Alice"},
		Provenance: map[string][]Provenance{"name": existingProv},
	}

	c.validateProvenance(artifact)

	if len(artifact.Provenance["name"]) != 1 {
		t.Error("existing provenance should be preserved")
	}
	if artifact.Provenance["name"][0].SourceID != "node_1" {
		t.Error("existing provenance should not be modified")
	}
}
