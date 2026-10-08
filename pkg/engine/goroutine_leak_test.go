package engine

// Test di regressione per l'audit A1: nessuna goroutine interna deve
// sopravvivere a Engine.Close().
//
// Caso trovato: initArenaIfNeeded costruiva l'AsyncCompactor in una variabile
// locale invece di usare arena.StartCompactor, quindi arena.compactor restava
// nil e arena.StopCompactor() era un no-op. La goroutine del compactor
// sopravviveva alla chiusura (e il suo jitter di avvio 0-30s la teneva viva
// ben oltre l'unmap dell'arena).

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// liveKektordbGoroutines conta le goroutine del progetto ancora attive,
// escludendo quelle del runtime di test.
func liveKektordbGoroutines(t *testing.T) []string {
	t.Helper()
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	var out []string
	for _, block := range strings.Split(string(buf[:n]), "\n\n") {
		if !strings.Contains(block, "github.com/sanonone/kektordb") {
			continue
		}
		// Escludi le goroutine del framework di test.
		if strings.Contains(block, "testing.") && strings.Contains(block, "_test.go") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if strings.Contains(line, "github.com/sanonone/kektordb") && strings.Contains(line, ".go:") {
				out = append(out, strings.TrimSpace(line))
				break
			}
		}
	}
	return out
}

// TestNoGoroutineSurvivesEngineClose: dopo Close non deve restare attiva
// nessuna goroutine interna dell'engine.
func TestNoGoroutineSurvivesEngineClose(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	idx := "leak_check"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	vec := make([]float32, 16)
	for i := range vec {
		vec[i] = 0.5
	}
	for i := 0; i < 20; i++ {
		eng.VAdd(idx, string(rune('a'+i)), vec, map[string]any{"content": "x"})
	}
	// Esercita i percorsi che avviano worker: ricerca, delete (cascade).
	_, _ = eng.VSearch(idx, vec, 5, "", "", 0, 0.5, nil)
	_ = eng.VDelete(idx, "a")

	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Attesa breve: le goroutine tracciate escono subito, un eventuale
	// superstite (jitter, timer) si manifesta qui.
	deadline := time.Now().Add(3 * time.Second)
	var alive []string
	for time.Now().Before(deadline) {
		alive = liveKektordbGoroutines(t)
		if len(alive) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("goroutine sopravvissute a Close() dopo 3s: %v", alive)
}

// TestCompactorRegisteredOnArena verifica il fix di A1 in modo DETERMINISTICO.
//
// Il difetto era che initArenaIfNeeded costruiva l'AsyncCompactor in una
// variabile locale senza assegnarlo ad arena.compactor, rendendo
// arena.StopCompactor() un no-op. La goroutine del compactor dorme in un
// jitter di avvio (0-30s) e non e' sempre osservabile via runtime.Stack, quindi
// il test verifica la CONDIZIONE del bug (l'arena conosce il suo compactor)
// invece del sintomo (una goroutine visibile), che sarebbe flaky.
func TestCompactorRegisteredOnArena(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	idx := "compactor_ref"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	vec := make([]float32, 16)
	for i := range vec {
		vec[i] = 0.5
	}
	// Il primo Add inizializza l'arena, che avvia il compactor.
	if err := eng.VAdd(idx, "a", vec, map[string]any{"content": "x"}); err != nil {
		t.Fatalf("VAdd: %v", err)
	}

	coreIdx, ok := eng.DB.GetVectorIndex(idx)
	if !ok {
		t.Fatal("index not found")
	}
	hnswIdx, ok := coreIdx.(*hnsw.Index)
	if !ok {
		t.Fatal("not an hnsw index")
	}
	arena := hnswIdx.ArenaForTest()
	if arena == nil {
		t.Fatal("arena non inizializzata")
	}
	if !arena.HasCompactor() {
		t.Error("l'arena non ha un compactor registrato: StopCompactor sarebbe un no-op (bug A1)")
	}

	// StopCompactor deve davvero fermarlo (non piu' un no-op).
	arena.StopCompactor()
	if arena.HasCompactor() {
		t.Error("StopCompactor non ha azzerato il riferimento al compactor")
	}
}
