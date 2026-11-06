package cognitive

import (
	"errors"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// failingLLM is an llm.Client that always returns an error, standing in for a
// timeout, a 429 or an expired key.
type failingLLM struct {
	Calls int
}

func (f *failingLLM) Chat(systemPrompt, userQuery string) (string, error) {
	f.Calls++
	return "", errors.New("simulated transient LLM failure")
}

func (f *failingLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	f.Calls++
	return "", errors.New("simulated transient LLM failure")
}

// newEvolutionEngine builds an index with one entity central enough for the
// knowledge-evolution detector to pick it up (in-degree >= 15, type "entity").
func newEvolutionEngine(t *testing.T) (*engine.Engine, string) {
	t.Helper()
	opts := engine.DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	const idx = "evolve"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd(idx, "entity_go", []float32{1, 0}, map[string]any{
		"type": "entity", "name": "Go", "_created_at": float64(time.Now().Unix()),
	}); err != nil {
		t.Fatalf("vadd entity: %v", err)
	}
	// 16 incoming "mentions" edges: above the in-degree threshold.
	for i := 0; i < 16; i++ {
		id := "mem_" + string(rune('a'+i))
		if err := eng.VAdd(idx, id, []float32{0.5, 0.5}, map[string]any{
			"type": "memory", "content": "about go",
			"_created_at": float64(time.Now().Add(-40 * 24 * time.Hour).Unix()),
		}); err != nil {
			t.Fatalf("vadd %s: %v", id, err)
		}
		if err := eng.VLink(idx, id, "entity_go", "mentions", "mentioned_by", 1.0, nil); err != nil {
			t.Fatalf("vlink %s: %v", id, err)
		}
	}
	return eng, idx
}

// TestEvolutionMarkerNotWrittenOnLLMFailure covers a marker written before the
// work it represents.
//
// detectKnowledgeEvolution writes the evolution_analyzed edge, then calls the
// LLM. Every failure after that point — the call itself, unparsable JSON, a
// missing timeline — hits `continue` with the marker already in place. The edge
// is never removed, so that entity is never analyzed again: one timeout costs
// the analysis permanently.
//
// The other LLM paths in this file (entity extraction, core facts,
// contradictions) all mark only after a successful parse. This one is the
// exception.
func TestEvolutionMarkerNotWrittenOnLLMFailure(t *testing.T) {
	eng, idx := newEvolutionEngine(t)
	mock := &failingLLM{}

	g := NewGardener(eng, mock, Config{
		Enabled:  true,
		Interval: time.Hour,
		Mode:     "advanced",
	})
	g.detectKnowledgeEvolution(idx)

	if mock.Calls == 0 {
		t.Fatal("the detector never reached the LLM: the test setup does not exercise the path")
	}
	t.Logf("LLM called %d time(s), all failed", mock.Calls)

	links, found := eng.VGetLinks(idx, "entity_go", "evolution_analyzed")
	if found && len(links) > 0 {
		t.Fatalf("entity_go is marked evolution_analyzed after a failed LLM call: "+
			"the marker was written before the work, so this entity will never be analyzed again "+
			"(edge: %v)", links)
	}

	// The entity must still be eligible: a second cycle must retry it.
	before := mock.Calls
	g.detectKnowledgeEvolution(idx)
	if mock.Calls == before {
		t.Error("the second cycle did not retry the entity: the marker (or a cursor) is blocking it")
	}
}

// TestEvolutionMarkerWrittenOnSuccess keeps the dedup: after a successful
// analysis the entity must not be processed again.
func TestEvolutionMarkerWrittenOnSuccess(t *testing.T) {
	eng, idx := newEvolutionEngine(t)

	var calls int
	successLLM := &countingReplyLLM{
		reply: `{"timeline": "phase1 -> phase2 -> phase3", "competency_level": "intermediate", "knowledge_gaps": ["generics"], "next_steps": "study generics"}`,
		calls: &calls,
	}

	g := NewGardener(eng, successLLM, Config{
		Enabled:  true,
		Interval: time.Hour,
		Mode:     "advanced",
	})
	g.detectKnowledgeEvolution(idx)

	if calls == 0 {
		t.Fatal("the detector never reached the LLM")
	}
	links, found := eng.VGetLinks(idx, "entity_go", "evolution_analyzed")
	if !found || len(links) == 0 {
		t.Fatal("a successful analysis did not mark the entity: it will be analyzed again every cycle")
	}
}

// TestEvolutionMarkerNotWrittenOnUnparsableReply is the second failure mode:
// the call succeeded but the answer is not usable.
func TestEvolutionMarkerNotWrittenOnUnparsableReply(t *testing.T) {
	eng, idx := newEvolutionEngine(t)

	var calls int
	garbageLLM := &countingReplyLLM{reply: "I cannot answer that.", calls: &calls}

	g := NewGardener(eng, garbageLLM, Config{
		Enabled:  true,
		Interval: time.Hour,
		Mode:     "advanced",
	})
	g.detectKnowledgeEvolution(idx)

	if calls == 0 {
		t.Fatal("the detector never reached the LLM")
	}
	links, found := eng.VGetLinks(idx, "entity_go", "evolution_analyzed")
	if found && len(links) > 0 {
		t.Fatal("entity_go is marked evolution_analyzed after an unparsable reply: " +
			"the analysis produced nothing but the entity is spent")
	}
}

// countingReplyLLM always answers with the same text.
type countingReplyLLM struct {
	reply string
	calls *int
}

func (c *countingReplyLLM) Chat(systemPrompt, userQuery string) (string, error) {
	*c.calls++
	return c.reply, nil
}

func (c *countingReplyLLM) ChatWithImages(systemPrompt, userQuery string, images [][]byte) (string, error) {
	*c.calls++
	return c.reply, nil
}
