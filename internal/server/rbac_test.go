package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sanonone/kektordb/pkg/engine"
)

// Helper function to make HTTP requests during tests
func makeRequest(t *testing.T, handler http.Handler, method, path, token string, body map[string]any) *httptest.ResponseRecorder {
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}

	req := httptest.NewRequest(method, path, bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestRBACAndNamespaces(t *testing.T) {
	// 1. Setup Engine & Server with a Master Token
	tmpDir := t.TempDir()
	opts := engine.DefaultOptions(tmpDir)
	opts.AutoSaveInterval = 0 // Disabilita autosave per i test
	eng, err := engine.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	masterToken := "super-secret-admin"
	srv, err := NewServer(eng, ":0", "", masterToken, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Per testare il middleware, usiamo direttamente il router principale del server
	mux := http.NewServeMux()
	srv.registerHTTPHandlers(mux)

	var handler http.Handler = mux
	handler = srv.authMiddleware(handler)
	handler = srv.LoggingMiddleware(handler)
	handler = srv.RecoveryMiddleware(handler)

	// --- FASE 1: Creazione Indici e Dati Iniziali ---

	res := makeRequest(t, handler, "POST", "/vector/actions/create", masterToken, map[string]any{
		"index_name": "tenant_A",
		"metric":     "euclidean",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("Failed to create tenant_A: %s", res.Body.String())
	}

	makeRequest(t, handler, "POST", "/vector/actions/create", masterToken, map[string]any{
		"index_name": "tenant_B",
		"metric":     "euclidean",
	})

	// --- FASE 2: Creazione delle API Keys tramite l'Admin ---

	// Chiave 1: Read-Only per tenant_A
	res = makeRequest(t, handler, "POST", "/auth/keys", masterToken, map[string]any{
		"description": "Reader for A",
		"role":        "read",
		"namespaces":  []string{"tenant_A"},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("Failed to create Read token: %s", res.Body.String())
	}
	var readTokenA map[string]any
	json.Unmarshal(res.Body.Bytes(), &readTokenA)
	tokenReadA := readTokenA["token"].(string)

	// Chiave 2: Write per tenant_B
	res = makeRequest(t, handler, "POST", "/auth/keys", masterToken, map[string]any{
		"description": "Writer for B",
		"role":        "write",
		"namespaces":  []string{"tenant_B"},
	})
	var writeTokenB map[string]any
	json.Unmarshal(res.Body.Bytes(), &writeTokenB)
	tokenWriteB := writeTokenB["token"].(string)

	// --- FASE 3: Test dei Limiti RBAC (Role-Based Access Control) ---

	t.Run("Nessun Token -> 401 Unauthorized", func(t *testing.T) {
		res := makeRequest(t, handler, "GET", "/vector/indexes", "", nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", res.Code)
		}
	})

	t.Run("Token Sbagliato -> 401 Unauthorized", func(t *testing.T) {
		res := makeRequest(t, handler, "GET", "/vector/indexes", "fake-token", nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", res.Code)
		}
	})

	t.Run("Read Token cerca di fare VAdd -> 403 Forbidden", func(t *testing.T) {
		res := makeRequest(t, handler, "POST", "/vector/actions/add", tokenReadA, map[string]any{
			"index_name": "tenant_A", // Namespace corretto, ma azione vietata (Read vs Write)
			"id":         "doc_1",
			"vector":     []float32{0.1, 0.2},
		})
		if res.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for Read-Only trying to write, got %d", res.Code)
		}
	})

	t.Run("Read Token prova ad accedere a System -> 403 Forbidden", func(t *testing.T) {
		res := makeRequest(t, handler, "POST", "/system/save", tokenReadA, nil)
		if res.Code != http.StatusForbidden {
			t.Errorf("Expected 403, got %d", res.Code)
		}
	})

	t.Run("Write Token fa VAdd -> 200 OK", func(t *testing.T) {
		res := makeRequest(t, handler, "POST", "/vector/actions/add", tokenWriteB, map[string]any{
			"index_name": "tenant_B",
			"id":         "doc_1",
			"vector":     []float32{0.1, 0.2},
		})
		if res.Code != http.StatusOK {
			t.Errorf("Expected 200 OK for Write Token, got %d: %s", res.Code, res.Body.String())
		}
	})

	// --- FASE 4: Test dei Namespace (Isolamento) ---

	t.Run("Write Token B prova a scrivere su Tenant A -> 403 Forbidden", func(t *testing.T) {
		// La chiave ha ruolo Write (corretto) ma per il namespace sbagliato
		res := makeRequest(t, handler, "POST", "/vector/actions/add", tokenWriteB, map[string]any{
			"index_name": "tenant_A",
			"id":         "doc_hacker",
			"vector":     []float32{0.9, 0.9},
		})
		if res.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for cross-tenant write, got %d", res.Code)
		}
	})

	t.Run("Read Token A prova a leggere da Tenant B -> 403 Forbidden", func(t *testing.T) {
		res := makeRequest(t, handler, "POST", "/vector/actions/search", tokenReadA, map[string]any{
			"index_name":   "tenant_B",
			"query_vector": []float32{0.1, 0.2},
			"k":            10,
		})
		if res.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden for cross-tenant read, got %d", res.Code)
		}
	})

	t.Run("Read Token A legge da Tenant A -> 200 OK", func(t *testing.T) {
		res := makeRequest(t, handler, "POST", "/vector/actions/search", tokenReadA, map[string]any{
			"index_name":   "tenant_A",
			"query_vector": []float32{0.1, 0.2},
			"k":            10,
		})
		if res.Code != http.StatusOK {
			t.Errorf("Expected 200 OK for valid namespace read, got %d: %s", res.Code, res.Body.String())
		}
	})

	// --- FASE 5: Grafo Multi-tenant ---

	t.Run("Isolamento Grafo (Prefissi Interni)", func(t *testing.T) {
		// Scriviamo nel grafo del tenant_B (valido)
		res := makeRequest(t, handler, "POST", "/graph/actions/link", tokenWriteB, map[string]any{
			"index_name":    "tenant_B",
			"source_id":     "doc_1",
			"target_id":     "doc_2",
			"relation_type": "next",
		})
		if res.Code != http.StatusOK {
			t.Fatalf("Failed to write to graph: %s", res.Body.String())
		}

		// Proviamo a leggere lo stesso source_id dal tenant_A con il master token
		// Siccome usiamo i prefissi (tenant_A::doc_1), tenant_A non deve vedere l'arco "next" di doc_1 creato in tenant_B.
		res = makeRequest(t, handler, "POST", "/graph/actions/get-links", masterToken, map[string]any{
			"index_name":    "tenant_A",
			"source_id":     "doc_1",
			"relation_type": "next",
		})

		var response map[string]any
		json.Unmarshal(res.Body.Bytes(), &response)

		// `targets` dovrebbe essere nullo o vuoto per tenant_A
		if targets, ok := response["targets"].([]interface{}); ok && len(targets) > 0 {
			t.Errorf("Tenant A found graph edges belonging to Tenant B! Isolation failed. Targets: %v", targets)
		}
	})

	// --- FASE 6: Revoca Token ---

	t.Run("Revoca Token impedisce l'accesso", func(t *testing.T) {
		policyData := readTokenA["policy"].(map[string]any)
		keyID := policyData["id"].(string) // Questo ora contiene l'hash della chiave (vedi fix sotto)

		// L'admin revoca la chiave usando l'ID (l'Hash)
		res := makeRequest(t, handler, "DELETE", "/auth/keys/"+keyID, masterToken, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("Failed to revoke key: %s", res.Body.String())
		}

		// Il client A prova a leggere di nuovo -> 401
		res = makeRequest(t, handler, "POST", "/vector/actions/search", tokenReadA, map[string]any{
			"index_name":   "tenant_A",
			"query_vector": []float32{0.1, 0.2},
			"k":            10,
		})

		if res.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 Unauthorized after key revocation, got %d", res.Code)
		}
	})
}
