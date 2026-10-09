package engine

// Casi limite del fix C4: verificano che il seed dal DB non introduca
// regressioni nei percorsi tradizionali (AOF senza snapshot, indice creato
// nell'AOF, VDEL su nodi dell'AOF).

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestC4eNoSnapshotStillWorks: senza snapshot il comportamento deve restare
// quello di sempre (indice creato nell'AOF, entry applicate).
func TestC4eNoSnapshotStillWorks(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4e"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(idx, "a", vecN(8, 0.5), map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 5, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	if !slices.Contains(res, "a") {
		t.Errorf("percorso AOF tradizionale rotto: %v", res)
	}
	d, err := eng2.VGet(idx, "a")
	if err != nil {
		t.Fatalf("VGet: %v", err)
	}
	if d.Metadata["k"] != "v" {
		t.Errorf("metadata persi nel percorso tradizionale: %v", d.Metadata)
	}
}

// TestC4fIndexCreatedAfterSnapshot: un indice creato DOPO lo snapshot è nuovo,
// arriva solo dall'AOF, e deve essere creato dal replay con le sue entry.
func TestC4fIndexCreatedAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)

	// Indice pre-esistente, poi snapshot.
	if err := eng.VCreate("c4f_old", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd("c4f_old", "old1", vecN(8, 0.5), nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// Indice NUOVO dopo lo snapshot: nell'AOF ci saranno VCREATE + VADD.
	if err := eng.VCreate("c4f_new", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd("c4f_new", "new1", vecN(8, 0.7), map[string]any{"src": "aof"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	// L'indice nuovo deve esistere, con il suo vettore e i suoi metadata.
	if !eng2.IndexExists("c4f_new") {
		t.Fatal("indice creato dopo lo snapshot assente dopo il restart")
	}
	res, err := eng2.VSearch("c4f_new", vecN(8, 0.7), 5, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	if !slices.Contains(res, "new1") {
		t.Errorf("vettore dell'indice nuovo mancante: %v", res)
	}
	d, _ := eng2.VGet("c4f_new", "new1")
	if d.Metadata["src"] != "aof" {
		t.Errorf("metadata dell'indice nuovo persi: %v", d.Metadata)
	}

	// E il vecchio deve essere intatto.
	resOld, _ := eng2.VSearch("c4f_old", vecN(8, 0.5), 5, "", "", 0, 0.5, nil)
	if !slices.Contains(resOld, "old1") {
		t.Errorf("indice dello snapshot danneggiato: %v", resOld)
	}
}

// TestC4gMixedOperationsAfterSnapshot: più operazioni miste dopo lo snapshot,
// tutte devono sopravvivere al crash.
func TestC4gMixedOperationsAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4g"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(idx, "base", vecN(8, 0.5), map[string]any{"n": 1.0}); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// Mix: add, meta update, delete, add.
	if err := eng.VAdd(idx, "added", vecN(8, 0.6), nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VSetMetadata(idx, "base", map[string]any{"n": 2.0, "updated": true}); err != nil {
		t.Fatal(err)
	}
	if err := eng.VDelete(idx, "added"); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(idx, "final", vecN(8, 0.9), map[string]any{"last": true}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("dopo il crash: %v", res)

	if !slices.Contains(res, "base") {
		t.Error("'base' (snapshot + meta update) perso")
	}
	if !slices.Contains(res, "final") {
		t.Error("'final' (add dopo delete) perso")
	}
	if slices.Contains(res, "added") {
		t.Error("'added' cancellato è risorto")
	}
	d, err := eng2.VGet(idx, "base")
	if err != nil {
		t.Fatalf("VGet base: %v", err)
	}
	if d.Metadata["n"] != 2.0 || d.Metadata["updated"] != true {
		t.Errorf("metadata di 'base' non aggiornati: %v", d.Metadata)
	}
}

// TestC4hMetadataOnNodeAddedAfterSnapshot: VMETA su un nodo che è stato aggiunto
// DOPO lo snapshot (quindi presente solo nel delta, non nel DB). È il caso
// speculare di C4d e deve restare nel percorso "accumula nelle entry".
func TestC4hMetadataOnNodeAddedAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4h"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(idx, "snap", vecN(8, 0.5), nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// Nodo NUOVO (solo nell'AOF), poi metadata su di esso.
	if err := eng.VAdd(idx, "delta", vecN(8, 0.6), nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VSetMetadata(idx, "delta", map[string]any{"origin": "aof"}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	d, err := eng2.VGet(idx, "delta")
	if err != nil {
		t.Fatalf("VGet delta: %v", err)
	}
	if d.Metadata["origin"] != "aof" {
		t.Errorf("metadata del nodo aggiunto dopo lo snapshot persi: %v", d.Metadata)
	}
}

// TestC4iRepeatedSnapshotCycles: più cicli snapshot -> modifiche -> crash.
// Ogni ciclo deve conservare quanto accumulato.
func TestC4iRepeatedSnapshotCycles(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4i"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	for cycle := 0; cycle < 3; cycle++ {
		// Una scrittura, poi snapshot (tronca l'AOF).
		id := "c" + string(rune('0'+cycle))
		if err := eng.VAdd(idx, id, vecN(8, 0.5+float32(cycle)*0.1), nil); err != nil {
			t.Fatal(err)
		}
		if err := eng.SaveSnapshot(); err != nil {
			t.Fatalf("snapshot ciclo %d: %v", cycle, err)
		}
		// E una scrittura DOPO lo snapshot (resta nel delta).
		id2 := "d" + string(rune('0'+cycle))
		if err := eng.VAdd(idx, id2, vecN(8, 0.55+float32(cycle)*0.1), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 20, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("dopo 3 cicli: %v", res)
	for cycle := 0; cycle < 3; cycle++ {
		for _, prefix := range []string{"c", "d"} {
			id := prefix + string(rune('0'+cycle))
			if !slices.Contains(res, id) {
				t.Errorf("%s mancante dopo 3 cicli snapshot (res=%v)", id, res)
			}
		}
	}
}

// TestC4jAddAndDeleteBothAfterSnapshot: un vettore aggiunto E cancellato dopo
// lo snapshot non è mai stato persistito: al replay VDEL trova un nodo che non
// esiste nel DB. Non deve essere un errore fatale, e il vettore non deve
// comparire.
func TestC4jAddAndDeleteBothAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4j"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.VAdd(idx, "base", vecN(8, 0.5), nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatal(err)
	}

	// Add + Delete, entrambi dopo lo snapshot.
	if err := eng.VAdd(idx, "ephemeral", vecN(8, 0.55), map[string]any{"tmp": true}); err != nil {
		t.Fatal(err)
	}
	if err := eng.VSetMetadata(idx, "ephemeral", map[string]any{"updated": true}); err != nil {
		t.Fatal(err)
	}
	if err := eng.VDelete(idx, "ephemeral"); err != nil {
		t.Fatal(err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatal(err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("dopo il crash: %v", res)
	if slices.Contains(res, "ephemeral") {
		t.Error("'ephemeral' (mai persistito) è comparso dopo il restart")
	}
	if !slices.Contains(res, "base") {
		t.Errorf("'base' deve sopravvivere: %v", res)
	}
}
