package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// Tests for the aggregated /system/status view.
//
// The point of this endpoint is that one call answers "is this working?". The
// tests pin that it answers, that it survives a section failing, and that it
// stays quiet when there is nothing to report.

// statusTestServer builds a server with one index holding a few vectors.
func statusTestServer(t *testing.T, vectors int) *Server {
	t.Helper()
	_, _, srv := kvTestEnv(t)

	if err := srv.Engine.VCreate("mem", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	for i := 0; i < vectors; i++ {
		if err := srv.Engine.VAdd("mem", string(rune('a'+i)),
			[]float32{float32(i + 1), 0, 0, 0},
			map[string]any{"content": "memory"}); err != nil {
			t.Fatalf("vadd: %v", err)
		}
	}
	return srv
}

// TestSystemStatusHasEverySection checks the shape when everything works.
func TestSystemStatusHasEverySection(t *testing.T) {
	srv := statusTestServer(t, 3)

	st := srv.BuildSystemStatus()

	if st.Version == "" {
		t.Error("no version")
	}
	if st.Memory == nil {
		t.Fatal("memory section missing")
	}
	if len(st.Memory.Indexes) != 1 {
		t.Fatalf("got %d indexes, want 1", len(st.Memory.Indexes))
	}
	if st.Memory.Indexes[0].Name != "mem" {
		t.Errorf("index name = %q", st.Memory.Indexes[0].Name)
	}
	if st.Memory.TotalVectors != 3 {
		t.Errorf("total vectors = %d, want 3", st.Memory.TotalVectors)
	}
	if st.Maintenance == nil {
		t.Error("maintenance section missing")
	}
	if st.LLM == nil {
		t.Error("llm section missing")
	}
	if st.Health == nil {
		t.Fatal("health section missing")
	}
	if st.Health.Persistence == nil {
		t.Error("persistence missing from health")
	}
	if st.Health.Embedder == nil {
		t.Error("embedder missing from health")
	}
}

// TestSystemStatusWarnsOnEmptyIndex is the first warning rule.
func TestSystemStatusWarnsOnEmptyIndex(t *testing.T) {
	srv := statusTestServer(t, 0)

	st := srv.BuildSystemStatus()
	if len(st.Warnings) == 0 {
		t.Fatal("an empty index produced no warning")
	}
	found := false
	for _, w := range st.Warnings {
		if containsAll(w, "empty", "mem") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one mentioning the empty index", st.Warnings)
	}
}

// TestSystemStatusQuietWhenHealthy is the rule that keeps the view readable: no
// warnings means an empty list, not a list of reassurances.
func TestSystemStatusQuietWhenHealthy(t *testing.T) {
	srv := statusTestServer(t, 3)
	// A fresh snapshot and a pending-free state.
	if err := srv.Engine.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	st := srv.BuildSystemStatus()
	for _, w := range st.Warnings {
		// The only acceptable warning here would be about the embedder, which
		// this test server does not configure.
		if containsAll(w, "embedder") {
			continue
		}
		t.Errorf("unexpected warning on a healthy system: %q", w)
	}
}

// TestSystemStatusPersistsSnapshotAge covers the snapshot staleness rule, using
// the accessor added in M0.
func TestSystemStatusPersistsSnapshotAge(t *testing.T) {
	srv := statusTestServer(t, 2)

	st := srv.BuildSystemStatus()
	if st.Health.Persistence.EverSnapshotted {
		t.Error("EverSnapshotted is true before any snapshot")
	}

	if err := srv.Engine.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	st = srv.BuildSystemStatus()
	if !st.Health.Persistence.EverSnapshotted {
		t.Error("EverSnapshotted is false after a snapshot")
	}
	if st.Health.Persistence.LastSnapshotAgoS > 5 {
		t.Errorf("snapshot age = %ds right after taking one", st.Health.Persistence.LastSnapshotAgoS)
	}
}

// TestSystemStatusJSONRoundTrip pins that the shape survives serialisation,
// since the CLI and MCP both decode it.
func TestSystemStatusJSONRoundTrip(t *testing.T) {
	srv := statusTestServer(t, 3)
	st := srv.BuildSystemStatus()

	blob, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back SystemStatus
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, blob)
	}
	if back.Memory == nil || len(back.Memory.Indexes) != 1 {
		t.Errorf("indexes did not survive the round trip: %s", blob)
	}
	if back.Warnings == nil {
		t.Error("warnings became null; callers expect an array")
	}
	if back.Errors == nil {
		t.Error("errors became null; callers expect an array")
	}
}

// TestSystemStatusLLMNotConfigured keeps the honest case: no model means the
// section says so, rather than showing zeros that look like free usage.
func TestSystemStatusLLMNotConfigured(t *testing.T) {
	srv := statusTestServer(t, 1)
	st := srv.BuildSystemStatus()
	if st.LLM.Configured {
		t.Error("llm reported as configured on a server without a model")
	}
}

// TestSystemStatusHandlesStaleCycle is a guard: LastCycleAgoSecond must never be
// negative, which would happen if the clock moved backwards.
func TestSystemStatusNeverReportsNegativeAge(t *testing.T) {
	srv := statusTestServer(t, 1)
	st := srv.BuildSystemStatus()
	if st.Maintenance != nil && st.Maintenance.LastCycleAgoSecond < 0 {
		t.Errorf("last cycle age = %d, must not be negative", st.Maintenance.LastCycleAgoSecond)
	}
	if st.Health != nil && st.Health.Persistence != nil && st.Health.Persistence.LastSnapshotAgoS < 0 {
		t.Errorf("snapshot age = %d, must not be negative", st.Health.Persistence.LastSnapshotAgoS)
	}
	_ = time.Now
}

// containsAll reports whether s contains every fragment, case-insensitively.
func containsAll(s string, fragments ...string) bool {
	for _, f := range fragments {
		if !containsFoldStr(s, f) {
			return false
		}
	}
	return true
}

func containsFoldStr(s, sub string) bool {
	return len(sub) == 0 || indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := toLowerASCII(s), toLowerASCII(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if 'A' <= b[i] && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
