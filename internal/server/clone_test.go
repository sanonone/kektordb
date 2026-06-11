package server

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/engine"
)

func TestCloneGraphNode_DeepCopy(t *testing.T) {
	// Build a tree: root -> [child1, child2], child1 -> [grandchild]
	original := &engine.GraphNode{
		VectorData: core.VectorData{
			ID:       "root",
			Metadata: map[string]any{"x": "y"},
		},
		Connections: map[string][]engine.GraphNode{
			"child": {
				{
					VectorData: core.VectorData{
						ID:       "child1",
						Metadata: map[string]any{"a": "b"},
					},
					Connections: map[string][]engine.GraphNode{
						"next": {
							{
								VectorData: core.VectorData{
									ID:       "grandchild",
									Metadata: map[string]any{"deep": true},
								},
							},
						},
					},
				},
				{
					VectorData: core.VectorData{
						ID:       "child2",
						Metadata: map[string]any{"c": "d"},
					},
				},
			},
		},
	}

	cloned := cloneGraphNode(original)

	// Verify root
	if cloned.ID != "root" {
		t.Errorf("root ID: got %s, want root", cloned.ID)
	}
	if cloned.Metadata["x"] != "y" {
		t.Error("root metadata not copied")
	}

	// Verify it's a copy, not a reference
	cloned.Metadata["x"] = "modified"
	if original.Metadata["x"] == "modified" {
		t.Error("metadata is shared, not a deep copy")
	}

	// Verify children
	children, ok := cloned.Connections["child"]
	if !ok || len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	if children[0].ID != "child1" || children[1].ID != "child2" {
		t.Error("child IDs not preserved")
	}

	// Verify grandchild (this was the bug: old clone dropped nested connections)
	grandchildren, ok := children[0].Connections["next"]
	if !ok || len(grandchildren) != 1 {
		t.Fatalf("BUG #4.2: expected 1 grandchild, got %d", len(grandchildren))
	}
	if grandchildren[0].ID != "grandchild" {
		t.Errorf("grandchild ID: got %s, want grandchild", grandchildren[0].ID)
	}
	if grandchildren[0].Metadata["deep"] != true {
		t.Error("grandchild metadata not preserved")
	}
}

func TestCloneGraphNode_Nil(t *testing.T) {
	if cloneGraphNode(nil) != nil {
		t.Error("cloneGraphNode(nil) should return nil")
	}
}

func TestCloneGraphNode_NoConnections(t *testing.T) {
	original := &engine.GraphNode{
		VectorData: core.VectorData{
			ID:       "solo",
			Vector:   []float32{1.0, 2.0},
			Metadata: map[string]any{"key": "value"},
		},
	}

	cloned := cloneGraphNode(original)

	if cloned.ID != "solo" {
		t.Error("ID not preserved")
	}
	if len(cloned.Vector) != 2 || cloned.Vector[0] != 1.0 {
		t.Error("vector not preserved")
	}
	if cloned.Connections == nil {
		t.Error("connections should be initialized (empty map)")
	}
	if len(cloned.Connections) != 0 {
		t.Error("connections should be empty")
	}

	// Verify vector is a copy
	cloned.Vector[0] = 99.0
	if original.Vector[0] == 99.0 {
		t.Error("vector is shared, not copied")
	}
}
