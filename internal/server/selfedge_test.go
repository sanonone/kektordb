package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestSelfEdgeIsAccepted covers a server API that refused what the engine writes.
//
// The Gardener records "this detector has already looked at me" by linking a node
// to itself: decay_analyzed, sentiment_analyzed, centrality_analyzed,
// entities_analyzed, evolution_analyzed. engine.VLink allows those, the HTTP
// handler rejected them. The asymmetry broke `kektordb import`: the markers are in
// every archive of a store that ran maintenance, and the import failed on each.
func TestSelfEdgeIsAccepted(t *testing.T) {
	// No auth token: this test is about the edge rule, not about credentials.
	handler, eng := openTestServer(t)
	if err := eng.VCreate("m", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd("m", "n1", []float32{1, 0, 0, 0}, map[string]any{"content": "x"}); err != nil {
		t.Fatalf("vadd: %v", err)
	}

	for _, rel := range []string{
		"decay_analyzed", "sentiment_analyzed", "centrality_analyzed",
		"entities_analyzed", "evolution_analyzed",
	} {
		t.Run(rel, func(t *testing.T) {
			body := `{"index_name":"m","source_id":"n1","target_id":"n1","relation_type":"` + rel + `"}`
			req := httptest.NewRequest(http.MethodPost, "/graph/actions/link", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s self-edge rejected with %d: %s", rel, rec.Code, rec.Body.String())
			}
			// The marker must be readable back: the detector uses it to skip.
			if rels := eng.VGetRelations("m", "n1"); len(rels[rel]) == 0 {
				t.Errorf("%s self-edge was accepted but not stored", rel)
			}
		})
	}
}

// openTestServer builds a server without authentication.
func openTestServer(t *testing.T) (http.Handler, *engine.Engine) {
	t.Helper()
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	srv, err := NewServer(eng, ":0", "", "", dir, "", nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv.httpServer.Handler, eng
}
