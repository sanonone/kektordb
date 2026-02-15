package engine

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
)

func TestAutoLinking(t *testing.T) {
	// 1. Setup Engine
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0 // Disable auto-save for speed
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "tasks"

	// 2. Define Rules
	// Logic: If metadata has "project_id", create a link "belongs_to_project" -> project_id value
	rules := []hnsw.AutoLinkRule{
		{
			MetadataField: "project_id",
			RelationType:  "belongs_to_project",
		},
	}

	// 3. Create Index with Rules
	// We pass 'rules' as the last argument
	err = eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, rules, nil)
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// 4. Test Single Insert (VAdd)
	// Task A belongs to "project_alpha"
	metaA := map[string]any{
		"project_id": "project_alpha",
		"title":      "Implement Graph",
	}
	if err := eng.VAdd(indexName, "task_A", []float32{0.1, 0.2}, metaA); err != nil {
		t.Fatalf("VAdd failed: %v", err)
	}

	// 5. Test Batch Insert (VAddBatch)
	// Task B and C belong to "project_alpha", Task D belongs to "project_beta"
	batch := []types.BatchObject{
		{
			Id:       "task_B",
			Vector:   []float32{0.2, 0.3},
			Metadata: map[string]any{"project_id": "project_alpha"},
		},
		{
			Id:       "task_C",
			Vector:   []float32{0.3, 0.4},
			Metadata: map[string]any{"project_id": "project_alpha"},
		},
		{
			Id:       "task_D",
			Vector:   []float32{0.9, 0.9},
			Metadata: map[string]any{"project_id": "project_beta"}, // Different project
		},
	}
	if err := eng.VAddBatch(indexName, batch); err != nil {
		t.Fatalf("VAddBatch failed: %v", err)
	}

	// 6. Verify Links (The "Smart Graph" check)

	// A. Check Forward Link: Does task_A point to project_alpha?
	targets, found := eng.VGetLinks("task_A", "belongs_to_project")
	if !found || !slices.Contains(targets, "project_alpha") {
		t.Errorf("Auto-link failed for VAdd. task_A should point to project_alpha. Got: %v", targets)
	}

	// B. Check Batch Link: Does task_D point to project_beta?
	targetsD, foundD := eng.VGetLinks("task_D", "belongs_to_project")
	if !foundD || !slices.Contains(targetsD, "project_beta") {
		t.Errorf("Auto-link failed for VAddBatch. task_D should point to project_beta. Got: %v", targetsD)
	}

	// C. Check Reverse Aggregation (Critical for RAG/Agents)
	// "project_alpha" should automatically know it contains task_A, task_B, and task_C.
	sources, foundRev := eng.VGetIncoming("project_alpha", "belongs_to_project")
	if !foundRev {
		t.Fatal("Reverse index entry for project_alpha not found")
	}

	expectedTasks := []string{"task_A", "task_B", "task_C"}
	for _, taskID := range expectedTasks {
		if !slices.Contains(sources, taskID) {
			t.Errorf("Reverse link incomplete. project_alpha should contain %s. Got: %v", taskID, sources)
		}
	}

	// D. Check project_beta (should only have task_D)
	sourcesBeta, _ := eng.VGetIncoming("project_beta", "belongs_to_project")
	if len(sourcesBeta) != 1 || sourcesBeta[0] != "task_D" {
		t.Errorf("Reverse link error for project_beta. Expected [task_D], got: %v", sourcesBeta)
	}

	t.Log("Auto-Linking Test Passed: Metadata successfully converted to Graph Edges.")
}
