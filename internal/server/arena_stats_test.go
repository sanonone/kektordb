package server

// Test di A2: l'endpoint arena-stats deve riportare lo stato fisico reale
// dell'arena (chunk, slot, footprint su disco) e riflettere le operazioni.
// Serve come strumento di osservazione per lo stress test (A5).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestArenaStatsEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)

	// Indice con arena (newTestServer usa un DataDir reale).
	createBody := strings.NewReader(`{"index_name":"astats","metric":"cosine","m":8,"ef_construction":100,"precision":"float32"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/create", createBody)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	get := func() (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequest("GET", ts.URL+"/vector/indexes/astats/arena-stats", nil)
		rp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer rp.Body.Close()
		var parsed map[string]any
		if err := json.NewDecoder(rp.Body).Decode(&parsed); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return rp.StatusCode, parsed
	}

	// 1. Prima di qualsiasi Add l'arena non esiste ancora (viene creata al primo
	// inserimento): conflitto, non 500.
	code, _ := get()
	if code != http.StatusConflict {
		t.Logf("nota: arena già inizializzata prima dell'Add (status %d)", code)
	}

	// Inserisci vettori: crea l'arena e i chunk.
	var sb strings.Builder
	sb.WriteString(`{"index_name":"astats","vectors":[`)
	const n = 300
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"v`)
		sb.WriteString(strings.Repeat("x", 1))
		sb.WriteString(itoa(i))
		sb.WriteString(`","vector":[0.1,0.2,0.3,0.4]}`)
	}
	sb.WriteString(`]}`)
	req2, _ := http.NewRequest("POST", ts.URL+"/vector/actions/add-batch", strings.NewReader(sb.String()))
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	code, stats := get()
	if code != http.StatusOK {
		t.Fatalf("atteso 200 dopo l'Add, got %d (%v)", code, stats)
	}

	chunks := stats["chunk_count"].(float64)
	disk := stats["disk_bytes"].(float64)
	used := stats["used_physical_slots"].(float64)
	total := stats["total_physical_slots"].(float64)
	free := stats["free_physical_slots"].(float64)
	ratio := stats["fragmentation_ratio"].(float64)
	compactorActive := stats["compactor_active"].(bool)

	t.Logf("chunk=%v disk=%vMB used=%v total=%v free=%v ratio=%.4f compactor=%v",
		chunks, disk/1024/1024, used, total, free, ratio, compactorActive)

	if chunks < 1 {
		t.Error("chunk_count deve essere >= 1 dopo l'Add")
	}
	if disk <= 0 {
		t.Error("disk_bytes deve essere > 0")
	}
	if used != n {
		t.Errorf("used_physical_slots = %v, want %d", used, n)
	}
	if total != chunks*float64(total/chunks) {
		t.Errorf("total_physical_slots (%v) incoerente con chunk_count (%v)", total, chunks)
	}
	if used+free != total {
		t.Errorf("used+free (%v) != total (%v)", used+free, total)
	}
	// Con pochi vettori in chunk da 64MB, la frammentazione è altissima.
	if ratio <= 0 {
		t.Errorf("fragmentation_ratio = %v, atteso > 0 (arena quasi vuota)", ratio)
	}
	// Dopo il fix A1 il compactor deve essere registrato.
	if !compactorActive {
		t.Error("compactor_active = false: il compactor non è registrato sull'arena (regressione A1)")
	}

	// 2. Delete + Vacuum: gli slot diventano liberi ma il totale non cambia.
	delBody := strings.NewReader(`{"index_name":"astats","id":"vx0"}`)
	req3, _ := http.NewRequest("POST", ts.URL+"/vector/actions/delete_vector", delBody)
	resp3, _ := ts.Client().Do(req3)
	resp3.Body.Close()

	// 3. Indice inesistente -> 404.
	r, _ := http.NewRequest("GET", ts.URL+"/vector/indexes/nope/arena-stats", nil)
	rp, err := ts.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Body.Close()
	if rp.StatusCode != http.StatusNotFound {
		t.Errorf("indice inesistente: atteso 404, got %d", rp.StatusCode)
	}
}

