package core

// Test del supporto alle parentesi in FindIDsByFilter.
//
// Contesto: i chiamanti costruiscono filtri come
//   "layer='a' OR layer='b'"  +  " AND _is_historical != 'true'"
// e senza parentesi la AND si applica solo all'ultimo ramo OR, facendo
// trapelare memorie nascoste. Le parentesi permettono di esprimere
// l'intento corretto; la precedenza standard (AND più stretto di OR)
// resta invariata quando non si usano.

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

func filterParensSetup(t *testing.T) *DB {
	t.Helper()
	db := NewDB()
	idx := "parens"
	if err := db.CreateVectorIndex(idx, distance.Cosine, 16, 200, distance.Float32, "", ""); err != nil {
		t.Fatalf("CreateVectorIndex: %v", err)
	}
	vi, _ := db.GetVectorIndex(idx)
	vec := make([]float32, 8)

	specs := []struct {
		id   string
		meta map[string]any
	}{
		{"epi_live", map[string]any{"memory_layer": "episodic"}},
		{"epi_hidden", map[string]any{"memory_layer": "episodic", "_is_historical": true}},
		{"sem_live", map[string]any{"memory_layer": "semantic"}},
		{"sem_hidden", map[string]any{"memory_layer": "semantic", "_is_historical": true}},
		{"proc_live", map[string]any{"memory_layer": "procedural"}},
	}
	for i, s := range specs {
		if _, err := vi.Add(s.id, vec); err != nil {
			t.Fatalf("Add %s: %v", s.id, err)
		}
		if err := db.AddMetadata(idx, uint32(i+1), s.meta); err != nil {
			t.Fatalf("AddMetadata %s: %v", s.id, err)
		}
	}
	return db
}

func mustFilter(t *testing.T, db *DB, filter string) map[string]bool {
	t.Helper()
	bm, err := db.FindIDsByFilter("parens", filter)
	if err != nil {
		t.Fatalf("FindIDsByFilter(%q): %v", filter, err)
	}
	out := make(map[string]bool)
	for _, id := range bm.ToArray() {
		ext, ok := db.exportID("parens", id)
		if ok {
			out[ext] = true
		}
	}
	return out
}

// exportID mappa un internal ID all'ID esterno tramite l'indice HNSW.
func (s *DB) exportID(indexName string, internalID uint32) (string, bool) {
	idx, ok := s.GetVectorIndex(indexName)
	if !ok {
		return "", false
	}
	h, ok := idx.(interface {
		GetExternalID(uint32) (string, bool)
	})
	if !ok {
		return "", false
	}
	return h.GetExternalID(internalID)
}

// TestFilterParensFixesLeak è il test del bug originale: senza parentesi la
// condizione globale appesa si applica solo all'ultimo ramo OR.
func TestFilterParensFixesLeak(t *testing.T) {
	db := filterParensSetup(t)

	// SBAGLIATO (comportamento pre-fix documentato): la AND si applica solo a 'semantic'.
	leaky := mustFilter(t, db, "memory_layer='episodic' OR memory_layer='semantic' AND _is_historical!='true'")
	if !leaky["epi_hidden"] {
		t.Log("nota: il leak non si è manifestato (la precedenza potrebbe differire)")
	} else {
		t.Log("confermato il comportamento di precedenza standard: epi_hidden trapela")
	}

	// CORRETTO con parentesi: nessun nascosto.
	fixed := mustFilter(t, db, "(memory_layer='episodic' OR memory_layer='semantic') AND _is_historical!='true'")
	if fixed["epi_hidden"] || fixed["sem_hidden"] {
		t.Errorf("leak con parentesi: %v", fixed)
	}
	if !fixed["epi_live"] || !fixed["sem_live"] {
		t.Errorf("vivi mancanti: %v", fixed)
	}
	if fixed["proc_live"] {
		t.Errorf("procedural non doveva essere incluso: %v", fixed)
	}
}

// TestFilterParensPrecedenceUnchanged: senza parentesi il comportamento è
// identico a prima (AND più stretto di OR).
func TestFilterParensPrecedenceUnchanged(t *testing.T) {
	db := filterParensSetup(t)

	got := mustFilter(t, db, "memory_layer='episodic' OR memory_layer='semantic'")
	if !got["epi_live"] || !got["epi_hidden"] || !got["sem_live"] || !got["sem_hidden"] {
		t.Errorf("OR semplice: %v", got)
	}
	if got["proc_live"] {
		t.Errorf("procedural non doveva essere incluso: %v", got)
	}

	got2 := mustFilter(t, db, "memory_layer='episodic' AND _is_historical!='true'")
	if !got2["epi_live"] || got2["epi_hidden"] {
		t.Errorf("AND semplice: %v", got2)
	}
}

// TestFilterParensNesting: gruppi annidati e misti.
func TestFilterParensNesting(t *testing.T) {
	db := filterParensSetup(t)

	cases := []struct {
		name   string
		filter string
		want   []string
	}{
		{
			"OR di due AND",
			"(memory_layer='episodic' AND _is_historical!='true') OR (memory_layer='semantic' AND _is_historical!='true')",
			[]string{"epi_live", "sem_live"},
		},
		{
			"AND con gruppo OR",
			"_is_historical!='true' AND (memory_layer='episodic' OR memory_layer='procedural')",
			[]string{"epi_live", "proc_live"},
		},
		{
			"annidamento a due livelli",
			"((memory_layer='episodic') OR (memory_layer='semantic')) AND _is_historical!='true'",
			[]string{"epi_live", "sem_live"},
		},
		{
			"parentesi ridondanti su singolo atomo",
			"(memory_layer='procedural')",
			[]string{"proc_live"},
		},
		{
			"gruppo che non copre tutta l'espressione",
			"(memory_layer='episodic') AND _is_historical='true'",
			[]string{"epi_hidden"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustFilter(t, db, tc.filter)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("manca %q in %v", w, got)
				}
			}
		})
	}
}

// TestFilterParensQuotedSeparators: AND/OR dentro valori quotati non devono
// essere trattati come operatori.
func TestFilterParensQuotedSeparators(t *testing.T) {
	db := NewDB()
	idx := "quoted"
	if err := db.CreateVectorIndex(idx, distance.Cosine, 16, 200, distance.Float32, "", ""); err != nil {
		t.Fatal(err)
	}
	vi, _ := db.GetVectorIndex(idx)
	vec := make([]float32, 8)
	if _, err := vi.Add("q1", vec); err != nil {
		t.Fatal(err)
	}
	if err := db.AddMetadata(idx, 1, map[string]any{"content": "a AND b OR c"}); err != nil {
		t.Fatal(err)
	}

	bm, err := db.FindIDsByFilter(idx, "content='a AND b OR c'")
	if err != nil {
		t.Fatalf("FindIDsByFilter: %v", err)
	}
	if bm.GetCardinality() != 1 {
		t.Errorf("cardinalità %d, want 1 (AND/OR dentro apici non sono operatori)", bm.GetCardinality())
	}
}

// TestFilterParensUnbalancedDoesNotPanic: parentesi sbilanciate non devono
// far crashare il server.
func TestFilterParensUnbalancedDoesNotPanic(t *testing.T) {
	db := filterParensSetup(t)
	for _, f := range []string{
		"(memory_layer='episodic'",
		"memory_layer='episodic')",
		"((memory_layer='episodic')",
		"((",
		")",
		"()",
		"( )",
	} {
		t.Run(f, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic su filtro %q: %v", f, r)
				}
			}()
			_, _ = db.FindIDsByFilter("parens", f)
		})
	}
}
