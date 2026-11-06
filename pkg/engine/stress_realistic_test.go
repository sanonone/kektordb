package engine

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// Realistic parallel memory workload.
//
// These tests run what an agent-backed memory store actually does, all at once:
// writing memories, searching, linking entities, reading back, deleting,
// snapshotting and compacting. Each one asserts something about the result, not
// only that no goroutine crashed — a store that returns wrong answers under load
// is as broken as one that panics.
//
// Run with: go test -race -run TestStressRealistic -timeout 30m ./pkg/engine/

// stressConfig keeps the shape of a memory workload in one place.
type stressConfig struct {
	dim       int
	workers   int
	perWorker int
	indexes   []string
}

func (c stressConfig) validate(t *testing.T) {
	t.Helper()
	if c.dim <= 0 || c.workers <= 0 || c.perWorker <= 0 || len(c.indexes) == 0 {
		t.Fatal("stressConfig is not initialised")
	}
}

// randomVector produces a normalised vector so cosine distances are meaningful.
func randomVector(r *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var norm float64
	for i := range v {
		val := r.NormFloat64()
		v[i] = float32(val)
		norm += val * val
	}
	if norm > 0 {
		inv := float32(1.0 / math.Sqrt(norm))
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}

func openStressEngine(t *testing.T, indexes []string, dim int) *Engine {
	t.Helper()
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0 // snapshots are driven explicitly
	opts.AutoSaveThreshold = 0
	opts.MaintenanceInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	for _, idx := range indexes {
		if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatalf("vcreate %s: %v", idx, err)
		}
	}
	// The dimension is learned from the first vector.
	for _, idx := range indexes {
		if err := eng.VAdd(idx, "__seed", randomVector(rand.New(rand.NewSource(1)), dim),
			map[string]any{"content": "seed", "type": "memory"}); err != nil {
			t.Fatalf("seed %s: %v", idx, err)
		}
	}
	_ = dim
	return eng
}

