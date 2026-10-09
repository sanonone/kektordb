package engine

// Test di riproduzione per C4: il replay dell'AOF non vede gli indici che
// arrivano dallo snapshot, perché la sua mappa locale si popola solo dai
// VCREATE presenti nell'AOF — e lo snapshot tronca l'AOF proprio di quei
// comandi. Due sintomi simmetrici:
//
//	C4a: una scrittura dopo lo snapshot viene SCARTATA (dato perso)
//	C4b: una cancellazione dopo lo snapshot viene IGNORATA (dato che risorge)

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// crashClose chiude le risorse senza snapshot finale. Close() non esegue
// snapshot, e i test flushato l'AOF esplicitamente prima di chiamarla, quindi
// lo stato su disco equivale a quello di un crash.
func crashClose(t *testing.T, eng *Engine) {
	t.Helper()
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// aofSetup apre un engine su una dir temporanea con autosave disattivato.
func aofSetup(t *testing.T, dir string) *Engine {
	t.Helper()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return eng
}

func vecN(n int, base float32) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = base + float32(i)*0.01
	}
	return v
}

// TestC4aWriteAfterSnapshotSurvivesCrash: snapshot, poi una scrittura NUOVA
// (che finisce solo nell'AOF), poi crash. Al restart il dato deve esserci.
func TestC4aWriteAfterSnapshotSurvivesCrash(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4a"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	if err := eng.VAdd(idx, "before", vecN(8, 0.5), nil); err != nil {
		t.Fatalf("VAdd before: %v", err)
	}

	// Snapshot: rende "before" durevole e TRONCA l'AOF (il VCREATE sparisce).
	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Scrittura successiva allo snapshot: finisce solo nell'AOF.
	if err := eng.VAdd(idx, "after", vecN(8, 0.7), nil); err != nil {
		t.Fatalf("VAdd after: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// "Crash": Close() non esegue snapshot, e i dati sono già stati flushati
	// esplicitamente, quindi lo stato su disco è identico a un crash avvenuto
	// subito dopo il flush.
	crashClose(t, eng)

	// Riapri: il replay deve applicare "after".
	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("dopo il crash: %v", res)
	if !slices.Contains(res, "after") {
		t.Errorf("C4a: il vettore scritto DOPO lo snapshot è andato perso (res=%v)", res)
	}
}

// TestC4bDropAfterSnapshotStaysDropped: snapshot, poi VDeleteIndex (VDROP
// nell'AOF), poi crash. Al restart l'indice NON deve risorgere.
func TestC4bDropAfterSnapshotStaysDropped(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4b"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	if err := eng.VAdd(idx, "m1", vecN(8, 0.5), nil); err != nil {
		t.Fatalf("VAdd: %v", err)
	}

	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Cancellazione dell'indice DOPO lo snapshot: VDROP va nell'AOF.
	if err := eng.VDeleteIndex(idx); err != nil {
		t.Fatalf("VDeleteIndex: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	if eng2.IndexExists(idx) {
		t.Errorf("C4b: l'indice cancellato dopo lo snapshot è RISORTO al restart")
	}
}

// TestC4cDeleteVectorAfterSnapshotStaysDeleted: stessa classe, a livello di
// singolo vettore (VDEL invece di VDROP).
func TestC4cDeleteVectorAfterSnapshotStaysDeleted(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4c"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	if err := eng.VAdd(idx, "keep", vecN(8, 0.5), nil); err != nil {
		t.Fatalf("VAdd keep: %v", err)
	}
	if err := eng.VAdd(idx, "drop", vecN(8, 0.51), nil); err != nil {
		t.Fatalf("VAdd drop: %v", err)
	}

	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	if err := eng.VDelete(idx, "drop"); err != nil {
		t.Fatalf("VDelete: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0.5), 10, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	t.Logf("dopo il crash: %v", res)
	if slices.Contains(res, "drop") {
		t.Errorf("C4c: il vettore cancellato dopo lo snapshot è RISORTO (res=%v)", res)
	}
	if !slices.Contains(res, "keep") {
		t.Errorf("c4c: 'keep' deve sopravvivere (res=%v)", res)
	}
}

// TestC4dMetadataAfterSnapshotSurvives: la stessa classe colpisce VMETA.
func TestC4dMetadataAfterSnapshotSurvives(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "c4d"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	if err := eng.VAdd(idx, "m1", vecN(8, 0.5), map[string]any{"tag": "old"}); err != nil {
		t.Fatalf("VAdd: %v", err)
	}

	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Aggiornamento metadata dopo lo snapshot -> VMETA nell'AOF.
	if err := eng.VSetMetadata(idx, "m1", map[string]any{"tag": "new"}); err != nil {
		t.Fatalf("VSetMetadata: %v", err)
	}
	if err := eng.AOF.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	d, err := eng2.VGet(idx, "m1")
	if err != nil {
		t.Fatalf("VGet: %v", err)
	}
	if d.Metadata["tag"] != "new" {
		t.Errorf("C4d: metadata persi — tag = %v, want new", d.Metadata["tag"])
	}
}
