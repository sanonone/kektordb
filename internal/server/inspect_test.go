package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// Tests for the inspect endpoint.
//
// Provenance is a graph, not a chain: a node can have several sources, those can
// have their own, and cycles are possible. The tests pin that the walk terminates,
// that it stops at the configured bounds, and that it reports truncation instead
// of quietly returning a partial tree.

// inspectTestServer builds a server with an index ready to receive nodes.
func inspectTestServer(t *testing.T) *Server {
	t.Helper()
	_, _, srv := kvTestEnv(t)
	if err := srv.Engine.VCreate("mem", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return srv
}

// addMemory stores a node and returns its ID.
func addMemory(t *testing.T, srv *Server, id, content string, meta map[string]any) {
	t.Helper()
	m := map[string]any{"content": content, "type": "memory", "_created_at": float64(time.Now().Unix())}
	for k, v := range meta {
		m[k] = v
	}
	if err := srv.Engine.VAdd("mem", id, []float32{1, 0, 0, 0}, m); err != nil {
		t.Fatalf("vadd %s: %v", id, err)
	}
}

// TestInspectBasic is the simple case: one memory, no provenance.
func TestInspectBasic(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "m1", "a simple memory", map[string]any{"memory_layer": "semantic"})

	resp := srv.BuildInspect("mem", "m1", 2)
	if !resp.Found {
		t.Fatalf("memory not found: %s", resp.Error)
	}
	if resp.Memory.Content != "a simple memory" {
		t.Errorf("content = %q", resp.Memory.Content)
	}
	if resp.Memory.Layer != "semantic" {
		t.Errorf("layer = %q", resp.Memory.Layer)
	}
	if resp.Memory.Dimensions != 4 {
		t.Errorf("dimensions = %d, want 4", resp.Memory.Dimensions)
	}
	if resp.Decay == nil {
		t.Error("no decay section for a timestamped memory")
	}
	if resp.Provenance == nil {
		t.Fatal("no provenance section")
	}
	if len(resp.Provenance.Tree) != 0 {
		t.Errorf("provenance has %d sources for an origin memory", len(resp.Provenance.Tree))
	}
}

// TestInspectNotFound is the error path.
func TestInspectNotFound(t *testing.T) {
	srv := inspectTestServer(t)
	resp := srv.BuildInspect("mem", "missing", 2)
	if resp.Found {
		t.Fatal("a missing memory was reported as found")
	}
	if resp.Error == "" {
		t.Error("no error message for a missing memory")
	}
}

// TestInspectProvenanceDepth1 shows only the direct sources.
func TestInspectProvenanceDepth1(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "master", "the merged truth", nil)
	addMemory(t, srv, "src1", "an older statement", nil)

	if err := srv.Engine.VLink("mem", "src1", "master", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatalf("vlink: %v", err)
	}

	resp := srv.BuildInspect("mem", "master", 1)
	if resp.Provenance == nil || len(resp.Provenance.Tree) != 1 {
		t.Fatalf("expected one source, got %+v", resp.Provenance)
	}
	src := resp.Provenance.Tree[0]
	if src.ID != "src1" {
		t.Errorf("source = %q, want src1", src.ID)
	}
	if src.Relation != "consolidated_into" {
		t.Errorf("relation = %q", src.Relation)
	}
	if !src.Exists {
		t.Error("source marked as missing although it exists")
	}
	if src.Content != "an older statement" {
		t.Errorf("source content = %q", src.Content)
	}
}

