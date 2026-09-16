package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestVDeleteIndexRecreateClean è il test di chiusura del fix D7: il delete
// è sincrono (niente goroutine in background), quindi delete → recreate
// immediato non deve mai resuscitare dati vecchi. Ripetuto in loop, senza
// sleep né directory fresche.
func TestVDeleteIndexRecreateClean(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const indexName = "recreate_idx"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	mkVec := func(base float32) []float32 {
		v := make([]float32, 8)
		for i := range v {
			v[i] = base + float32(i)*0.01
		}
		return v
	}

	for round := 0; round < 3; round++ {
		tag := fmt.Sprintf("round%d", round)
		if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatalf("round %d: VCreate: %v", round, err)
		}
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("%s-vec%d", tag, i)
			if err := eng.VAdd(indexName, id, mkVec(float32(round*10+i)), map[string]any{"tag": tag}); err != nil {
				t.Fatalf("round %d: VAdd %s: %v", round, id, err)
			}
		}
		// I dati devono aver toccato davvero il disco (altrimenti il test è vacuo).
		entries, err := os.ReadDir(arenaDir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("round %d: arena dir senza file dopo insert (err=%v), test vacuo", round, err)
		}

		if err := eng.VDeleteIndex(indexName); err != nil {
			t.Fatalf("round %d: VDeleteIndex: %v", round, err)
		}
		// Sincrono: nessun sleep, la dir deve essere sparita ORA.
		if _, err := os.Stat(arenaDir); !os.IsNotExist(err) {
			t.Fatalf("round %d: arena dir ancora presente subito dopo VDeleteIndex", round)
		}

		// Ricrea e verifica che i vecchi dati non siano resuscitati.
		if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
			t.Fatalf("round %d: VCreate dopo delete: %v", round, err)
		}
		if round > 0 {
			// I round > 0 provano il path D-b solo se orfani esistono; qui non
			// devono esisterne (delete sincrono riuscito), ma la search deve
			// comunque vedere solo dati nuovi.
			res, err := eng.VSearch(indexName, mkVec(0), 20, "", "", 0, 0, nil)
			if err != nil {
				t.Fatalf("round %d: VSearch su indice fresco: %v", round, err)
			}
			for _, id := range res {
				if !strings.HasPrefix(id, tag) {
					t.Fatalf("round %d: dato fantasma resuscitato: %q", round, id)
				}
			}
		}
		// Riempie per il prossimo round (l'ultimo riempimento resta, innocuo).
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("%s-vec%d", tag, i)
			_ = eng.VAdd(indexName, id, mkVec(float32(round*10+i)), map[string]any{"tag": tag})
		}
		if err := eng.VDeleteIndex(indexName); err != nil {
			t.Fatalf("round %d: VDeleteIndex finale: %v", round, err)
		}
	}
}

// TestVCreateCleansOrphanArenaDir verifica il self-healing D-b: se una
// directory arena orfana esiste senza indice in memoria, VCreate la rimuove
// invece di resuscitarne i dati.
func TestVCreateCleansOrphanArenaDir(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	const indexName = "orphan_idx"
	arenaDir := filepath.Join(tmpDir, "arenas", indexName)

	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	oldVec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	if err := eng.VAdd(indexName, "old-vec", oldVec, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(arenaDir); err != nil {
		t.Fatalf("arena dir assente dopo insert, test vacuo: %v", err)
	}

	// Simula un delete incompleto: rimuove l'indice dalle mappe via AOF-free
	// path? No: usa il DB diretto per lasciare i file orfani su disco.
	if err := eng.DB.DeleteVectorIndex(indexName); err != nil {
		t.Fatalf("DB.DeleteVectorIndex: %v", err)
	}
	// Ricrea artificialmente i file orfani (il delete sincrono li ha tolti).
	if err := os.MkdirAll(arenaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arenaDir, "chunk_fake.bin"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	// VCreate deve ripulire e riuscire, senza resuscitare nulla.
	if err := eng.VCreate(indexName, distance.Cosine, 8, 100, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate con orfani: %v", err)
	}
	newVec := []float32{0, 1, 0, 0, 0, 0, 0, 0}
	if err := eng.VAdd(indexName, "new-vec", newVec, nil); err != nil {
		t.Fatal(err)
	}
	res, err := eng.VSearch(indexName, newVec, 20, "", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range res {
		if id == "old-vec" {
			t.Fatalf("dato orfano resuscitato dopo VCreate: %q", id)
		}
	}
	if len(res) == 0 {
		t.Fatalf("nessun risultato dopo recreate+insert")
	}
}
