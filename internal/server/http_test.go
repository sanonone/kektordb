package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

func TestHealthzEndpoint(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":9092", "", "test-secret-token", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error)
	go func() {
		errCh <- s.Run()
	}()

	time.Sleep(500 * time.Millisecond)

	resp, err := http.Get("http://localhost:9092/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("healthz expected 200, got %d", resp.StatusCode)
	}

	resp, err = http.Get("http://localhost:9092/vector/indexes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("protected expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest("GET", "http://localhost:9092/vector/indexes", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Add("Authorization", "Bearer test-secret-token")

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("protected with token expected 200, got %d", resp.StatusCode)
	}

	// Clean shutdown
	s.Shutdown()
	<-errCh
}

// TestKUpperBoundRejected verifies that K values exceeding maxK are rejected with 400.
func TestKUpperBoundRejected(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":0", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	// Create an index first
	body := strings.NewReader(`{"index_name":"test","metric":"cosine","m":8,"ef_construction":100,"precision":"float32"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/create", body)
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()

	vectors := `[` + strings.Repeat(`{"id":"a","vector":[1.0,2.0]},`, 1) + `]`
	req2, _ := http.NewRequest("POST", ts.URL+"/vector/actions/add-batch", strings.NewReader(`{"index_name":"test","vectors":`+vectors+`}`))
	resp2, _ := ts.Client().Do(req2)
	resp2.Body.Close()

	// Test K > maxK
	searchBody := fmt.Sprintf(`{"index_name":"test","k":%d,"query_vector":[1.0,2.0]}`, maxK+1)
	req3, _ := http.NewRequest("POST", ts.URL+"/vector/actions/search", strings.NewReader(searchBody))
	resp3, err := ts.Client().Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for K > maxK, got %d", resp3.StatusCode)
	}
}

// TestBatchSizeLimitRejected verifies that batch sizes exceeding maxBatchSize are rejected.
func TestBatchSizeLimitRejected(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":0", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	// Create index first
	body := strings.NewReader(`{"index_name":"test2","metric":"cosine","m":8,"ef_construction":100,"precision":"float32"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/create", body)
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()

	// Build oversized batch (use repeated IDs for efficiency, validation rejects before engine call)
	var buf bytes.Buffer
	buf.WriteString(`{"index_name":"test2","vectors":[`)
	for i := 0; i < maxBatchSize; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"id":"v0","vector":[1.0,2.0]}`)
	}
	// Add one extra to exceed the limit
	buf.WriteString(`,{"id":"v1","vector":[1.0,2.0]}`)
	buf.WriteString(`]}`)
	batchBody := buf.String()

	req2, _ := http.NewRequest("POST", ts.URL+"/vector/actions/add-batch", strings.NewReader(batchBody))
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for batch > maxBatchSize, got %d", resp2.StatusCode)
	}
}

// TestVectorDimLimitRejected verifies that vectors exceeding maxVectorDim are rejected.
func TestVectorDimLimitRejected(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":0", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	// Create index first
	body := strings.NewReader(`{"index_name":"test3","metric":"cosine","m":8,"ef_construction":100,"precision":"float32"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/create", body)
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()

	// Build oversized vector (dim > maxVectorDim)
	bigVec := make([]float32, maxVectorDim+1)
	for i := range bigVec {
		bigVec[i] = 1.0
	}
	vecJSON, _ := json.Marshal(bigVec)
	addBody := fmt.Sprintf(`{"index_name":"test3","id":"big","vector":%s}`, vecJSON)
	req2, _ := http.NewRequest("POST", ts.URL+"/vector/actions/add", strings.NewReader(addBody))
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for vector dim > maxVectorDim, got %d", resp2.StatusCode)
	}
}

// TestBodySizeLimit verifies that oversized request bodies are rejected by the middleware.
func TestBodySizeLimit(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":0", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(s.httpServer.Handler)
	defer ts.Close()

	// Send a body larger than defaultMaxBodySize (10 MB)
	oversized := make([]byte, defaultMaxBodySize+1024)
	for i := range oversized {
		oversized[i] = 'x'
	}

	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/search", bytes.NewReader(oversized))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// The oversized body should be rejected (400=bad request or 413=too large)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("oversized body should be rejected, got %d", resp.StatusCode)
	}
}

