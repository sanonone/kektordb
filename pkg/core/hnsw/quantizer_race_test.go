package hnsw

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestQuantizerConcurrentAutoTrain reproduces P1-3: the single-vector Add
// path checked h.quantizer.AbsMax == 0 (an unlocked field read) before
// calling Train, which writes AbsMax under the quantizer's internal lock.
// Two concurrent first Adds on a fresh int8 index therefore raced on AbsMax.
//
// Pre-fix this fails under -race (DATA RACE on AbsMax); post-fix (locked
// IsTrained accessor) it passes.
func TestQuantizerConcurrentAutoTrain(t *testing.T) {
	arenaDir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Int8, "", arenaDir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	const dim = 32
	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				vec := make([]float32, dim)
				for j := range vec {
					vec[j] = rand.Float32()
				}
				if _, err := idx.Add(fmt.Sprintf("w%d-%d", w, i), vec); err != nil {
					t.Errorf("Add: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// After training, the quantizer must be usable and deterministic.
	if !idx.quantizer.IsTrained() {
		t.Fatal("quantizer not trained after concurrent adds")
	}
}