// TestInspectProvenanceDepth2 follows one more level.
func TestInspectProvenanceDepth2(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "top", "final", nil)
	addMemory(t, srv, "mid", "middle", nil)
	addMemory(t, srv, "bottom", "original", nil)

	if err := srv.Engine.VLink("mem", "bottom", "mid", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.Engine.VLink("mem", "mid", "top", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	resp := srv.BuildInspect("mem", "top", 2)
	if len(resp.Provenance.Tree) != 1 {
		t.Fatalf("expected one direct source, got %d", len(resp.Provenance.Tree))
	}
	first := resp.Provenance.Tree[0]
	if first.ID != "mid" {
		t.Fatalf("first source = %q, want mid", first.ID)
	}
	if len(first.Children) != 1 || first.Children[0].ID != "bottom" {
		t.Errorf("second level = %+v, want bottom", first.Children)
	}

	// With depth 1 the second level must not appear.
	shallow := srv.BuildInspect("mem", "top", 1)
	if len(shallow.Provenance.Tree[0].Children) != 0 {
		t.Error("depth 1 returned grandchildren")
	}
}

// TestInspectProvenanceHandlesCycles is the guard against an infinite walk: a
// graph can contain a loop, and a naive recursion would never return.
func TestInspectProvenanceHandlesCycles(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "a", "node a", nil)
	addMemory(t, srv, "b", "node b", nil)

	// a -> b and b -> a, both meaning "produced by".
	if err := srv.Engine.VLink("mem", "b", "a", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.Engine.VLink("mem", "a", "b", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var resp InspectResponse
	go func() {
		defer close(done)
		resp = srv.BuildInspect("mem", "a", 10)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("provenance walk did not terminate on a cyclic graph")
	}

	// Terminating is not enough: a cycle visited twice would produce the same
	// node in several branches and the total would grow, which is what the
	// visited set exists to prevent.
	seen := map[string]int{}
	var walk func(nodes []InspectSource)
	walk = func(nodes []InspectSource) {
		for _, n := range nodes {
			seen[n.ID]++
			walk(n.Children)
		}
	}
	walk(resp.Provenance.Tree)

	for id, count := range seen {
		if count > 1 {
			t.Errorf("node %q appears %d times in the provenance tree: the walk revisited it", id, count)
		}
	}
	if resp.Provenance.TotalNodes > 2 {
		t.Errorf("walk with two nodes produced %d entries", resp.Provenance.TotalNodes)
	}
}

// TestInspectProvenanceTruncates is the bound: many sources must not produce an
// unbounded tree, and the answer must say it was cut.
func TestInspectProvenanceTruncates(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "hub", "the hub", nil)

	const sources = inspectMaxNodes + 20
	for i := 0; i < sources; i++ {
		id := fmt.Sprintf("s%d", i)
		addMemory(t, srv, id, "source", nil)
		if err := srv.Engine.VLink("mem", id, "hub", "consolidated_into", "derived_from", 1.0, nil); err != nil {
			t.Fatalf("vlink %s: %v", id, err)
		}
	}

	resp := srv.BuildInspect("mem", "hub", 3)
	p := resp.Provenance
	if !p.Truncated {
		t.Errorf("walk with %d sources was not reported as truncated", sources)
	}
	if p.TotalNodes > inspectMaxNodes {
		t.Errorf("walk returned %d nodes, above the %d bound", p.TotalNodes, inspectMaxNodes)
	}
	if p.Note == "" {
		t.Error("truncation happened with no explanation")
	}
}

// TestInspectProvenanceMissingSource keeps the walk honest about a dangling edge:
// a link can point at a node that no longer exists.
func TestInspectProvenanceMissingSource(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "master", "survivor", nil)

	if err := srv.Engine.VLink("mem", "ghost", "master", "consolidated_into", "derived_from", 1.0, nil); err != nil {
		t.Fatal(err)
	}

	resp := srv.BuildInspect("mem", "master", 2)
	if len(resp.Provenance.Tree) != 1 {
		t.Fatalf("expected the dangling source to be listed, got %+v", resp.Provenance.Tree)
	}
	if resp.Provenance.Tree[0].Exists {
		t.Error("a missing source was reported as existing")
	}
}

// TestInspectDepthZero skips provenance entirely.
func TestInspectDepthZero(t *testing.T) {
	srv := inspectTestServer(t)
	addMemory(t, srv, "m", "content", nil)
	resp := srv.BuildInspect("mem", "m", 0)
	if resp.Provenance == nil {
		t.Fatal("provenance section missing even at depth 0")
	}
	if len(resp.Provenance.Tree) != 0 {
		t.Error("depth 0 returned sources")
	}
}

// TestMetadataFloatAcceptsEveryType guards the JSON-round-trip case: a value
// written as int64 in Go arrives as float64 after decoding.
func TestMetadataFloatAcceptsEveryType(t *testing.T) {
	meta := map[string]any{
		"f64": float64(1.5), "f32": float32(2.5), "i64": int64(3), "i": 4, "s": "nope",
	}
	for key, want := range map[string]float64{"f64": 1.5, "f32": 2.5, "i64": 3, "i": 4} {
		got, ok := metadataFloat(meta, key)
		if !ok || got != want {
			t.Errorf("metadataFloat(%q) = %v, %v; want %v, true", key, got, ok, want)
		}
	}
	if _, ok := metadataFloat(meta, "s"); ok {
		t.Error("a string was accepted as a number")
	}
	if _, ok := metadataFloat(meta, "absent"); ok {
		t.Error("an absent key was accepted")
	}
}