// TestServerTimeoutsSet verifies that http.Server has timeouts configured.
func TestServerTimeoutsSet(t *testing.T) {
	testDir := t.TempDir()
	opts := engine.DefaultOptions(testDir)
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := NewServer(eng, ":0", "", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if s.httpServer.ReadTimeout == 0 {
		t.Error("ReadTimeout not set on http.Server")
	}
	if s.httpServer.WriteTimeout == 0 {
		t.Error("WriteTimeout not set on http.Server")
	}
	if s.httpServer.IdleTimeout == 0 {
		t.Error("IdleTimeout not set on http.Server")
	}
	if s.httpServer.MaxHeaderBytes == 0 {
		t.Error("MaxHeaderBytes not set on http.Server")
	}
}

// --- Knowledge Engine (Compiler) HTTP Tests ---

type fakeEmbedder struct{ dim int }

func (f *fakeEmbedder) Embed(text string) ([]float32, error) {
	vec := make([]float32, f.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func newTestServer(t *testing.T) (*httptest.Server, *engine.Engine) {
	return newTestServerWithEmbedder(t, nil)
}

func newTestServerWithEmbedder(t *testing.T, emb embeddings.Embedder) (*httptest.Server, *engine.Engine) {
	t.Helper()
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	srv, err := NewServer(eng, ":0", "", "", tmpDir, "", emb)
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.httpServer.Handler)
	t.Cleanup(func() { ts.Close() })
	return ts, eng
}

func prepareTestData(t *testing.T, eng *engine.Engine, indexName string) {
	t.Helper()
	err := eng.VCreate(indexName, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	vec := make([]float32, 384)
	eng.VAdd(indexName, "user:alice", vec, map[string]any{
		"type": "user", "entity_id": "alice", "name": "Alice", "_pinned": true,
	})
	eng.VAdd(indexName, "user:alice:mem1", vec, map[string]any{
		"type": "memory", "content": "Alice prefers concise code",
	})
	eng.VLink(indexName, "user:alice", "user:alice:mem1", "has_interaction", "interaction_of", 1.0, nil)
}

func TestCompileEntityCardEndpoint(t *testing.T) {
	ts, eng := newTestServer(t)
	prepareTestData(t, eng, "test_compile")

	body := strings.NewReader(`{
		"name": "entity_card",
		"sources": {"type": "graph_query", "entity": {"type": "user", "id": "alice"}, "depth": 2},
		"index_name": "test_compile"
	}`)
	resp, err := ts.Client().Post(ts.URL+"/compile", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var artifact map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact["name"] != "entity_card" {
		t.Errorf("expected name 'entity_card', got %v", artifact["name"])
	}
	if _, ok := artifact["data"]; !ok {
		t.Error("artifact missing data field")
	}
}

func TestCompileWithTemplate(t *testing.T) {
	ts, eng := newTestServer(t)
	prepareTestData(t, eng, "test_tmpl")

	body := strings.NewReader(`{
		"name": "project_summary",
		"template": "project_summary",
		"sources": {"type": "graph_query", "entity": {"type": "project", "id": "alice"}, "depth": 1},
		"index_name": "test_tmpl"
	}`)
	resp, err := ts.Client().Post(ts.URL+"/compile", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}
}

func TestCompileTemplatesEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/compile/templates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if _, ok := result["templates"]; !ok {
		t.Error("missing templates field")
	}
	if names, ok := result["names"].([]any); ok {
		if len(names) < 5 {
			t.Errorf("expected at least 5 templates, got %d", len(names))
		}
	}
}

func TestArtifactsEndpoint(t *testing.T) {
	ts, eng := newTestServer(t)
	prepareTestData(t, eng, "test_arts")

	// Compile an artifact first
	body := strings.NewReader(`{
		"name": "entity_card",
		"sources": {"type": "graph_query", "entity": {"type": "user", "id": "alice"}, "depth": 2},
		"index_name": "test_arts"
	}`)
	resp, _ := ts.Client().Post(ts.URL+"/compile", "application/json", body)
	resp.Body.Close()

	// List artifacts
	resp2, err := ts.Client().Get(ts.URL + "/artifacts?index=test_arts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	count, _ := result["count"].(float64)
	if count < 1 {
		t.Errorf("expected at least 1 artifact, got %v", count)
	}
}

func TestGetArtifactEndpoint(t *testing.T) {
	ts, eng := newTestServer(t)
	prepareTestData(t, eng, "test_get")

	// Compile first
	body := strings.NewReader(`{
		"name": "entity_card",
		"sources": {"type": "graph_query", "entity": {"type": "user", "id": "alice"}, "depth": 2},
		"index_name": "test_get"
	}`)
	resp, _ := ts.Client().Post(ts.URL+"/compile", "application/json", body)
	resp.Body.Close()

	// Get the artifact
	resp2, err := ts.Client().Get(ts.URL + "/artifact/entity_card?entity_type=user&entity_id=alice&index=test_get")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 200, got %d: %s", resp2.StatusCode, string(respBody))
	}

	var artifact map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&artifact); err != nil {
		t.Fatal(err)
	}
	if artifact["name"] != "entity_card" {
		t.Errorf("expected 'entity_card', got %v", artifact["name"])
	}
}

func TestGetArtifactNotFound(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/artifact/nonexistent?entity_type=user&entity_id=nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing artifact, got %d", resp.StatusCode)
	}
}

func TestCompileStatusNotFound(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/compile/status?task_id=nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing task, got %d", resp.StatusCode)
	}
}

func TestCompileValidateEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)

	// Valid request
	body := strings.NewReader(`{
		"name": "test",
		"sources": {"type": "graph_query", "entity": {"type": "user", "id": "test"}, "depth": 1}
	}`)
	resp, err := ts.Client().Post(ts.URL+"/compile/validate", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Invalid request (missing name)
	body2 := strings.NewReader(`{"sources": {"type": "graph_query", "entity": {"type": "user", "id": "test"}}}`)
	resp2, err := ts.Client().Post(ts.URL+"/compile/validate", "application/json", body2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", resp2.StatusCode)
	}
}

func TestTransferMemoryEndpoint(t *testing.T) {
	ts, eng := newTestServerWithEmbedder(t, &fakeEmbedder{dim: 384})

	src := "source_idx"
	dst := "target_idx"
	if err := eng.VCreate(src, distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VCreate(dst, distance.Cosine, 384, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}
	if err := eng.VAdd(src, "mem1", vec, map[string]any{"content": "test memory"}); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"source_index":"source_idx","target_index":"target_idx","query":"test","limit":10}`)
	resp, err := ts.Client().Post(ts.URL+"/transfer/memory", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		TransferredCount int    `json:"transferred_count"`
		Message          string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.TransferredCount == 0 {
		t.Errorf("expected at least one transfer, got %d", result.TransferredCount)
	}

	// Verify the transferred memory exists in the target index.
	if _, err := eng.VGet(dst, "mem1"); err != nil {
		t.Errorf("transferred memory not found in target: %v", err)
	}
}

func TestTransferMemoryEndpointValidation(t *testing.T) {
	ts, _ := newTestServerWithEmbedder(t, &fakeEmbedder{dim: 384})

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"missing source", `{"target_index":"t","query":"q"}`, http.StatusBadRequest},
		{"same source and target", `{"source_index":"x","target_index":"x","query":"q"}`, http.StatusBadRequest},
		{"missing query", `{"source_index":"s","target_index":"t"}`, http.StatusBadRequest},
		{"source not found", `{"source_index":"missing","target_index":"t","query":"q"}`, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := ts.Client().Post(ts.URL+"/transfer/memory", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestAuthEndpointsConditionallyRegistered(t *testing.T) {
	// JWT mode: auth endpoints are available.
	ts, _ := newTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/auth/keys")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Error("JWT mode: /auth/keys should be registered")
	}

	// OIDC mode: auth endpoints are not registered (server constructed with nil keyManager).
	// NewServer always uses the JWT provider, so we simulate OIDC by manually creating a server
	// with a nil keyManager and registering only the routes we need.
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	srv := &Server{Engine: eng, keyManager: nil}
	mux := http.NewServeMux()
	srv.registerHTTPHandlers(mux)
	ts2 := httptest.NewServer(mux)
	defer ts2.Close()

	resp2, err := ts2.Client().Get(ts2.URL + "/auth/keys")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("OIDC mode: /auth/keys should return 404, got %d", resp2.StatusCode)
	}

	resp3, err := ts2.Client().Get(ts2.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("OIDC mode: /.well-known/jwks.json should return 404, got %d", resp3.StatusCode)
	}
}
