package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// Self-edges: allowed for the engine's own markers, refused for everything else.
//
// The Gardener records "this detector has already looked at me" by linking a node
// to itself. Those five relations must be accepted or an exported store cannot be
// reimported: the markers are in every archive of a store that ran maintenance.
//
// A caller's own self-link is still refused. `doc-1 -> doc-1` is a cycle that says
// nothing, and the API contract documents that rejection.
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

// TestUserSelfEdgeIsStillRejected keeps the validation the contract documents.
func TestUserSelfEdgeIsStillRejected(t *testing.T) {
	handler, eng := openTestServer(t)
	if err := eng.VCreate("m", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd("m", "n1", []float32{1, 0, 0, 0}, map[string]any{"content": "x"}); err != nil {
		t.Fatalf("vadd: %v", err)
	}

	for _, rel := range []string{"self_ref", "related_to", "mentions", "analyzed", "decay_analyzed_typo"} {
		t.Run(rel, func(t *testing.T) {
			body := `{"index_name":"m","source_id":"n1","target_id":"n1","relation_type":"` + rel + `"}`
			req := httptest.NewRequest(http.MethodPost, "/graph/actions/link", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("relation %q produced %d, want 400: a caller self-link is a mistake",
					rel, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "cannot link a node to itself") {
				t.Errorf("error message does not explain the problem: %s", rec.Body.String())
			}
		})
	}
}

// TestSelfEdgeAllowlistIsExact: a relation name that merely looks internal must
// not acquire the privilege.
func TestSelfEdgeAllowlistIsExact(t *testing.T) {
	for name := range selfEdgeRelations {
		if !strings.HasSuffix(name, "_analyzed") {
			t.Errorf("%q is in the allowlist but does not follow the marker convention", name)
		}
	}
	if selfEdgeRelations["analyzed"] {
		t.Error("the bare name \"analyzed\" was allowed: the allowlist must be exact")
	}
}