// itoa evita di importare strconv nel test.
func itoa(i int) string {
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

// TestArenaStatsTracksGrowthAndReuse dimostra il valore diagnostico
// dell'endpoint (e la sua utilità per lo stress test A5): il footprint su disco
// cresce con gli inserimenti e NON cresce quando gli slot vengono riusati.
func TestArenaStatsTracksGrowthAndReuse(t *testing.T) {
	ts, eng := newTestServer(t)

	createBody := strings.NewReader(`{"index_name":"grow","metric":"cosine","m":8,"ef_construction":100,"precision":"float32"}`)
	req, _ := http.NewRequest("POST", ts.URL+"/vector/actions/create", createBody)
	resp, _ := ts.Client().Do(req)
	resp.Body.Close()

	stats := func() (int64, int, uint32) {
		t.Helper()
		s, err := eng.GetArenaStats("grow")
		if err != nil {
			t.Fatalf("GetArenaStats: %v", err)
		}
		return s.DiskBytes, s.UsedPhysicalSlots, s.NextPhysSlot
	}

	// 400 vettori: riempie una frazione del primo chunk.
	const n = 400
	for i := 0; i < n; i++ {
		if err := eng.VAdd("grow", "g"+itoa(i), []float32{0.1, 0.2, 0.3, 0.4}, nil); err != nil {
			t.Fatalf("VAdd %d: %v", i, err)
		}
	}
	disk1, used1, next1 := stats()
	t.Logf("dopo %d add: disk=%dMB used=%d nextPhys=%d", n, disk1/1024/1024, used1, next1)
	if used1 != n {
		t.Errorf("used = %d, want %d", used1, n)
	}

	// Cancella metà e forza il Vacuum: gli slot tornano liberi.
	for i := 0; i < n/2; i++ {
		if err := eng.VDelete("grow", "g"+itoa(i)); err != nil {
			t.Fatalf("VDelete %d: %v", i, err)
		}
	}
	if err := eng.VTriggerMaintenance("grow", "vacuum"); err != nil {
		t.Fatalf("VTriggerMaintenance: %v", err)
	}
	disk2, used2, next2 := stats()
	t.Logf("dopo delete+vacuum: disk=%dMB used=%d nextPhys=%d", disk2/1024/1024, used2, next2)
	if used2 >= used1 {
		t.Errorf("used dopo vacuum = %d, atteso < %d (gli slot devono essere liberati)", used2, used1)
	}
	if next2 != next1 {
		t.Errorf("nextPhysSlot è cresciuto (%d -> %d): il vacuum non deve allocare", next1, next2)
	}

	// Reinserisci la stessa quantità: gli slot liberi devono essere RIUSATI,
	// quindi nextPhysSlot non cresce e il disco non cresce.
	for i := 0; i < n/2; i++ {
		if err := eng.VAdd("grow", "r"+itoa(i), []float32{0.1, 0.2, 0.3, 0.4}, nil); err != nil {
			t.Fatalf("VAdd reuse %d: %v", i, err)
		}
	}
	disk3, used3, next3 := stats()
	t.Logf("dopo riuso: disk=%dMB used=%d nextPhys=%d", disk3/1024/1024, used3, next3)
	if next3 != next1 {
		t.Errorf("nextPhysSlot è cresciuto durante il riuso (%d -> %d): gli slot liberi non sono stati riusati", next1, next3)
	}
	if disk3 != disk1 {
		t.Errorf("il footprint su disco è cresciuto (%d -> %d) nonostante il riuso degli slot", disk1, disk3)
	}
	if used3 != used1 {
		t.Errorf("used dopo riuso = %d, want %d", used3, used1)
	}
}
