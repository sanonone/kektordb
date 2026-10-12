package hnsw

// Regressione C5: l'entrypoint deve puntare a un nodo vivo, e un entrypoint
// morto non deve rendere l'indice non ricercabile.
//
// Il difetto: Index.Delete fa soft-delete (il nodo resta in nodes[] con
// Deleted=true), quindi un entrypoint cancellato non è nil. Se sta a livello
// >= 1, la discesa dal livello alto non trova nodi vivi e la ricerca ritorna
// vuoto — senza errore, quindi in modo silenzioso.

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// TestDeleteReelectsDeadEntrypoint (fix punto 3): cancellare l'entrypoint
// rielegge subito un nodo vivo.
func TestDeleteReelectsDeadEntrypoint(t *testing.T) {
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	vectors := buildVectorPool(30, 32)
	for i, v := range vectors {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	// Trova l'entrypoint e cancellalo.
	ep, ml := idx.EntrypointForTest()
	t.Logf("entrypoint=%d maxLevel=%d", ep, ml)

	// L'ID esterno del nodo entrypoint.
	ext, ok := idx.GetExternalID(ep)
	if !ok {
		t.Fatalf("entrypoint %d senza external ID", ep)
	}
	idx.Delete(ext)

	// Dopo il delete l'entrypoint deve essere vivo.
	ep2, ml2 := idx.EntrypointForTest()
	t.Logf("dopo delete: entrypoint=%d maxLevel=%d", ep2, ml2)

	nodes := idx.getNodes()
	if ep2 >= uint32(len(nodes)) || nodes[ep2] == nil {
		t.Fatalf("entrypoint %d non valido dopo il delete", ep2)
	}
	if nodes[ep2].Deleted.Load() {
		t.Errorf("entrypoint %d è ancora un nodo cancellato", ep2)
	}
	if ml2 < 0 {
		t.Errorf("maxLevel = %d, atteso >= 0 (ci sono nodi vivi)", ml2)
	}

	// La ricerca deve funzionare.
	res := idx.SearchWithScores(vectors[5], 10, nil, 100)
	if len(res) == 0 {
		t.Error("ricerca vuota dopo aver cancellato l'entrypoint")
	}
}

// TestSearchSurvivesDeadEntrypointAtHighLevel (fix punto 2): anche forzando un
// entrypoint morto a livello alto, la ricerca deve trovare i nodi vivi invece
// di ritornare vuoto.
func TestSearchSurvivesDeadEntrypointAtHighLevel(t *testing.T) {
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	vectors := buildVectorPool(50, 32)
	for i, v := range vectors {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	// Forza lo stato patologico: l'entrypoint corrente marcato morto, con
	// maxLevel gonfiato a 2 (più alto del grafo reale). È lo stato che il
	// recovery produceva quando l'entrypoint dello snapshot era cancellato.
	//
	// NOTA: gli internal ID partono da 1 (nodeCounter.Add restituisce il nuovo
	// valore), quindi nodes[0] è nil: si marca il nodo dell'entrypoint reale.
	epBefore, _ := idx.EntrypointForTest()
	idx.metaMu.Lock()
	nodes := idx.getNodes()
	if epBefore >= uint32(len(nodes)) || nodes[epBefore] == nil {
		idx.metaMu.Unlock()
		t.Fatalf("entrypoint %d non valido prima del test", epBefore)
	}
	nodes[epBefore].Deleted.Store(true)
	idx.maxLevel.Store(2) // più alto del grafo reale: il layer 1 e 2 non hanno nodi vivi
	idx.metaMu.Unlock()

	res := idx.SearchWithScores(vectors[10], 10, nil, 100)
	t.Logf("ricerca con entrypoint morto a livello 2: %d risultati", len(res))
	if len(res) == 0 {
		t.Error("BUG: ricerca vuota con entrypoint morto a livello alto")
	}
	// L'entrypoint deve essere stato rieletto.
	ep, ml := idx.EntrypointForTest()
	if ep == epBefore {
		t.Errorf("entrypoint non rieletto: %d (ancora il nodo morto)", ep)
	}
	t.Logf("entrypoint dopo la ricerca: %d maxLevel=%d", ep, ml)
}

// TestEmptyLevelDescendsInsteadOfFailing (fix punto 2, seconda parte): un
// maxLevel più alto del grafo reale non deve far fallire la ricerca.
func TestEmptyLevelDescendsInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	vectors := buildVectorPool(20, 32)
	for i, v := range vectors {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	// maxLevel gonfiato: il grafo reale ha livello 0, la ricerca partirà da 3.
	idx.metaMu.Lock()
	idx.maxLevel.Store(3)
	idx.metaMu.Unlock()

	res := idx.SearchWithScores(vectors[3], 10, nil, 100)
	t.Logf("ricerca con maxLevel gonfiato: %d risultati", len(res))
	if len(res) == 0 {
		t.Error("BUG: maxLevel gonfiato rende la ricerca vuota")
	}
}

// TestEnsureLiveEntrypointEmptyGraph: grafo senza nodi vivi -> maxLevel -1.
func TestEnsureLiveEntrypointEmptyGraph(t *testing.T) {
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	// Grafo vuoto: non deve panicare.
	idx.ensureLiveEntrypointLocked()
	if ml := idx.maxLevel.Load(); ml != -1 {
		t.Errorf("grafo vuoto: maxLevel = %d, atteso -1", ml)
	}

	// Tutti i nodi morti -> entrypoint 0, maxLevel -1.
	vectors := buildVectorPool(5, 32)
	for i, v := range vectors {
		if _, err := idx.Add(embeddingID(i), v); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	idx.metaMu.Lock()
	for _, n := range idx.getNodes() {
		if n != nil {
			n.Deleted.Store(true)
		}
	}
	idx.ensureLiveEntrypointLocked()
	idx.metaMu.Unlock()

	if ml := idx.maxLevel.Load(); ml != -1 {
		t.Errorf("tutti i nodi morti: maxLevel = %d, atteso -1", ml)
	}
	// La ricerca su un grafo tutto morto deve ritornare vuoto senza panicare.
	res := idx.SearchWithScores(vectors[0], 5, nil, 100)
	if len(res) != 0 {
		t.Errorf("grafo tutto morto: attesi 0 risultati, ottenuti %d", len(res))
	}
}

// TestAddBatchKeepsEntrypointValid: l'invariante regge anche dopo operazioni
// bulk (AddBatch aggiorna entrypoint/maxLevel in più punti).
func TestAddBatchKeepsEntrypointValid(t *testing.T) {
	dir := t.TempDir()
	idx, err := New(16, 200, distance.Cosine, distance.Float32, "", dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer idx.Close()

	vectors := buildVectorPool(200, 32)
	objects := make([]types.BatchObject, len(vectors))
	for i, v := range vectors {
		objects[i] = types.BatchObject{Id: embeddingID(i), Vector: v}
	}
	if err := idx.AddBatch(objects); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}

	ep, ml := idx.EntrypointForTest()
	nodes := idx.getNodes()
	if ep >= uint32(len(nodes)) || nodes[ep] == nil || nodes[ep].Deleted.Load() {
		t.Fatalf("AddBatch: entrypoint %d non vivo (maxLevel=%d)", ep, ml)
	}
	res := idx.SearchWithScores(vectors[0], 10, nil, 100)
	if len(res) != 10 {
		t.Errorf("ricerca dopo AddBatch: %d risultati, attesi 10", len(res))
	}
}
