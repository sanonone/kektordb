package cognitive

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

// TestCursorInvalidatedWhenIndexShrinks verifica che un cursore oltre la fine
// dell'indice venga scartato.
//
// Il difetto: il cursore e' una mappa in memoria per nome d'indice, mai
// azzerata. `VDeleteIndex` + `VCreate` con lo stesso nome lasciava il cursore
// puntato a un indice che non esiste piu': i detector partivano da meta'
// array e trovavano zero candidati, senza alcun segnale.
func TestCursorInvalidatedWhenIndexShrinks(t *testing.T) {
	dir := t.TempDir()
	opts := engine.DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close()

	idx := "cursor_test"
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate: %v", err)
	}
	v := make([]float32, 384)
	for i := range v {
		v[i] = 0.01 * float32(i)
	}
	for i := 0; i < 50; i++ {
		eng.VAdd(idx, "n"+string(rune('a'+i%26))+string(rune('a'+i/26)), v, map[string]any{"content": "x"})
	}

	g := &Gardener{eng: eng, scanCursors: make(map[string]uint32), cursorGeneration: make(map[string]uint64)}

	// Cursore avanti, indice pieno: deve essere rispettato.
	g.setCursor(idx+"_contradictions", 40)
	if got := g.getCursor(idx + "_contradictions"); got != 40 {
		t.Errorf("cursore valido: atteso 40, got %d", got)
	}

	// Ricrea l'indice con pochi nodi: il cursore precedente non ha piu' senso.
	if err := eng.VDeleteIndex(idx); err != nil {
		t.Fatalf("VDeleteIndex: %v", err)
	}
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("VCreate 2: %v", err)
	}
	eng.VAdd(idx, "small", v, map[string]any{"content": "x"})

	if got := g.getCursor(idx + "_contradictions"); got != 0 {
		t.Errorf("dopo la ricreazione il cursore deve ripartire da 0, got %d", got)
	}
}