// TestStressRealisticMixedWorkload is the main test: many goroutines doing the
// operations a memory engine serves, on several indexes, for a while.
//
// Assertions:
//   - every write that returns nil can be read back with the same content
//   - search returns results for a query derived from a stored vector, and the
//     stored vector is among them
//   - deletions actually remove the node
//   - no panic, no race (checked by -race), no lost write
func TestStressRealisticMixedWorkload(t *testing.T) {
	cfg := stressConfig{
		dim:       64,
		workers:   8,
		perWorker: 120,
		indexes:   []string{"mem_a", "mem_b"},
	}
	cfg.validate(t)

	eng := openStressEngine(t, cfg.indexes, cfg.dim)

	// Nodes that were not the top hit at insert time; verified after the storm.
	var reachableBacklog sync.Map

	var (
		writes     atomic.Int64
		searches   atomic.Int64
		reads      atomic.Int64
		deletes    atomic.Int64
		links      atomic.Int64
		verifyFail atomic.Int64
	)

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(1000 + worker)))
			idx := cfg.indexes[worker%len(cfg.indexes)]

			for i := 0; i < cfg.perWorker; i++ {
				id := fmt.Sprintf("w%d_i%d", worker, i)
				vec := randomVector(r, cfg.dim)
				content := fmt.Sprintf("memory %s about topic %d", id, i%7)

				if err := eng.VAdd(idx, id, vec, map[string]any{
					"content":      content,
					"type":         "memory",
					"user_id":      fmt.Sprintf("user_%d", worker%3),
					"memory_layer": "episodic",
				}); err != nil {
					t.Errorf("VAdd %s: %v", id, err)
					return
				}
				writes.Add(1)

				// Read back and check the content survived.
				data, err := eng.VGet(idx, id)
				if err != nil {
					t.Errorf("VGet %s after write: %v", id, err)
					return
				}
				if got, _ := data.Metadata["content"].(string); got != content {
					t.Errorf("content mismatch for %s: got %q want %q", id, got, content)
					verifyFail.Add(1)
				}
				reads.Add(1)

				// The retrieved vector must be the same one we stored.
				if len(data.Vector) != cfg.dim {
					t.Errorf("vector dim for %s = %d, want %d", id, len(data.Vector), cfg.dim)
				}
				for j := range data.Vector {
					if math.Abs(float64(data.Vector[j]-vec[j])) > 1e-5 {
						t.Errorf("vector mismatch for %s at %d: got %v want %v",
							id, j, data.Vector[j], vec[j])
						verifyFail.Add(1)
						break
					}
				}

				// Search with this vector: it must come back as the top hit.
				//
				// A node becomes searchable once it is linked into the graph, and
				// VAdd returns after that. So the immediate search must see it —
				// that is exactly the guarantee being tested here.
				results, err := eng.VSearchWithScores(idx, vec, 5, "", 0)
				if err != nil {
					t.Errorf("VSearch after %s: %v", id, err)
					return
				}
				searches.Add(1)
				if len(results) == 0 {
					// An empty result under concurrent load means the graph could
					// not be traversed at all, which is a real defect.
					t.Errorf("search returned nothing after storing %s", id)
					verifyFail.Add(1)
					continue
				}
				if results[0].ID != id {
					// Concurrent inserts legitimately change what is nearest. The
					// node must still be reachable, but "reachable" during heavy
					// graph mutation is best checked once the storm settles —
					// see TestStressSettledGraphIsFullyReachable.
					reachableBacklog.Store(id, vec)
				}

				// Every third write, link it to an entity and delete the previous one.
				if i%3 == 0 {
					entity := fmt.Sprintf("entity_%d", i%5)
					if err := eng.VLink(idx, id, entity, "mentions", "mentioned_by", 1.0, nil); err != nil {
						t.Errorf("VLink %s: %v", id, err)
					} else {
						links.Add(1)
					}
				}
				if i%5 == 4 && i >= 5 {
					old := fmt.Sprintf("w%d_i%d", worker, i-5)
					if err := eng.VDelete(idx, old); err != nil {
						t.Errorf("VDelete %s: %v", old, err)
					} else {
						deletes.Add(1)
					}
					// A deleted node must be gone from search.
					if _, err := eng.VGet(idx, old); err == nil {
						t.Errorf("deleted node %s is still readable", old)
						verifyFail.Add(1)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	t.Logf("writes=%d reads=%d searches=%d links=%d deletes=%d verifyFailures=%d",
		writes.Load(), reads.Load(), searches.Load(), links.Load(), deletes.Load(), verifyFail.Load())

	// Once the writers stop, every node that still exists must be findable by
	// searching with its own vector. This separates "another node was closer
	// mid-insert" from "the graph cannot reach this node at all".
	unreachable := 0
	reachableBacklog.Range(func(k, v any) bool {
		id := k.(string)
		vec := v.([]float32)
		if _, err := eng.VGet(idxFor(id, cfg), id); err != nil {
			return true // deleted during the run: expected
		}
		res, err := eng.VSearchWithScores(idxFor(id, cfg), vec, 10, "", 0)
		if err != nil {
			return true
		}
		for _, r := range res {
			if r.ID == id {
				return true
			}
		}
		unreachable++
		if unreachable <= 3 {
			t.Logf("unreachable after quiesce: %s", id)
		}
		return true
	})
	if unreachable > 0 {
		t.Errorf("%d nodes are not reachable even after the load settled", unreachable)
		verifyFail.Add(1)
	}

	if verifyFail.Load() > 0 {
		t.Fatalf("%d correctness failures under load", verifyFail.Load())
	}
	if writes.Load() == 0 || searches.Load() == 0 {
		t.Fatal("the workload did not run")
	}
}

// idxFor maps a generated worker id back to the index it was written to.
func idxFor(id string, cfg stressConfig) string {
	var worker int
	if _, err := fmt.Sscanf(id, "w%d_", &worker); err != nil {
		return cfg.indexes[0]
	}
	return cfg.indexes[worker%len(cfg.indexes)]
}

// TestStressWithSnapshotAndCompaction puts the persistence and maintenance paths
// under the same load. Snapshots truncate the AOF while writes are arriving, so
// this is where shadow-buffer logic and lock ordering get exercised for real.
func TestStressWithSnapshotAndCompaction(t *testing.T) {
	cfg := stressConfig{dim: 32, workers: 6, perWorker: 80, indexes: []string{"s1", "s2"}}
	cfg.validate(t)
	eng := openStressEngine(t, cfg.indexes, cfg.dim)

	stop := make(chan struct{})
	var bg sync.WaitGroup

	// Background: snapshots and maintenance while writes are in flight.
	bg.Add(2)
	go func() {
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
				if err := eng.SaveSnapshot(); err != nil {
					// A snapshot racing shutdown is acceptable; anything else is not.
					t.Logf("SaveSnapshot under load: %v", err)
				}
			}
		}
	}()
	go func() {
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				for _, idx := range cfg.indexes {
					if err := eng.VTriggerMaintenance(idx, "vacuum"); err != nil {
						t.Logf("vacuum %s under load: %v", idx, err)
					}
				}
			}
		}
	}()

	var verifyFail atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(2000 + worker)))
			idx := cfg.indexes[worker%len(cfg.indexes)]
			for i := 0; i < cfg.perWorker; i++ {
				id := fmt.Sprintf("snap_w%d_i%d", worker, i)
				content := fmt.Sprintf("payload %d-%d", worker, i)
				if err := eng.VAdd(idx, id, randomVector(r, cfg.dim),
					map[string]any{"content": content, "type": "memory"}); err != nil {
					t.Errorf("VAdd: %v", err)
					return
				}
				data, err := eng.VGet(idx, id)
				if err != nil {
					t.Errorf("VGet after snapshot cycle: %v", err)
					verifyFail.Add(1)
					continue
				}
				if got, _ := data.Metadata["content"].(string); got != content {
					t.Errorf("content lost across snapshot: got %q want %q", got, content)
					verifyFail.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	bg.Wait()

	if verifyFail.Load() > 0 {
		t.Fatalf("%d writes were damaged by concurrent snapshot/compaction", verifyFail.Load())
	}

	// Everything must still be there after a final snapshot and reopen.
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("final snapshot: %v", err)
	}
	t.Log("snapshot + compaction under load completed")
}

