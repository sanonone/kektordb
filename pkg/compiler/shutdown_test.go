package compiler

// Regressione C1: una compilazione asincrona in volo durante la chiusura
// dell'engine non deve leggere memoria smappata (SIGSEGV) né lasciare
// goroutine non tracciate.
//
// Riprodotto pre-fix: TestRequestKnowledge{ CacheHit, CacheMiss } in
// internal/mcp andavano in SIGSEGV (2/6 run isolati, 4/4 insieme) perché
// StartAsyncCompile lanciava una goroutine non tracciata che leggeva
// data.Vector dopo engine.Close() (arena smappata).

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestAsyncCompileDrainedBeforeEngineClose: l'engine deve attendere le
// compilazioni async prima di smappare l'arena.
func TestAsyncCompileDrainedBeforeEngineClose(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const idx = "compile_shutdown"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}

	vec := make([]float32, 16)
	for i := range vec {
		vec[i] = 0.5
	}
	eng.VAdd(idx, "src1", vec, map[string]any{"type": "user", "entity_id": "a", "name": "A"})

	comp := NewCompiler(eng, nil, nil)

	// Avvia N compilazioni asincrone e chiudi SUBITO l'engine: senza drain
	// leggerebbero l'arena smappata.
	const n = 8
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := comp.StartAsyncCompile(CompileRequest{
			Name:      "entity_card",
			IndexName: idx,
			Sources: SourceSpec{
				Type:   "graph_query",
				Entity: EntityRef{Type: "user", ID: "a"},
				Depth:  2,
			},
		})
		if err != nil {
			t.Fatalf("StartAsyncCompile %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	// Chiusura: deve drenare senza crash. Se il drain manca, il processo
	// termina con SIGSEGV (il test fallisce in modo rumoroso).
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Le compilazioni devono risultare concluse (complete o failed), mai pending.
	for _, id := range ids {
		task, err := comp.GetTaskStatus(id)
		if err != nil {
			t.Fatalf("GetTaskStatus(%s): %v", id, err)
		}
		if task.Status == CompileStatusPending || task.Status == CompileStatusCompiling {
			t.Errorf("task %s ancora %s dopo la chiusura dell'engine", id, task.Status)
		}
	}
}

// TestCompilerCloseIsIdempotent: Close può essere chiamato più volte e in
// concorrenza (engine + server possono entrambi invocarlo).
func TestCompilerCloseIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	comp := NewCompiler(eng, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = comp.Close(1 * time.Second)
		}()
	}
	wg.Wait()

	// Dopo Close, nuove compilazioni async sono rifiutate (non avviate a vuoto).
	if _, err := comp.StartAsyncCompile(CompileRequest{Name: "entity_card"}); err == nil {
		t.Error("StartAsyncCompile dopo Close deve fallire, non avviare lavoro")
	}
}

// TestAsyncCompileDuringCloseNoRace: avviare compilazioni mentre Close gira non
// deve produrre race (il flag closing viene ricontrollato nella goroutine).
func TestAsyncCompileDuringCloseNoRace(t *testing.T) {
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const idx = "compile_race"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	vec := make([]float32, 16)
	for i := range vec {
		vec[i] = 0.5
	}
	eng.VAdd(idx, "src1", vec, map[string]any{"type": "user", "entity_id": "a", "name": "A"})

	comp := NewCompiler(eng, nil, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = comp.StartAsyncCompile(CompileRequest{
				Name:      "entity_card",
				IndexName: idx,
				Sources: SourceSpec{
					Type:   "graph_query",
					Entity: EntityRef{Type: "user", ID: "a"},
					Depth:  2,
				},
			})
		}
	}()

	time.Sleep(5 * time.Millisecond)
	_ = comp.Close(5 * time.Second)
	wg.Wait()
	_ = eng.Close()
}
