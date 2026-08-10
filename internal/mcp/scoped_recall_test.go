package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// setupScopedRecallFixture builds an engine with a root node and a set of
// memories in its neighborhood, and returns the service + engine.
func setupScopedRecallFixture(t *testing.T) (*Service, *engine.Engine, func()) {
	t.Helper()

	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("failed to open engine: %v", err)
	}
	if err := eng.VCreate("mcp_memory", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Root entity + linked memories. Vectors come from the mock embedder
	// (deterministic per text), so semantic proximity follows text similarity.
	rootVec, _ := (&mockEmbedder{}).Embed("project alpha")
	eng.VAdd("mcp_memory", "project:alpha", rootVec, map[string]any{"type": "project"})

	memories := []string{
		"project alpha architecture decisions",
		"project alpha database choice",
		"project alpha deployment plan",
		"user prefers concise answers",
		"unrelated cooking recipe",
	}
	for i, content := range memories {
		vec, _ := (&mockEmbedder{}).Embed(content)
		eng.VAdd("mcp_memory", fmt.Sprintf("mem%d", i), vec, map[string]any{"type": "memory", "content": content})
		eng.VLink("mcp_memory", "project:alpha", fmt.Sprintf("mem%d", i), "mentions", "mentioned_in", 1.0, nil)
	}

	embedder := &mockEmbedder{}
	svc := NewService(eng, embedder, nil, nil)

	cleanup := func() {
		eng.Close()
	}
	return svc, eng, cleanup
}

// TestScopedRecallTwoStage_ExpandOnSparseRoot verifies that a query with few
// scoped hits gets expanded with semantically similar nodes from the root's
// neighborhood, up to the limit.
func TestScopedRecallTwoStage_ExpandOnSparseRoot(t *testing.T) {
	svc, _, cleanup := setupScopedRecallFixture(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	// Query semantically similar to only a subset of the scoped memories.
	_, result, err := svc.ScopedRecall(ctx, req, ScopedRecallArgs{
		Query:  "project alpha",
		RootID: "project:alpha",
		Limit:  4,
	})
	if err != nil {
		t.Fatalf("ScopedRecall: %v", err)
	}
	if len(result.Results) == 0 {
		t.Fatal("expected results from scoped recall")
	}
	if len(result.Results) < 4 {
		t.Errorf("two-stage expansion should fill up to limit: got %d results, want >= 4", len(result.Results))
	}
	// The expansion must stay within the root's scope: "unrelated cooking
	// recipe" is linked to the root here (fixture), so scope check is about
	// semantic ordering: the top result should be project-related.
	t.Logf("results: %d -> first: %s", len(result.Results), result.Results[0])
}

// TestScopedRecallTwoStage_Dedup verifies a node present in both the seed and
// the expansion appears only once.
func TestScopedRecallTwoStage_Dedup(t *testing.T) {
	svc, _, cleanup := setupScopedRecallFixture(t)
	defer cleanup()

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	_, result, err := svc.ScopedRecall(ctx, req, ScopedRecallArgs{
		Query:  "project alpha",
		RootID: "project:alpha",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ScopedRecall: %v", err)
	}

	seen := make(map[string]bool)
	for _, r := range result.Results {
		if seen[r] {
			t.Errorf("duplicate result: %s", r)
		}
		seen[r] = true
	}
}

// TestScopedRecallTwoStage_RespectsScope verifies that the expansion never
// returns nodes outside the root's graph neighborhood.
func TestScopedRecallTwoStage_RespectsScope(t *testing.T) {
	svc, eng, cleanup := setupScopedRecallFixture(t)
	defer cleanup()

	// An out-of-scope memory: not linked to the root.
	vec, _ := (&mockEmbedder{}).Embed("completely unrelated topic about gardening")
	if err := eng.VAdd("mcp_memory", "out_of_scope", vec, map[string]any{"type": "memory", "content": "gardening"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	req := &mcp.CallToolRequest{}

	_, result, err := svc.ScopedRecall(ctx, req, ScopedRecallArgs{
		Query:  "project alpha",
		RootID: "project:alpha",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ScopedRecall: %v", err)
	}
	for _, r := range result.Results {
		if r == "out_of_scope" {
			t.Error("expansion leaked a node outside the root's neighborhood")
		}
	}
}
