package client

import (
	"fmt"
	"log"
	"net/http"
	"testing"
	"time"
)

// NOTA: Questo è un test di INTEGRAZIONE.
// Richiede che un server KektorDB sia in esecuzione su localhost:9091.
func TestClientIntegration(t *testing.T) {
	client := New("localhost", 9091)

	// Usa un nome di indice univoco per ogni esecuzione del test per evitare conflitti.
	// Aggiungiamo un timestamp.
	indexName := fmt.Sprintf("go-client-index-%d", time.Now().UnixNano())

	t.Run("KV Store Operations", func(t *testing.T) {
		key, value := "go-client-test-kv", []byte("hello from go client")

		err := client.Set(key, value)
		if err != nil {
			t.Fatalf("Set fallito: %v", err)
		}

		retrieved, err := client.Get(key)
		if err != nil {
			t.Fatalf("Get fallito: %v", err)
		}
		if string(retrieved) != string(value) {
			t.Errorf("Get ha restituito '%s', atteso '%s'", retrieved, value)
		}

		err = client.Delete(key)
		if err != nil {
			t.Fatalf("Delete fallito: %v", err)
		}

		_, err = client.Get(key)
		if err == nil {
			t.Fatalf("Get dopo Delete avrebbe dovuto fallire, ma non l'ha fatto")
		}
		if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("Get dopo Delete ha restituito un errore inatteso: %v", err)
		}
		log.Println("[OK] Test KV superato")
	})

	t.Run("Vector Operations and Filtering", func(t *testing.T) {
		err := client.VCreate(indexName, "cosine", 16, 200)
		if err != nil {
			t.Fatalf("VCreate fallito: %v", err)
		}

		err = client.VAdd(indexName, "vec1", []float32{0.1, 0.8}, map[string]interface{}{"tag": "A", "val": 10})
		if err != nil {
			t.Fatalf("VAdd per vec1 fallito: %v", err)
		}

		err = client.VAdd(indexName, "vec2", []float32{0.9, 0.1}, map[string]interface{}{"tag": "B", "val": 20})
		if err != nil {
			t.Fatalf("VAdd per vec2 fallito: %v", err)
		}

		results, err := client.VSearch(indexName, 1, []float32{0.2, 0.7}, "tag=A")
		if err != nil {
			t.Fatalf("VSearch fallito: %v", err)
		}
		if len(results) != 1 || results[0] != "vec1" {
			t.Errorf("VSearch con filtro ha restituito %v, atteso ['vec1']", results)
		}
		log.Println("[OK] Test Vettoriale (VCreate/VAdd/VSearch) superato")
	})

	t.Run("Vector Deletion", func(t *testing.T) {
		err := client.VDelete(indexName, "vec1")
		if err != nil {
			t.Fatalf("VDelete fallito: %v", err)
		}

		// Cerca di nuovo. Ora non dovremmo trovare 'vec1'.
		results, err := client.VSearch(indexName, 2, []float32{0.1, 0.8}, "") // Senza filtro
		if err != nil {
			t.Fatalf("VSearch dopo delete fallito: %v", err)
		}

		for _, id := range results {
			if id == "vec1" {
				t.Errorf("VSearch ha trovato 'vec1' dopo che era stato eliminato. Risultati: %v", results)
			}
		}
		log.Println("[OK] Test Eliminazione Vettoriale (VDelete) superato")
	})

	t.Run("AOF Rewrite", func(t *testing.T) {
		err := client.AOFRewrite()
		if err != nil {
			t.Fatalf("AOFRewrite fallito: %v", err)
		}
		log.Println("[OK] Test Amministrazione (AOFRewrite) superato")
	})
}
