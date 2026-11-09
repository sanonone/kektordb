package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/cognitive"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// Tests for the get_status MCP tool.
//
// A coding agent told "you have a memory" should be able to answer "how is it
// doing?" with facts. These tests pin that the tool reports facts, that it stays
// quiet when nothing is wrong, and that a broken embedder is visible rather than
// silently degrading search.

func statusTestService(t *testing.T, gardener *cognitive.Gardener) (*Service, *engine.Engine) {
	t.Helper()
	opts := engine.DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	opts.MaintenanceInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	svc := NewService(eng, nil, nil, gardener)
	return svc, eng
}

// TestGetStatusReportsIndexes is the basic shape.
func TestGetStatusReportsIndexes(t *testing.T) {
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("mcp_memory", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := eng.VAdd("mcp_memory", string(rune('a'+i)), []float32{1, 0, 0, 0},
			map[string]any{"content": "memory"}); err != nil {
			t.Fatalf("vadd: %v", err)
		}
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if res.Memory == nil {
		t.Fatal("no memory section")
	}
	if res.Memory.TotalVectors != 3 {
		t.Errorf("total vectors = %d, want 3", res.Memory.TotalVectors)
	}
	if len(res.Memory.Indexes) != 1 || res.Memory.Indexes[0].Name != "mcp_memory" {
		t.Errorf("indexes = %+v", res.Memory.Indexes)
	}
	if res.Summary == "" {
		t.Error("no summary: the agent needs a line it can act on")
	}
}

// TestGetStatusWarnsOnEmptyIndex is the first warning rule.
func TestGetStatusWarnsOnEmptyIndex(t *testing.T) {
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("empty_idx", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "empty") && strings.Contains(w, "empty_idx") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one naming the empty index", res.Warnings)
	}
}

// TestGetStatusWarnsWhenEmbedderMissing: a missing embedder degrades semantic
// search, and the agent must be able to say so.
func TestGetStatusWarnsWhenEmbedderMissing(t *testing.T) {
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("idx", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "embedder") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one about the missing embedder", res.Warnings)
	}
}

// TestGetStatusReportsMaintenanceOff: the Gardener being absent is a fact worth
// stating, because it means duplicates accumulate.
func TestGetStatusReportsMaintenanceOff(t *testing.T) {
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("idx", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Maintenance == nil {
		t.Fatal("no maintenance section")
	}
	if res.Maintenance.Enabled {
		t.Error("maintenance reported as enabled with no Gardener configured")
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "maintenance") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one about maintenance being off", res.Warnings)
	}
}

// TestGetStatusWithGardener reports what each detector has done.
func TestGetStatusWithGardener(t *testing.T) {
	// The Gardener needs a real engine: it reads indexes on construction.
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("idx", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	g := cognitive.NewGardener(eng, nil, cognitive.Config{
		Enabled:       true,
		Interval:      time.Hour,
		Mode:          "advanced",
		TargetIndexes: []string{"idx"},
	})
	svc.gardener = g
	if err := eng.VAdd("idx", "a", []float32{1, 0, 0, 0}, map[string]any{"content": "x"}); err != nil {
		t.Fatal(err)
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Maintenance == nil || !res.Maintenance.Enabled {
		t.Fatal("maintenance not reported as enabled although a Gardener is configured")
	}
	if len(res.Maintenance.Features) == 0 {
		t.Error("no per-feature breakdown")
	}
}

// TestGetStatusSummaryIsReadable pins that the summary line is not empty and
// mentions the memory count, which is what an agent quotes back to the user.
func TestGetStatusSummaryIsReadable(t *testing.T) {
	svc, eng := statusTestService(t, nil)
	if err := eng.VCreate("idx", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	_, res, err := svc.GetStatus(context.Background(), nil, GetStatusArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "memories") {
		t.Errorf("summary = %q, want it to mention the memory count", res.Summary)
	}
	if !strings.Contains(res.Summary, "warnings") {
		t.Errorf("summary = %q, want it to mention the warning count", res.Summary)
	}
}

// TestRelativeTime keeps the ages readable.
func TestRelativeTime(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s ago"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, tt := range tests {
		if got := relativeTime(tt.d); got != tt.want {
			t.Errorf("relativeTime(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestGetStatusIsInProfiles checks the tool is reachable without being in the
// minimal profile, which is deliberately kept small.
func TestGetStatusIsInProfiles(t *testing.T) {
	if ProfileMinimal[ToolGetStatus] {
		t.Error("get_status is in the minimal profile: that profile is kept to eight tools " +
			"so the agent is not confused at first use")
	}
	if !ProfileStandard[ToolGetStatus] {
		t.Error("get_status is missing from the standard profile")
	}
	if !ProfileFull[ToolGetStatus] {
		t.Error("get_status is missing from the full profile")
	}
}
