package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	s, err := NewServer(eng, ":9092", "", "test-secret-token", "", "")
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

	s, err := NewServer(eng, ":0", "", "", "", "")
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

	s, err := NewServer(eng, ":0", "", "", "", "")
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

	s, err := NewServer(eng, ":0", "", "", "", "")
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

	s, err := NewServer(eng, ":0", "", "", "", "")
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

	s, err := NewServer(eng, ":0", "", "", "", "")
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
