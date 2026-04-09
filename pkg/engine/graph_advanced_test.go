package engine

import (
	"slices"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

func TestVTraverse(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "traverse_test"

	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	if err := eng.VAdd(indexName, "node_a", []float32{1, 0, 0}, map[string]any{"name": "A"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(indexName, "node_b", []float32{0, 1, 0}, map[string]any{"name": "B"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(indexName, "node_c", []float32{0, 0, 1}, map[string]any{"name": "C"}); err != nil {
		t.Fatal(err)
	}

	if err := eng.VLink(indexName, "node_a", "node_b", "knows", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "node_b", "node_c", "knows", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	t.Run("EmptyPaths", func(t *testing.T) {
		node, err := eng.VTraverse(indexName, "node_a", nil)
		if err != nil {
			t.Fatalf("VTraverse failed: %v", err)
		}
		if node.ID != "node_a" {
			t.Errorf("Expected node_a, got %s", node.ID)
		}
		if len(node.Connections) != 0 {
			t.Errorf("Expected no connections for empty paths")
		}
	})

	t.Run("SinglePath", func(t *testing.T) {
		node, err := eng.VTraverse(indexName, "node_a", []string{"knows"})
		if err != nil {
			t.Fatalf("VTraverse failed: %v", err)
		}
		if node.ID != "node_a" {
			t.Errorf("Expected node_a, got %s", node.ID)
		}
		conns, ok := node.Connections["knows"]
		if !ok {
			t.Fatal("Missing 'knows' connection")
		}
		if len(conns) != 1 || conns[0].ID != "node_b" {
			t.Errorf("Expected [node_b], got %v", conns)
		}
	})

	t.Run("NestedPath", func(t *testing.T) {
		node, err := eng.VTraverse(indexName, "node_a", []string{"knows.knows"})
		if err != nil {
			t.Fatalf("VTraverse failed: %v", err)
		}
		conns, ok := node.Connections["knows.knows"]
		if !ok {
			t.Fatal("Missing 'knows.knows' connection")
		}
		if len(conns) != 1 {
			t.Errorf("Expected 1 node at first hop, got %d", len(conns))
		}
		if conns[0].ID != "node_b" {
			t.Errorf("Expected first hop to be node_b, got %s", conns[0].ID)
		}
		if conns[0].Connections == nil {
			t.Error("Expected nested connections in first hop node")
		}
	})

	t.Run("MultiplePaths", func(t *testing.T) {
		if err := eng.VLink(indexName, "node_a", "node_c", "related", "", 1.0, nil); err != nil {
			t.Fatal(err)
		}
		node, err := eng.VTraverse(indexName, "node_a", []string{"knows", "related"})
		if err != nil {
			t.Fatalf("VTraverse failed: %v", err)
		}
		if _, ok := node.Connections["knows"]; !ok {
			t.Error("Missing 'knows' connection")
		}
		if _, ok := node.Connections["related"]; !ok {
			t.Error("Missing 'related' connection")
		}
	})

	t.Run("NonExistentStartNode", func(t *testing.T) {
		_, err := eng.VTraverse(indexName, "nonexistent", []string{"knows"})
		if err == nil {
			t.Error("Expected error for nonexistent node")
		}
	})
}

func TestVExtractSubgraph(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "subgraph_test"

	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if err := eng.VAdd(indexName, "node_"+string(rune('a'+i)), []float32{float32(i), 0, 0}, map[string]any{"index": i}); err != nil {
			t.Fatal(err)
		}
	}

	if err := eng.VLink(indexName, "node_a", "node_b", "next", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "node_b", "node_c", "next", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "node_a", "node_d", "jump", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	t.Run("BasicExtraction", func(t *testing.T) {
		result, err := eng.VExtractSubgraph(indexName, "node_a", []string{"next", "jump"}, 2, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		if result.RootID != "node_a" {
			t.Errorf("Expected root node_a, got %s", result.RootID)
		}
		if len(result.Nodes) < 3 {
			t.Errorf("Expected at least 3 nodes, got %d", len(result.Nodes))
		}
		if len(result.Edges) < 2 {
			t.Errorf("Expected at least 2 edges, got %d", len(result.Edges))
		}
	})

	t.Run("DepthLimit", func(t *testing.T) {
		result, err := eng.VExtractSubgraph(indexName, "node_a", []string{"next"}, 1, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		hasNodeC := false
		for _, n := range result.Nodes {
			if n.ID == "node_c" {
				hasNodeC = true
				break
			}
		}
		if hasNodeC {
			t.Error("Depth limit not respected - node_c should not be included")
		}
	})

	t.Run("RelationFilter", func(t *testing.T) {
		result, err := eng.VExtractSubgraph(indexName, "node_a", []string{"next"}, 2, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		hasJumpEdge := false
		for _, e := range result.Edges {
			if e.Relation == "jump" {
				hasJumpEdge = true
				break
			}
		}
		if hasJumpEdge {
			t.Error("Should not have 'jump' edges when only 'next' is requested")
		}
	})

	t.Run("NonExistentRoot", func(t *testing.T) {
		_, err := eng.VExtractSubgraph(indexName, "nonexistent", []string{"next"}, 1, 0, nil, 0)
		if err != nil {
			t.Logf("VExtractSubgraph correctly returns error for nonexistent root: %v", err)
		}
	})

	t.Run("EmptyRelations", func(t *testing.T) {
		result, err := eng.VExtractSubgraph(indexName, "node_a", []string{}, 2, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		if len(result.Nodes) != 1 {
			t.Errorf("Expected only root node, got %d", len(result.Nodes))
		}
	})
}

func TestFindPath(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "pathfinding_test"

	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	nodes := []string{"a", "b", "c", "d", "e"}
	for i, id := range nodes {
		if err := eng.VAdd(indexName, id, []float32{float32(i), 0, 0}, nil); err != nil {
			t.Fatal(err)
		}
	}

	if err := eng.VLink(indexName, "a", "b", "next", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "b", "c", "next", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "c", "d", "next", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "a", "e", "jump", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	t.Run("DirectPath", func(t *testing.T) {
		result, err := eng.FindPath(indexName, "a", "b", []string{"next"}, 4, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result == nil {
			t.Fatal("Expected path, got nil")
		}
		if !slices.Contains(result.Path, "a") || !slices.Contains(result.Path, "b") {
			t.Errorf("Path should contain a and b: %v", result.Path)
		}
	})

	t.Run("MultiHopPath", func(t *testing.T) {
		result, err := eng.FindPath(indexName, "a", "d", []string{"next"}, 4, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result == nil {
			t.Fatal("Expected path, got nil")
		}
		if len(result.Path) < 3 {
			t.Errorf("Expected path length >= 3, got %d: %v", len(result.Path), result.Path)
		}
	})

	t.Run("NoPathExists", func(t *testing.T) {
		result, err := eng.FindPath(indexName, "b", "e", []string{"next"}, 4, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result != nil {
			t.Errorf("Expected no path, got: %v", result.Path)
		}
	})

	t.Run("EmptyRelations", func(t *testing.T) {
		_, err := eng.FindPath(indexName, "a", "b", []string{}, 4, 0)
		if err == nil {
			t.Error("Expected error for empty relations")
		}
	})

	t.Run("MaxDepthLimit", func(t *testing.T) {
		result, err := eng.FindPath(indexName, "a", "d", []string{"next"}, 1, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result != nil {
			t.Error("Expected no path due to depth limit")
		}
	})

	t.Run("SelfPath", func(t *testing.T) {
		result, err := eng.FindPath(indexName, "a", "a", []string{"next"}, 4, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result != nil && len(result.Path) > 0 {
			t.Logf("Self path result: %v", result.Path)
		}
	})
}

func TestVCompress(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "compress_test"

	if err := eng.VCreate(indexName, distance.Cosine, 32, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	vec := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	for i := 0; i < 10; i++ {
		v := make([]float32, len(vec))
		copy(v, vec)
		for j := range v {
			v[j] += float32(i) * 0.01
		}
		if err := eng.VAdd(indexName, "vec_"+string(rune('0'+i)), v, map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("CompressToInt8", func(t *testing.T) {
		err := eng.VCompress(indexName, distance.Int8)
		if err != nil {
			t.Fatalf("VCompress failed: %v", err)
		}

		data, err := eng.VGet(indexName, "vec_0")
		if err != nil {
			t.Fatalf("VGet failed: %v", err)
		}
		if len(data.Vector) != 8 {
			t.Errorf("Expected dimension 8, got %d", len(data.Vector))
		}
	})

	t.Run("CompressNonExistentIndex", func(t *testing.T) {
		err := eng.VCompress("nonexistent", distance.Int8)
		if err == nil {
			t.Error("Expected error for nonexistent index")
		}
	})
}

func TestVUpdateIndexConfig(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "config_test"

	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	t.Run("UpdateMaintenanceConfig", func(t *testing.T) {
		newConfig := hnsw.AutoMaintenanceConfig{
			VacuumInterval:  hnsw.Duration(30 * time.Minute),
			DeleteThreshold: 0.2,
			RefineEnabled:   true,
			RefineInterval:  hnsw.Duration(60 * time.Minute),
			RefineBatchSize: 1000,
		}

		err := eng.VUpdateIndexConfig(indexName, newConfig)
		if err != nil {
			t.Fatalf("VUpdateIndexConfig failed: %v", err)
		}

		idx, ok := eng.DB.GetVectorIndex(indexName)
		if !ok {
			t.Fatal("Index not found")
		}
		hnswIdx := idx.(*hnsw.Index)
		cfg := hnswIdx.GetMaintenanceConfig()

		if cfg.RefineEnabled != true {
			t.Errorf("Expected RefineEnabled=true, got %v", cfg.RefineEnabled)
		}
		if cfg.RefineInterval != hnsw.Duration(60*time.Minute) {
			t.Errorf("Expected RefineInterval=60m, got %v", cfg.RefineInterval)
		}
	})

	t.Run("UpdateNonExistentIndex", func(t *testing.T) {
		err := eng.VUpdateIndexConfig("nonexistent", hnsw.AutoMaintenanceConfig{})
		if err == nil {
			t.Error("Expected error for nonexistent index")
		}
	})
}

func TestRunGraphVacuum(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	indexName := "vacuum_test"

	vacuumConfig := hnsw.AutoMaintenanceConfig{
		GraphRetention: hnsw.Duration(1),
	}

	if err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", &vacuumConfig, nil, nil); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if err := eng.VAdd(indexName, "node_"+string(rune('a'+i)), []float32{float32(i), 0, 0}, nil); err != nil {
			t.Fatal(err)
		}
	}

	if err := eng.VLink(indexName, "node_a", "node_b", "related", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VLink(indexName, "node_b", "node_c", "related", "", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	initialLinks, _ := eng.VGetLinks(indexName, "node_a", "related")
	if len(initialLinks) != 1 {
		t.Fatalf("Expected 1 link, got %d", len(initialLinks))
	}

	t.Run("VacuumWithNoExpiredEdges", func(t *testing.T) {
		eng.RunGraphVacuum()

		links, _ := eng.VGetLinks(indexName, "node_a", "related")
		if len(links) != 1 {
			t.Errorf("Expected links to remain, got %d", len(links))
		}
	})

	t.Run("VacuumWithZeroRetention", func(t *testing.T) {
		tmpDir2 := t.TempDir()
		opts2 := DefaultOptions(tmpDir2)
		opts2.AutoSaveInterval = 0
		eng2, err := Open(opts2)
		if err != nil {
			t.Fatal(err)
		}
		defer eng2.Close()

		indexName2 := "vacuum_no_retention"
		if err := eng2.VCreate(indexName2, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := eng2.VAdd(indexName2, "a", []float32{1, 0, 0}, nil); err != nil {
			t.Fatal(err)
		}
		if err := eng2.VAdd(indexName2, "b", []float32{0, 1, 0}, nil); err != nil {
			t.Fatal(err)
		}
		if err := eng2.VLink(indexName2, "a", "b", "rel", "", 1.0, nil); err != nil {
			t.Fatal(err)
		}

		eng2.RunGraphVacuum()

		links, _ := eng2.VGetLinks(indexName2, "a", "rel")
		if len(links) != 1 {
			t.Errorf("With zero retention, edges should be kept forever, got %d", len(links))
		}
	})
}
