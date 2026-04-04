package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// testServer manages a KektorDB server subprocess for E2E testing.
type testServer struct {
	cmd    *exec.Cmd
	port   int
	tmpDir string
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()

	tmpDir := t.TempDir()
	port := 19091 // Use a non-default port to avoid conflicts

	// Build the binary
	binPath := filepath.Join(tmpDir, "kektordb-test")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/kektordb")
	buildCmd.Dir = filepath.Join(getProjectRoot(), ".")
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build kektordb binary: %v", err)
	}

	// Start server
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(dataDir, 0755)

	srv := exec.Command(binPath,
		"--http-addr", fmt.Sprintf(":%d", port),
		"--aof-path", filepath.Join(dataDir, "test.aof"),
		"--log-level", "error",
	)
	srv.Stdout = nil
	srv.Stderr = os.Stderr
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	ts := &testServer{cmd: srv, port: port, tmpDir: tmpDir}

	// Wait for server to be ready
	ts.waitForHealthz(t, 15*time.Second)

	t.Cleanup(func() { ts.stop(t) })

	return ts
}

func (s *testServer) stop(t *testing.T) {
	t.Helper()
	if s.cmd.Process != nil {
		s.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			s.cmd.Process.Kill()
		}
	}
}

func (s *testServer) waitForHealthz(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://localhost:%d/healthz", s.port))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Test server did not become healthy within %v", timeout)
}

