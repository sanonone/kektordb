package engine

// Test del fix D: il supersede/archiviazione deve essere reversibile.
// Il caso d'uso: un consolidamento automatico ha nascosto una memoria ancora
// valida; l'utente la ripristina e torna visibile alla retrieval di default.

import (
	"slices"
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

const fase4DefaultFilter = "_is_historical != 'true' AND _archived != 'true'"

func newRestoreEngine(t *testing.T, idx string) *Engine {
	t.Helper()
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	return eng
}

func vec8() []float32 { return []float32{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5} }

// TestVRestoreAfterEvolve: una memoria resa storica da VEvolve torna visibile.
func TestVRestoreAfterEvolve(t *testing.T) {
	eng := newRestoreEngine(t, "restore_evolve")
	idx := "restore_evolve"
	v := vec8()

	eng.VAdd(idx, "m1", v, map[string]any{"content": "fact v1", "type": "memory"})
	newID, err := eng.VEvolve(idx, "m1", v, map[string]any{"content": "fact v2"}, "test")
	if err != nil {
		t.Fatalf("VEvolve: %v", err)
	}

	visible := func() []string {
		res, err := eng.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
		if err != nil {
			t.Fatalf("VSearch: %v", err)
		}
		return res
	}

	if slices.Contains(visible(), "m1") {
		t.Fatal("m1 visibile subito dopo VEvolve: il test non esercita il fix")
	}
	if !slices.Contains(visible(), newID) {
		t.Fatal("il nodo evoluto deve essere visibile")
	}

	if err := eng.VRestore(idx, "m1"); err != nil {
		t.Fatalf("VRestore: %v", err)
	}

	if !slices.Contains(visible(), "m1") {
		t.Error("m1 ancora invisibile dopo VRestore: il ripristino non ha funzionato")
	}
	// La catena di evoluzione resta intatta (storia preservata).
	edges, found := eng.VGetEdges(idx, "m1", "superseded_by", 0)
	if !found || len(edges) == 0 || edges[0].TargetID != newID {
		t.Error("la catena superseded_by deve restare intatta dopo il restore")
	}
}

// TestVRestoreAfterArchive: una memoria archiviata dal consolidamento torna visibile.
func TestVRestoreAfterArchive(t *testing.T) {
	eng := newRestoreEngine(t, "restore_archive")
	idx := "restore_archive"
	v := vec8()

	eng.VAdd(idx, "m2", v, map[string]any{"content": "consolidated fact", "type": "memory"})
	if err := eng.VSetMetadata(idx, "m2", map[string]any{
		"_archived":          true,
		"_consolidated_into": "consolidation_x",
	}); err != nil {
		t.Fatalf("VSetMetadata: %v", err)
	}

	res, _ := eng.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
	if slices.Contains(res, "m2") {
		t.Fatal("m2 visibile dopo l'archiviazione: test non valido")
	}

	if err := eng.VRestore(idx, "m2"); err != nil {
		t.Fatalf("VRestore: %v", err)
	}

	res2, _ := eng.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
	if !slices.Contains(res2, "m2") {
		t.Error("m2 ancora invisibile dopo VRestore")
	}
	// La provenienza resta (storia preservata).
	d, _ := eng.VGet(idx, "m2")
	if d.Metadata["_consolidated_into"] != "consolidation_x" {
		t.Error("la provenienza _consolidated_into deve restare dopo il restore")
	}
	if d.Metadata["_restored_at"] == nil {
		t.Error("atteso _restored_at come traccia del ripristino")
	}
}

// TestVRestoreRejectsDeleted: una memoria VDelete'd NON è ripristinabile, e
// l'errore deve dirlo esplicitamente (niente falso successo).
func TestVRestoreRejectsDeleted(t *testing.T) {
	eng := newRestoreEngine(t, "restore_deleted")
	idx := "restore_deleted"

	eng.VAdd(idx, "gone", vec8(), map[string]any{"content": "x", "type": "memory"})
	if err := eng.VDelete(idx, "gone"); err != nil {
		t.Fatalf("VDelete: %v", err)
	}

	err := eng.VRestore(idx, "gone")
	if err == nil {
		t.Fatal("VRestore su nodo cancellato deve fallire, non fingere successo")
	}
	t.Logf("errore atteso: %v", err)
}

// TestVRestoreRejectsNonHidden: ripristinare una memoria mai nascosta è un
// no-op esplicito (errore chiaro), non un silenzioso successo.
func TestVRestoreRejectsNonHidden(t *testing.T) {
	eng := newRestoreEngine(t, "restore_plain")
	idx := "restore_plain"

	eng.VAdd(idx, "live", vec8(), map[string]any{"content": "x", "type": "memory"})

	if err := eng.VRestore(idx, "live"); err == nil {
		t.Error("VRestore su memoria attiva deve segnalare che non c'è nulla da ripristinare")
	}
}

// TestResolveConflictArchiveIsRestorable è il test di regressione per
// l'outlier corretto: ResolveConflict archiviava E cancellava. Ora archivia
// soltanto, quindi il nodo è ripristinabile.
func TestResolveConflictArchiveIsRestorable(t *testing.T) {
	eng := newRestoreEngine(t, "resolve_restore")
	idx := "resolve_restore"
	v := vec8()

	eng.VAdd(idx, "discarded", v, map[string]any{"content": "stale fact", "type": "memory"})
	// Simula esattamente ciò che ora fa ResolveConflict (archivia, niente VDelete).
	if err := eng.VSetMetadata(idx, "discarded", map[string]any{
		"_archived":      true,
		"invalidated_by": "reflection_1",
	}); err != nil {
		t.Fatalf("VSetMetadata: %v", err)
	}

	res, _ := eng.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
	if slices.Contains(res, "discarded") {
		t.Fatal("il nodo archiviato non deve essere visibile")
	}

	if err := eng.VRestore(idx, "discarded"); err != nil {
		t.Fatalf("VRestore: %v", err)
	}
	res2, _ := eng.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
	if !slices.Contains(res2, "discarded") {
		t.Error("il nodo archiviato da ResolveConflict deve essere ripristinabile")
	}
}

// TestVRestoreSurvivesRestart: il ripristino deve essere persistito (VMETA in
// AOF), altrimenti al riavvio la memoria tornerebbe nascosta.
func TestVRestoreSurvivesRestart(t *testing.T) {
	tmpDir := t.TempDir()
	opts := DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	idx := "restore_restart"

	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	v := vec8()
	eng.VAdd(idx, "m1", v, map[string]any{"content": "fact", "type": "memory"})
	if err := eng.VSetMetadata(idx, "m1", map[string]any{"_archived": true}); err != nil {
		t.Fatalf("VSetMetadata: %v", err)
	}
	if err := eng.VRestore(idx, "m1"); err != nil {
		t.Fatalf("VRestore: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Riapri: il replay dell'AOF deve conservare lo stato ripristinato.
	eng2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer eng2.Close()

	res, err := eng2.VSearch(idx, v, 10, fase4DefaultFilter, "", 100, 1.0, nil)
	if err != nil {
		t.Fatalf("VSearch dopo restart: %v", err)
	}
	if !slices.Contains(res, "m1") {
		t.Errorf("m1 non visibile dopo il restart: il ripristino non è stato persistito (res=%v)", res)
	}
}
