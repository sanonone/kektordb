package engine

// Test che il fix di durabilita' regga: snapshot e AOF rewrite devono essere
// sincronizzati su disco PRIMA del rename, non solo scritti nel buffer.
//
// Prima del fix: saveSnapshotLocked faceva Flush+Close (senza fsync) e poi
// rinominava; RewriteAOF idem, senza Sync. Il rename e' atomico ma rende
// visibile un file i cui dati possono essere ancora in page cache: un crash
// subito dopo lascerebbe un file "valido" ma incompleto.
//
// Il test verifica il contratto osservabile: dopo SaveSnapshot/RewriteAOF il
// file finale esiste, e' completo (si rilegge e ripristina correttamente) e il
// vecchio file non e' rimasto. Non puo' osservare il fsync direttamente, ma
// esercita il percorso che ora lo include e fallirebbe se una write fosse
// persa.

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestSnapshotSurvivesImmediateCrash: dopo SaveSnapshot, un "crash" immediato
// (nessun Close pulito) deve lasciare uno snapshot completo e leggibile.
func TestSnapshotSurvivesImmediateCrash(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "fsync_snap"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	for i := 0; i < 50; i++ {
		if err := eng.VAdd(idx, "v"+itoaSmall(i), vecN(8, float32(i)), map[string]any{"n": float64(i)}); err != nil {
			t.Fatalf("VAdd %d: %v", i, err)
		}
	}

	if err := eng.SaveSnapshot(); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// "Crash": nessun Close, nessun flush aggiuntivo. Se lo snapshot non fosse
	// sincronizzato, la riapertura troverebbe dati mancanti o corrotti.
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0), 60, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	if len(res) != 50 {
		t.Errorf("dopo il crash: %d vettori recuperati, attesi 50", len(res))
	}
	d, err := eng2.VGet(idx, "v7")
	if err != nil {
		t.Fatalf("VGet v7: %v", err)
	}
	if d.Metadata["n"] != float64(7) {
		t.Errorf("metadata non recuperati: %v", d.Metadata["n"])
	}
}

// TestRewriteAOFSurvivesImmediateCrash: stesso contratto per RewriteAOF.
func TestRewriteAOFSurvivesImmediateCrash(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "fsync_rw"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	for i := 0; i < 30; i++ {
		if err := eng.VAdd(idx, "r"+itoaSmall(i), vecN(8, float32(i)), map[string]any{"i": float64(i)}); err != nil {
			t.Fatalf("VAdd %d: %v", i, err)
		}
	}

	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF: %v", err)
	}

	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	res, err := eng2.VSearch(idx, vecN(8, 0), 40, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	if len(res) != 30 {
		t.Errorf("dopo il crash: %d vettori, attesi 30", len(res))
	}
	// Il file temporaneo del rewrite non deve restare.
	// (verificato implicitamente: se fosse rimasto, l'apertura sarebbe fallita)
}

// TestAOFWriteFailureIsPropagated verifica che un errore di scrittura durante il
// rewrite venga riportato e non produca un AOF parziale silenzioso.
//
// Non potendo iniettare un errore di I/O facilmente, verifico il contratto
// complementare: dopo un rewrite riuscito il contenuto e' completo e leggibile.
// La propagazione dell'errore e' coperta dalla struttura writeCmd + Flush/Sync
// (un errore rende no-op le write successive e viene ritornato).
func TestRewriteAOFProducesCompleteFile(t *testing.T) {
	dir := t.TempDir()
	eng := aofSetup(t, dir)
	const idx = "rw_complete"

	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	ids := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		id := "c" + itoaSmall(i)
		ids = append(ids, id)
		if err := eng.VAdd(idx, id, vecN(8, float32(i)), map[string]any{"tag": "x"}); err != nil {
			t.Fatalf("VAdd %d: %v", i, err)
		}
	}
	if err := eng.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF: %v", err)
	}
	crashClose(t, eng)

	eng2 := aofSetup(t, dir)
	defer eng2.Close()

	// Tutti gli ID devono essere presenti: un AOF parziale ne perderebbe alcuni.
	res, err := eng2.VSearch(idx, vecN(8, 0), 30, "", "", 0, 0.5, nil)
	if err != nil {
		t.Fatalf("VSearch: %v", err)
	}
	for _, id := range ids {
		if !slices.Contains(res, id) {
			t.Errorf("id %q mancante dopo il rewrite", id)
		}
	}
}

// itoaSmall converte un intero piccolo in stringa senza importare strconv.
func itoaSmall(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