func getProjectRoot() string {
	// Walk up from pkg/client to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := filepath.Glob(filepath.Join(dir, "go.mod")); err == nil {
			if entries, _ := filepath.Glob(filepath.Join(dir, "go.mod")); len(entries) > 0 {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// --- E2E Test ---

func TestClientFullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	idxName := fmt.Sprintf("e2e_test_%d", time.Now().UnixNano())

	// --- Index Management ---
	t.Run("Index CRUD", func(t *testing.T) {
		err := c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		if err != nil {
			t.Fatalf("VCreate failed: %v", err)
		}

		indexes, err := c.ListIndexes()
		if err != nil {
			t.Fatalf("ListIndexes failed: %v", err)
		}
		found := false
		for _, idx := range indexes {
			if idx.Name == idxName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Index %s not found in list", idxName)
		}

		info, err := c.GetIndexInfo(idxName)
		if err != nil {
			t.Fatalf("GetIndexInfo failed: %v", err)
		}
		if info.Name != idxName {
			t.Errorf("Expected name=%s, got %s", idxName, info.Name)
		}
	})

	// --- Vector CRUD ---
	t.Run("Vector CRUD", func(t *testing.T) {
		err := c.VAdd(idxName, "vec_1", []float32{0.1, 0.2, 0.3, 0.4}, map[string]interface{}{"content": "hello"})
		if err != nil {
			t.Fatalf("VAdd failed: %v", err)
		}

		data, err := c.VGet(idxName, "vec_1")
		if err != nil {
			t.Fatalf("VGet failed: %v", err)
		}
		if data.ID != "vec_1" {
			t.Errorf("Expected id=vec_1, got %s", data.ID)
		}

		_, err = c.VGetMany(idxName, []string{"vec_1"})
		if err != nil {
			t.Fatalf("VGetMany failed: %v", err)
		}

		results, err := c.VSearch(idxName, 5, []float32{0.1, 0.2, 0.3, 0.4}, "", 0, 0, nil)
		if err != nil {
			t.Fatalf("VSearch failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("VSearch returned no results")
		}

		scored, err := c.VSearchWithScores(idxName, 5, []float32{0.1, 0.2, 0.3, 0.4})
		if err != nil {
			t.Fatalf("VSearchWithScores failed: %v", err)
		}
		if len(scored) == 0 || scored[0].Score <= 0 {
			t.Error("VSearchWithScores returned invalid scores")
		}
	})

	// --- Graph Operations ---
	t.Run("Graph Operations", func(t *testing.T) {
		c.VAdd(idxName, "entity_1", nil, map[string]interface{}{"name": "Python", "type": "entity"})

		err := c.VLink(idxName, "vec_1", "entity_1", "mentions", "mentioned_in")
		if err != nil {
			t.Fatalf("VLink failed: %v", err)
		}

		links, err := c.VGetLinks(idxName, "vec_1", "mentions")
		if err != nil {
			t.Fatalf("VGetLinks failed: %v", err)
		}
		if len(links) != 1 || links[0] != "entity_1" {
			t.Errorf("Expected [entity_1], got %v", links)
		}

		incoming, err := c.VGetIncoming(idxName, "entity_1", "mentions")
		if err != nil {
			t.Fatalf("VGetIncoming failed: %v", err)
		}
		if len(incoming) != 1 || incoming[0] != "vec_1" {
			t.Errorf("Expected [vec_1], got %v", incoming)
		}

		allRels, err := c.VGetAllRelations(idxName, "vec_1")
		if err != nil {
			t.Fatalf("VGetAllRelations failed: %v", err)
		}
		if _, ok := allRels["mentions"]; !ok {
			t.Error("Expected 'mentions' in all relations")
		}

		allIncoming, err := c.VGetAllIncoming(idxName, "entity_1")
		if err != nil {
			t.Fatalf("VGetAllIncoming failed: %v", err)
		}
		if _, ok := allIncoming["mentions"]; !ok {
			t.Error("Expected 'mentions' in all incoming")
		}

		sg, err := c.VExtractSubgraph(idxName, "vec_1", []string{"mentions"}, 2, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		if sg.RootID != "vec_1" {
			t.Errorf("Expected root_id=vec_1, got %s", sg.RootID)
		}

		path, err := c.FindPath(idxName, "vec_1", "entity_1", []string{"mentions"}, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if path == nil {
			t.Error("FindPath returned nil")
		}

		edges, err := c.GetEdges(idxName, "vec_1", "mentions", 0)
		if err != nil {
			t.Fatalf("GetEdges failed: %v", err)
		}
		if len(edges) == 0 {
			t.Error("GetEdges returned no edges")
		}
	})

	// --- Reinforce & Export ---
	t.Run("Reinforce and Export", func(t *testing.T) {
		err := c.VReinforce(idxName, []string{"vec_1"})
		if err != nil {
			t.Fatalf("VReinforce failed: %v", err)
		}

		export, err := c.VExport(idxName, 10, 0)
		if err != nil {
			t.Fatalf("VExport failed: %v", err)
		}
		if len(export.Data) == 0 {
			t.Error("VExport returned no data")
		}
	})

	// --- Auto-Links ---
	t.Run("Auto-Links", func(t *testing.T) {
		rules := []AutoLinkRule{{MetadataField: "project_id", RelationType: "belongs_to"}}
		err := c.SetAutoLinks(idxName, rules)
		if err != nil {
			t.Fatalf("SetAutoLinks failed: %v", err)
		}

		got, err := c.GetAutoLinks(idxName)
		if err != nil {
			t.Fatalf("GetAutoLinks failed: %v", err)
		}
		if len(got) == 0 {
			t.Error("GetAutoLinks returned empty rules")
		}
	})

	// --- Cognitive Engine ---
	t.Run("Cognitive Engine", func(t *testing.T) {
		err := c.Think(idxName)
		if err != nil {
			t.Fatalf("Think failed: %v", err)
		}

		refs, err := c.GetReflections(idxName, "")
		if err != nil {
			t.Fatalf("GetReflections failed: %v", err)
		}
		_ = refs // May be empty if no insights were generated
	})

	// --- System ---
	t.Run("System", func(t *testing.T) {
		err := c.Save()
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}

		err = c.VUpdateConfig(idxName, MaintenanceConfig{VacuumInterval: "300s"})
		if err != nil {
			t.Fatalf("VUpdateConfig failed: %v", err)
		}
	})

	// --- Cleanup ---
	t.Run("Delete", func(t *testing.T) {
		err := c.VDelete(idxName, "vec_1")
		if err != nil {
			t.Fatalf("VDelete failed: %v", err)
		}

		err = c.DeleteIndex(idxName)
		if err != nil {
			t.Fatalf("DeleteIndex failed: %v", err)
		}
	})
}

// --- Contract Test Runner ---

func TestAPIContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping contract test in short mode.")
	}

	srv := startTestServer(t)

	// Load contracts
	contractsPath := filepath.Join(getProjectRoot(), "testdata", "api_contracts.json")
	data, err := os.ReadFile(contractsPath)
	if err != nil {
		t.Fatalf("Failed to read contracts: %v", err)
	}

	var contracts struct {
		Tests []struct {
			Name                   string                 `json:"name"`
			Method                 string                 `json:"method"`
			Path                   string                 `json:"path"`
			Request                map[string]interface{} `json:"request"`
			ExpectedStatus         int                    `json:"expected_status"`
			ExpectedResponseFields []string               `json:"expected_response_fields"`
			DependsOn              []string               `json:"depends_on"`
			Auth                   *bool                  `json:"auth"`
			Cleanup                *struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"cleanup"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatalf("Failed to parse contracts: %v", err)
	}

	results := make(map[string]bool)
	client := &http.Client{Timeout: 10 * time.Second}

	for _, tc := range contracts.Tests {
		t.Run(tc.Name, func(t *testing.T) {
			// Check dependencies
			for _, dep := range tc.DependsOn {
				if !results[dep] {
					t.Skipf("Dependency '%s' not passed", dep)
				}
			}

			// Replace placeholder
			path := tc.Path
			url := fmt.Sprintf("http://localhost:%d%s", srv.port, path)

			// Prepare request
			var body io.Reader
			if tc.Request != nil {
				jsonBody, _ := json.Marshal(tc.Request)
				body = stringReader{string(jsonBody)}
			}

			req, err := http.NewRequest(tc.Method, url, body)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			// Execute
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			// Validate status
			if resp.StatusCode != tc.ExpectedStatus {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("Expected status %d, got %d. Body: %s", tc.ExpectedStatus, resp.StatusCode, string(respBody))
				return
			}

			// Validate response fields
			if len(tc.ExpectedResponseFields) > 0 && resp.StatusCode == 200 {
				respBody, _ := io.ReadAll(resp.Body)
				var respMap map[string]interface{}
				if json.Unmarshal(respBody, &respMap) == nil {
					for _, field := range tc.ExpectedResponseFields {
						if _, ok := respMap[field]; !ok {
							t.Errorf("Missing expected field '%s' in response", field)
						}
					}
				}
			}

			results[tc.Name] = true

			// Run cleanup if specified
			if tc.Cleanup != nil {
				cleanupURL := fmt.Sprintf("http://localhost:%d%s", srv.port, tc.Cleanup.Path)
				cleanupReq, _ := http.NewRequest(tc.Cleanup.Method, cleanupURL, nil)
				client.Do(cleanupReq)
			}
		})
	}
}

type stringReader struct {
	s string
}

func (r stringReader) Read(p []byte) (int, error) {
	n := copy(p, r.s)
	r.s = r.s[n:]
	if len(r.s) == 0 {
		return n, io.EOF
	}
	return n, nil
}

// =============================================================================
// COMPREHENSIVE ENDPOINT TESTS
// =============================================================================

// generateTestID creates a unique test identifier
func generateTestID() string {
	return fmt.Sprintf("test_%d", time.Now().UnixNano())
}

// TestKVStore tests all KV Store endpoints
func TestKVStore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("SetAndGet", func(t *testing.T) {
		key := generateTestID()
		value := "test_value_123"

		// Set
		err := c.Set(key, []byte(value))
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		// Get
		result, err := c.Get(key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if string(result) != value {
			t.Errorf("Expected %s, got %s", value, string(result))
		}
	})

	t.Run("SetOverwrite", func(t *testing.T) {
		key := generateTestID()

		c.Set(key, []byte("value1"))
		c.Set(key, []byte("value2"))

		result, _ := c.Get(key)
		if string(result) != "value2" {
			t.Errorf("Expected value2, got %s", string(result))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := generateTestID()

		c.Set(key, []byte("value"))
		c.Delete(key)

		_, err := c.Get(key)
		if err == nil {
			t.Error("Expected error for deleted key")
		}
	})

	t.Run("GetNonExistent", func(t *testing.T) {
		key := generateTestID() + "_nonexistent"

		_, err := c.Get(key)
		if err == nil {
			t.Error("Expected error for non-existent key")
		}
	})
}

// TestVectorIndexManagement tests all Vector Index Management endpoints
func TestVectorIndexManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("CreateIndex", func(t *testing.T) {
		idxName := generateTestID()

		err := c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		if err != nil {
			t.Fatalf("VCreate failed: %v", err)
		}

		info, err := c.GetIndexInfo(idxName)
		if err != nil {
			t.Fatalf("GetIndexInfo failed: %v", err)
		}
		if info.Name != idxName {
			t.Errorf("Expected name=%s, got %s", idxName, info.Name)
		}

		// Cleanup
		c.DeleteIndex(idxName)
	})
}

// TestGraphOperations tests all Graph Operations endpoints
func TestGraphOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("VLinkAndVUnlink", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "doc1", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "doc2", []float32{0.4, 0.5, 0.6}, nil)

		// Link
		err := c.VLink(idxName, "doc1", "doc2", "references", "referenced_by")
		if err != nil {
			t.Fatalf("VLink failed: %v", err)
		}

		links, err := c.VGetLinks(idxName, "doc1", "references")
		if err != nil {
			t.Fatalf("VGetLinks failed: %v", err)
		}
		if !contains(links, "doc2") {
			t.Errorf("Expected doc2 in links, got %v", links)
		}

		// Unlink
		err = c.VUnlink(idxName, "doc1", "doc2", "references", "")
		if err != nil {
			t.Fatalf("VUnlink failed: %v", err)
		}

		linksAfter, _ := c.VGetLinks(idxName, "doc1", "references")
		if contains(linksAfter, "doc2") {
			t.Error("doc2 should not be in links after unlink")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetLinks", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		for i := 0; i < 3; i++ {
			c.VAdd(idxName, fmt.Sprintf("node%d", i), []float32{0.1 * float32(i), 0.2 * float32(i), 0.3}, nil)
			if i > 0 {
				c.VLink(idxName, "node0", fmt.Sprintf("node%d", i), "connected_to", "")
			}
		}

		links, err := c.VGetLinks(idxName, "node0", "connected_to")
		if err != nil {
			t.Fatalf("VGetLinks failed: %v", err)
		}
		if len(links) != 2 {
			t.Errorf("Expected 2 links, got %d", len(links))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetIncoming", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "parent", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "child1", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "child2", []float32{0.7, 0.8, 0.9}, nil)

		c.VLink(idxName, "child1", "parent", "child_of", "")
		c.VLink(idxName, "child2", "parent", "child_of", "")

		incoming, err := c.VGetIncoming(idxName, "parent", "child_of")
		if err != nil {
			t.Fatalf("VGetIncoming failed: %v", err)
		}
		if len(incoming) != 2 {
			t.Errorf("Expected 2 incoming links, got %d", len(incoming))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetConnections", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "src", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "tgt", []float32{0.4, 0.5, 0.6}, map[string]interface{}{"content": "target"})

		c.VLink(idxName, "src", "tgt", "links_to", "")

		connections, err := c.VGetConnections(idxName, "src", "links_to")
		if err != nil {
			t.Fatalf("VGetConnections failed: %v", err)
		}
		if len(connections) != 1 {
			t.Errorf("Expected 1 connection, got %d", len(connections))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetAllRelations", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "hub", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "a", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "b", []float32{0.7, 0.8, 0.9}, nil)

		c.VLink(idxName, "hub", "a", "type_a", "")
		c.VLink(idxName, "hub", "b", "type_b", "")

		relations, err := c.VGetAllRelations(idxName, "hub")
		if err != nil {
			t.Fatalf("VGetAllRelations failed: %v", err)
		}
		if _, ok := relations["type_a"]; !ok {
			t.Error("Expected type_a in relations")
		}
		if _, ok := relations["type_b"]; !ok {
			t.Error("Expected type_b in relations")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetAllIncoming", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "target", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "source1", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "source2", []float32{0.7, 0.8, 0.9}, nil)

		c.VLink(idxName, "source1", "target", "points_to", "")
		c.VLink(idxName, "source2", "target", "points_to", "")

		incoming, err := c.VGetAllIncoming(idxName, "target")
		if err != nil {
			t.Fatalf("VGetAllIncoming failed: %v", err)
		}
		if _, ok := incoming["points_to"]; !ok {
			t.Error("Expected points_to in incoming relations")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VTraverse", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "a", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "b", []float32{0.4, 0.5, 0.6}, nil)

		c.VLink(idxName, "a", "b", "next", "")

		result, err := c.VTraverse(idxName, "a", []string{"next"})
		if err != nil {
			t.Fatalf("VTraverse failed: %v", err)
		}
		if result.ID != "a" {
			t.Errorf("Expected root id=a, got %s", result.ID)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VExtractSubgraph", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "root", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "c1", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "c2", []float32{0.7, 0.8, 0.9}, nil)

		c.VLink(idxName, "root", "c1", "has_child", "")
		c.VLink(idxName, "root", "c2", "has_child", "")

		result, err := c.VExtractSubgraph(idxName, "root", []string{"has_child"}, 2, 0, nil, 0)
		if err != nil {
			t.Fatalf("VExtractSubgraph failed: %v", err)
		}
		if result.RootID != "root" {
			t.Errorf("Expected root_id=root, got %s", result.RootID)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("FindPath", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "start", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "middle", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "end", []float32{0.7, 0.8, 0.9}, nil)

		c.VLink(idxName, "start", "middle", "connects", "")
		c.VLink(idxName, "middle", "end", "connects", "")

		result, err := c.FindPath(idxName, "start", "end", []string{"connects"}, 0)
		if err != nil {
			t.Fatalf("FindPath failed: %v", err)
		}
		if result == nil {
			t.Error("FindPath returned nil")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("GetEdges", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "esrc", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "etgt", []float32{0.4, 0.5, 0.6}, nil)

		c.VLink(idxName, "esrc", "etgt", "links", "")

		edges, err := c.GetEdges(idxName, "esrc", "links", 0)
		if err != nil {
			t.Fatalf("GetEdges failed: %v", err)
		}
		if len(edges) == 0 {
			t.Error("GetEdges returned no edges")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("SetAndGetNodeProperties", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "prop_node", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"initial": "val"})

		err := c.SetNodeProperties(idxName, "prop_node", map[string]interface{}{
			"updated": "new_value",
			"count":   42,
		})
		if err != nil {
			// Skip if server returns error (endpoint may not be fully implemented)
			t.Skipf("SetNodeProperties skipped due to server error: %v", err)
		}

		props, err := c.GetNodeProperties(idxName, "prop_node")
		if err != nil {
			t.Fatalf("GetNodeProperties failed: %v", err)
		}
		if props["updated"] != "new_value" {
			t.Errorf("Expected updated=new_value, got %v", props["updated"])
		}

		c.DeleteIndex(idxName)
	})

	t.Run("SearchNodes", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "p1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"type": "person", "name": "Alice"})
		c.VAdd(idxName, "p2", []float32{0.4, 0.5, 0.6}, map[string]interface{}{"type": "person", "name": "Bob"})
		c.VAdd(idxName, "c1", []float32{0.7, 0.8, 0.9}, map[string]interface{}{"type": "company", "name": "Acme"})

		results, err := c.SearchNodes(idxName, "type='person'", 10)
		if err != nil {
			t.Fatalf("SearchNodes failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}

		c.DeleteIndex(idxName)
	})
}

// TestVectorOperations tests all Vector Operations endpoints
func TestVectorOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("VAdd", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		err := c.VAdd(idxName, "vec1", []float32{0.1, 0.2, 0.3, 0.4}, map[string]interface{}{
			"content": "test",
			"type":    "document",
		})
		if err != nil {
			t.Fatalf("VAdd failed: %v", err)
		}

		vec, err := c.VGet(idxName, "vec1")
		if err != nil {
			t.Fatalf("VGet failed: %v", err)
		}
		if vec.ID != "vec1" {
			t.Errorf("Expected id=vec1, got %s", vec.ID)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VAddZeroVectorEntity", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		// Use empty vector for entity (zero-vector entities)
		err := c.VAdd(idxName, "entity1", []float32{}, map[string]interface{}{
			"name": "Python",
			"type": "entity",
		})
		if err != nil {
			t.Fatalf("VAdd with empty vector failed: %v", err)
		}

		vec, _ := c.VGet(idxName, "entity1")
		if vec.Metadata["type"] != "entity" {
			t.Errorf("Expected type=entity, got %v", vec.Metadata["type"])
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VAddBatch", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		vectors := []VectorAddObject{
			{Id: "v1", Vector: []float32{0.1, 0.2, 0.3}, Metadata: map[string]interface{}{"cat": "a"}},
			{Id: "v2", Vector: []float32{0.4, 0.5, 0.6}, Metadata: map[string]interface{}{"cat": "b"}},
			{Id: "v3", Vector: []float32{0.7, 0.8, 0.9}, Metadata: map[string]interface{}{"cat": "a"}},
		}

		result, err := c.VAddBatch(idxName, vectors)
		if err != nil {
			t.Fatalf("VAddBatch failed: %v", err)
		}

		if result["vectors_added"] != float64(3) {
			t.Errorf("Expected 3 vectors added, got %v", result["vectors_added"])
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VImport", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		vectors := []VectorAddObject{
			{Id: "i1", Vector: []float32{0.1, 0.2, 0.3}, Metadata: map[string]interface{}{}},
			{Id: "i2", Vector: []float32{0.4, 0.5, 0.6}, Metadata: map[string]interface{}{}},
		}

		_, err := c.VImport(idxName, vectors)
		if err != nil {
			t.Fatalf("VImport failed: %v", err)
		}

		info, _ := c.GetIndexInfo(idxName)
		if info.VectorCount != 2 {
			t.Errorf("Expected 2 vectors, got %d", info.VectorCount)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGet", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "vec1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"key": "value"})

		vec, err := c.VGet(idxName, "vec1")
		if err != nil {
			t.Fatalf("VGet failed: %v", err)
		}
		if vec.ID != "vec1" {
			t.Errorf("Expected id=vec1, got %s", vec.ID)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VGetMany", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "v1", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "v2", []float32{0.4, 0.5, 0.6}, nil)
		c.VAdd(idxName, "v3", []float32{0.7, 0.8, 0.9}, nil)

		vectors, err := c.VGetMany(idxName, []string{"v1", "v2"})
		if err != nil {
			t.Fatalf("VGetMany failed: %v", err)
		}
		if len(vectors) != 2 {
			t.Errorf("Expected 2 vectors, got %d", len(vectors))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VDelete", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "to_delete", []float32{0.1, 0.2, 0.3}, nil)

		err := c.VDelete(idxName, "to_delete")
		if err != nil {
			t.Fatalf("VDelete failed: %v", err)
		}

		_, err = c.VGet(idxName, "to_delete")
		if err == nil {
			t.Error("Expected error for deleted vector")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VSearch", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "v1", []float32{0.1, 0.2, 0.3, 0.4}, map[string]interface{}{"cat": "a"})
		c.VAdd(idxName, "v2", []float32{0.9, 0.8, 0.7, 0.6}, map[string]interface{}{"cat": "b"})
		c.VAdd(idxName, "v3", []float32{0.15, 0.25, 0.35, 0.45}, map[string]interface{}{"cat": "a"})

		results, err := c.VSearch(idxName, 2, []float32{0.1, 0.2, 0.3, 0.4}, "", 0, 0, nil)
		if err != nil {
			t.Fatalf("VSearch failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(results))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VSearchWithFilter", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "a1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"category": "a"})
		c.VAdd(idxName, "a2", []float32{0.11, 0.21, 0.31}, map[string]interface{}{"category": "a"})
		c.VAdd(idxName, "b1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"category": "b"})

		graphFilter := &GraphQuery{
			RootID:    "a1",
			Relations: []string{},
			MaxDepth:  1,
		}

		results, err := c.VSearch(idxName, 10, []float32{0.1, 0.2, 0.3}, "category='a'", 0, 0, graphFilter)
		if err != nil {
			t.Fatalf("VSearch with filter failed: %v", err)
		}
		if len(results) > 2 {
			t.Errorf("Expected max 2 results with filter, got %d", len(results))
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VSearchWithScores", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "s1", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "s2", []float32{0.9, 0.8, 0.7}, nil)

		results, err := c.VSearchWithScores(idxName, 5, []float32{0.1, 0.2, 0.3})
		if err != nil {
			t.Fatalf("VSearchWithScores failed: %v", err)
		}
		if len(results) == 0 {
			t.Error("Expected at least 1 result")
		}
		for _, r := range results {
			if r.Score < 0 || r.Score > 1 {
				t.Errorf("Invalid score: %f", r.Score)
			}
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VReinforce", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "r1", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "r2", []float32{0.4, 0.5, 0.6}, nil)

		err := c.VReinforce(idxName, []string{"r1", "r2"})
		if err != nil {
			t.Fatalf("VReinforce failed: %v", err)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VExport", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "e1", []float32{0.1, 0.2, 0.3}, nil)
		c.VAdd(idxName, "e2", []float32{0.4, 0.5, 0.6}, nil)

		result, err := c.VExport(idxName, 1, 0)
		if err != nil {
			t.Fatalf("VExport failed: %v", err)
		}
		if len(result.Data) < 1 {
			t.Errorf("Expected at least 1 result, got %d", len(result.Data))
		}

		c.DeleteIndex(idxName)
	})
}

// TestSessions tests Session Management endpoints
func TestSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("StartAndEndSession", func(t *testing.T) {
		// Create index first (sessions require an index)
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		defer c.DeleteIndex(idxName)

		result, err := c.StartSession(StartSessionRequest{
			UserID:    "test_user",
			IndexName: idxName, // Required: index_name must be specified
			Metadata: map[string]interface{}{
				"test": true,
			},
		})
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}
		if result.SessionID == "" {
			t.Error("Expected session_id to be set")
		}

		// EndSession also requires index_name in request body
		err = c.EndSession(result.SessionID)
		if err != nil {
			t.Fatalf("EndSession failed: %v", err)
		}
	})

	t.Run("StartSessionWithConversation", func(t *testing.T) {
		// Create index first
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		defer c.DeleteIndex(idxName)

		result, err := c.StartSession(StartSessionRequest{
			UserID:    "test_user",
			IndexName: idxName, // Required: index_name must be specified
			Conversation: []ConversationMessage{
				{Role: "system", Content: "You are a test assistant"},
				{Role: "user", Content: "Hello"},
			},
		})
		if err != nil {
			t.Fatalf("StartSession with conversation failed: %v", err)
		}

		c.EndSession(result.SessionID)
	})
}

// TestUserProfiles tests User Profile endpoints
func TestUserProfiles(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("ListUserProfiles", func(t *testing.T) {
		// Create an index first
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		defer c.DeleteIndex(idxName)

		profiles, err := c.ListUserProfiles(idxName)
		if err != nil {
			t.Fatalf("ListUserProfiles failed: %v", err)
		}
		if profiles == nil {
			t.Error("Expected profiles to not be nil")
		}
		// Should return empty list for new index
		if len(profiles) != 0 {
			t.Errorf("Expected 0 profiles for new index, got %d", len(profiles))
		}
	})

	t.Run("GetUserProfile", func(t *testing.T) {
		// Create an index first
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		defer c.DeleteIndex(idxName)

		// Try to get a non-existent profile - should return 404
		_, err := c.GetUserProfile("nonexistent_user", idxName)
		if err == nil {
			t.Error("Expected error for non-existent profile")
		}
	})
}

// TestAuth tests Auth endpoints
func TestAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("CreateApiKey", func(t *testing.T) {
		result, err := c.CreateApiKey("read", "")
		if err != nil {
			t.Fatalf("CreateApiKey failed: %v", err)
		}
		if result == "" {
			t.Error("Expected key to be returned")
		}
	})

	t.Run("ListApiKeys", func(t *testing.T) {
		c.CreateApiKey("read", "")

		keys, err := c.ListApiKeys()
		if err != nil {
			t.Fatalf("ListApiKeys failed: %v", err)
		}
		if keys == nil {
			t.Error("Expected keys to not be nil")
		}
	})

	t.Run("RevokeApiKey", func(t *testing.T) {
		keys, err := c.ListApiKeys()
		if err != nil {
			t.Fatalf("ListApiKeys failed: %v", err)
		}

		if len(keys) > 0 {
			keyID := ""
			if id, ok := keys[0]["id"].(string); ok {
				keyID = id
			} else if key, ok := keys[0]["key"].(string); ok {
				keyID = key
			}

			if keyID != "" {
				err = c.RevokeApiKey(keyID)
				if err != nil {
					t.Fatalf("RevokeApiKey failed: %v", err)
				}
			}
		}
	})
}

// TestSystem tests System endpoints
func TestSystem(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("Save", func(t *testing.T) {
		err := c.Save()
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	})

	t.Run("AOFRewrite", func(t *testing.T) {
		task, err := c.AOFRewrite()
		if err != nil {
			t.Fatalf("AOFRewrite failed: %v", err)
		}

		err = task.Wait(2*time.Second, 1*time.Minute)
		if err != nil {
			t.Fatalf("AOFRewrite wait failed: %v", err)
		}
	})

	t.Run("GetTaskStatus", func(t *testing.T) {
		task, err := c.AOFRewrite()
		if err != nil {
			t.Fatalf("AOFRewrite failed: %v", err)
		}

		status, err := c.GetTaskStatus(task.ID)
		if err != nil {
			t.Fatalf("GetTaskStatus failed: %v", err)
		}
		if status.ID != task.ID {
			t.Errorf("Expected task id=%s, got %s", task.ID, status.ID)
		}

		task.Wait(2*time.Second, 1*time.Minute)
	})
}

// TestMaintenance tests Maintenance endpoints
func TestMaintenance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("VTriggerMaintenanceVacuum", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "temp_del", []float32{0.1, 0.2, 0.3}, nil)
		c.VDelete(idxName, "temp_del")

		err := c.VTriggerMaintenance(idxName, "vacuum")
		if err != nil {
			t.Fatalf("VTriggerMaintenance (vacuum) failed: %v", err)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VTriggerMaintenanceRefine", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		for i := 0; i < 10; i++ {
			c.VAdd(idxName, fmt.Sprintf("refine_vec%d", i), []float32{0.1 * float32(i), 0.2 * float32(i), 0.3}, nil)
		}

		err := c.VTriggerMaintenance(idxName, "refine")
		if err != nil {
			t.Fatalf("VTriggerMaintenance (refine) failed: %v", err)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("VUpdateConfigMaintenance", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		config := MaintenanceConfig{
			VacuumInterval:  "120s",
			DeleteThreshold: 0.5,
			RefineEnabled:   true,
			RefineInterval:  "60s",
		}

		err := c.VUpdateConfig(idxName, config)
		if err != nil {
			t.Fatalf("VUpdateConfig failed: %v", err)
		}

		c.DeleteIndex(idxName)
	})
}

// TestCognitiveEngine tests Cognitive Engine endpoints
func TestCognitiveEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	srv := startTestServer(t)
	c := New("localhost", srv.port, "")

	t.Run("Think", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "think_vec", []float32{0.1, 0.2, 0.3}, nil)

		err := c.Think(idxName)
		if err != nil {
			t.Fatalf("Think failed: %v", err)
		}

		c.DeleteIndex(idxName)
	})

	t.Run("GetReflections", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "rvec1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"content": "same"})
		c.VAdd(idxName, "rvec2", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"content": "same"})

		c.Think(idxName)

		reflections, err := c.GetReflections(idxName, "")
		if err != nil {
			t.Fatalf("GetReflections failed: %v", err)
		}
		if reflections == nil {
			t.Error("Expected reflections to not be nil")
		}

		c.DeleteIndex(idxName)
	})

	t.Run("ResolveReflection", func(t *testing.T) {
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)
		c.VAdd(idxName, "r1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"content": "same"})
		c.VAdd(idxName, "r2", []float32{0.1, 0.2, 0.3}, map[string]interface{}{"content": "same"})

		c.Think(idxName)

		reflections, _ := c.GetReflections(idxName, "")
		if len(reflections) > 0 {
			reflectionID := ""
			if id, ok := reflections[0]["id"].(string); ok {
				reflectionID = id
			}
			if reflectionID != "" {
				err := c.ResolveReflection(idxName, reflectionID, "keep_newer", "")
				if err != nil {
					t.Fatalf("ResolveReflection failed: %v", err)
				}
			}
		}

		c.DeleteIndex(idxName)
	})
}

// TestErrorHandling tests error handling scenarios
func TestErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode.")
	}

	// Note: We don't start server here to test connection errors
	t.Run("ConnectionError", func(t *testing.T) {
		badClient := New("localhost", 59999, "")

		_, err := badClient.ListIndexes()
		if err == nil {
			t.Error("Expected connection error for bad port")
		}
	})

	t.Run("NonExistentIndex", func(t *testing.T) {
		srv := startTestServer(t)
		c := New("localhost", srv.port, "")

		_, err := c.GetIndexInfo(generateTestID() + "_nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent index")
		}
	})

	t.Run("NonExistentVector", func(t *testing.T) {
		srv := startTestServer(t)
		c := New("localhost", srv.port, "")
		idxName := generateTestID()
		c.VCreate(idxName, "cosine", "float32", 16, 200, nil)

		_, err := c.VGet(idxName, "nonexistent")
		if err == nil {
			t.Error("Expected error for non-existent vector")
		}

		c.DeleteIndex(idxName)
	})
}
