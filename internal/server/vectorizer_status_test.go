package server

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/rag"
)

// TestGetStatusesReflectsReality covers a status endpoint that asserted health.
//
// IsRunning was the literal `true` for every configured vectorizer, so one whose
// pipeline never started — an embedder that could not be created, a RAG
// pipeline that failed — still reported itself as running. An operator watching
// this endpoint saw health that did not exist.
func TestGetStatusesReflectsReality(t *testing.T) {
	// With no pipelines at all, nothing can be running.
	empty := &VectorizerService{
		server: &Server{
			vectorizerConfig: &Config{
				Vectorizers: []VectorizerConfig{{Name: "never-started"}},
			},
		},
	}
	statuses := empty.GetStatuses()
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].IsRunning {
		t.Error("a vectorizer with no pipeline reports is_running=true: the value is asserted, not measured")
	}
	if statuses[0].CurrentState == "active" {
		t.Errorf("state = %q for a vectorizer with no pipeline", statuses[0].CurrentState)
	}
}

// TestGetStatusesReportsRunningPipelines keeps the positive case working.
func TestGetStatusesReportsRunningPipelines(t *testing.T) {
	vs := &VectorizerService{
		server: &Server{
			vectorizerConfig: &Config{
				Vectorizers: []VectorizerConfig{{Name: "ok"}},
			},
		},
	}
	// A non-nil slot means a pipeline exists.
	vs.pipelines = make([]*rag.Pipeline, 1)
	vs.pipelines[0] = &rag.Pipeline{}

	statuses := vs.GetStatuses()
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses", len(statuses))
	}
	if !statuses[0].IsRunning {
		t.Error("a vectorizer with a pipeline reports is_running=false")
	}
}