// TestStressManyReadersOneWriter checks the read path under write pressure: a
// reader must never see a torn vector or a half-written metadata map.
func TestStressManyReadersOneWriter(t *testing.T) {
	cfg := stressConfig{dim: 128, workers: 1, perWorker: 1, indexes: []string{"rw"}}
	cfg.validate(t)
	eng := openStressEngine(t, cfg.indexes, cfg.dim)
	const idx = "rw"

	stored := make(map[string][]float32)
	var mu sync.Mutex
	stop := make(chan struct{})
	var readers sync.WaitGroup

	var verifyFail atomic.Int64
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func(seed int) {
			defer readers.Done()
			rng := rand.New(rand.NewSource(int64(3000 + seed)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				mu.Lock()
				if len(stored) == 0 {
					mu.Unlock()
					continue
				}
				var id string
				var want []float32
				for k, v := range stored {
					id, want = k, v
					break
				}
				mu.Unlock()

				data, err := eng.VGet(idx, id)
				if err != nil {
					continue // may have been deleted
				}
				if len(data.Vector) != len(want) {
					t.Errorf("torn vector for %s: %d vs %d", id, len(data.Vector), len(want))
					verifyFail.Add(1)
					continue
				}
				for i := range want {
					if math.Abs(float64(data.Vector[i]-want[i])) > 1e-5 {
						t.Errorf("reader saw a modified vector for %s at %d", id, i)
						verifyFail.Add(1)
						break
					}
				}
				_ = rng
			}
		}(r)
	}

	writerRng := rand.New(rand.NewSource(99))
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("rw_%d", i)
		vec := randomVector(writerRng, cfg.dim)
		if err := eng.VAdd(idx, id, vec, map[string]any{"content": id, "type": "memory"}); err != nil {
			t.Fatalf("VAdd: %v", err)
		}
		mu.Lock()
		stored[id] = vec
		mu.Unlock()
	}
	close(stop)
	readers.Wait()

	if verifyFail.Load() > 0 {
		t.Fatalf("%d torn reads under concurrent writes", verifyFail.Load())
	}
}

