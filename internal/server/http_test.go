package server

import (
	"net/http"
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

	s, err := NewServer(eng, ":9092", "", "test-secret-token")
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
