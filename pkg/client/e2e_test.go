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