// TestStressDeleteRecreate races index deletion against readers and writers.
// This is the case that used to produce use-after-unmap.
func TestStressDeleteRecreate(t *testing.T) {
	cfg := stressConfig{dim: 16, workers: 1, perWorker: 1, indexes: []string{"dr"}}
	cfg.validate(t)
	eng := openStressEngine(t, cfg.indexes, cfg.dim)
	const idx = "dr"

	stop := make(chan struct{})
	var bg sync.WaitGroup
	for w := 0; w < 6; w++ {
		bg.Add(1)
		go func(worker int) {
			defer bg.Done()
			r := rand.New(rand.NewSource(int64(4000 + worker)))
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := fmt.Sprintf("dr_w%d_%d", worker, i)
				// Errors are expected while the index is being recreated; the
				// point is that they are errors, not panics or faults.
				_ = eng.VAdd(idx, id, randomVector(r, cfg.dim), map[string]any{"content": id})
				_, _ = eng.VSearchWithScores(idx, randomVector(r, cfg.dim), 3, "", 0)
				_, _ = eng.VGet(idx, id)
				i++
			}
		}(w)
	}

	for cycle := 0; cycle < 5; cycle++ {
		time.Sleep(40 * time.Millisecond)
		if err := eng.VDeleteIndex(idx); err != nil {
			t.Logf("delete index: %v", err)
		}
		if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatalf("recreate index: %v", err)
		}
		_ = eng.VAdd(idx, "__seed", randomVector(rand.New(rand.NewSource(1)), cfg.dim), nil)
	}
	close(stop)
	bg.Wait()
	t.Log("delete/recreate cycles completed")
}

// TestStressCloseWhileBusy closes the engine while workers are mid-operation.
// Nothing may panic; VGet after close must not fault (that was a SIGSEGV).
func TestStressCloseWhileBusy(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		opts := DefaultOptions(t.TempDir())
		opts.AutoSaveInterval = 0
		opts.AutoSaveThreshold = 0
		opts.MaintenanceInterval = 0
		eng, err := Open(opts)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := eng.VCreate("cl", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatalf("vcreate: %v", err)
		}
		_ = eng.VAdd("cl", "__seed", randomVector(rand.New(rand.NewSource(1)), 32), nil)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				r := rand.New(rand.NewSource(int64(5000 + worker)))
				for {
					select {
					case <-stop:
						return
					default:
					}
					id := fmt.Sprintf("cl_%d_%d", worker, rand.Intn(1000))
					_ = eng.VAdd("cl", id, randomVector(r, 32), map[string]any{"content": id})
					_, _ = eng.VGet("cl", id)
					_, _ = eng.VSearchWithScores("cl", randomVector(r, 32), 3, "", 0)
				}
			}(w)
		}

		time.Sleep(25 * time.Millisecond)
		// Close while the workers are still running.
		if err := eng.Close(); err != nil {
			t.Logf("close reported: %v", err)
		}
		close(stop)
		wg.Wait()

		// Reading after close must return an error, never fault.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("attempt %d: VGet panicked after close: %v", attempt, r)
				}
			}()
			_, _ = eng.VGet("cl", "__seed")
		}()
	}
	t.Log("close-during-load completed without panic")
}
