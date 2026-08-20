package cognitive

import (
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestForceThinkDisabledGardenerDoesNotDeadlock reproduces P1-4: with the
// Gardener disabled (the default), Start() never runs and thinkReqs stays
// nil. ForceThink used to do a blocking send on the nil channel, hanging the
// MCP tool trigger_reflection / the HTTP reflection endpoint forever.
//
// Post-fix ForceThink must return immediately with a warning.
func TestForceThinkDisabledGardenerDoesNotDeadlock(t *testing.T) {
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.MaintenanceInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	if err := eng.VCreate("idx", distance.Cosine, 4, 10, distance.Float32, "", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	// Gardener disabled (default): Start() is never called, thinkReqs == nil.
	gardener := NewGardener(eng, nil, Config{Enabled: false, TargetIndexes: []string{"idx"}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		gardener.ForceThink("idx")
	}()

	select {
	case <-done:
		// OK: returned immediately.
	case <-time.After(3 * time.Second):
		t.Fatal("ForceThink blocked forever with Gardener disabled (P1-4)")
	}
}
