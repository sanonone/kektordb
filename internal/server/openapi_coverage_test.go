package server

// TestOpenAPICoversRoutes impedisce che la specifica OpenAPI diverga dalle route
// realmente registrate.
//
// Prima di questo test la spec copriva 45 delle 74 route registrate: endpoint
// come belief-assessment, evolve, restore, sessions, users, metrics e
// rag/retrieve-adaptive erano assenti, quindi chi generava un client dalla spec
// aveva una visione parziale dell'API. Il test rende impossibile aggiungere una
// route senza documentarla.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// routeRegistrationRegex estrae "METODO /path" dalle chiamate mux.HandleFunc.
var routeRegistrationRegex = regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|DELETE|PATCH) (/[^"]*)"`)

// specPathRegex estrae i path documentati nella sezione paths: di openapi.yaml.
var specPathRegex = regexp.MustCompile(`(?m)^  (/[a-zA-Z0-9/{}_.-]+):`)

// TestOpenAPICoversRoutes verifica che ogni route registrata compaia nella spec.
func TestOpenAPICoversRoutes(t *testing.T) {
	handlerSrc, err := os.ReadFile("http_handlers.go")
	if err != nil {
		t.Fatalf("read http_handlers.go: %v", err)
	}
	registered := map[string]bool{}
	for _, m := range routeRegistrationRegex.FindAllStringSubmatch(string(handlerSrc), -1) {
		registered[m[2]] = true
	}
	if len(registered) == 0 {
		t.Fatal("nessuna route trovata: la regex non corrisponde più al codice")
	}

	specBytes, err := os.ReadFile(filepath.Join("ui", "static", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	documented := map[string]bool{}
	for _, m := range specPathRegex.FindAllStringSubmatch(string(specBytes), -1) {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("nessun path trovato nella spec: la regex non corrisponde più al formato")
	}

	var missing []string
	for route := range registered {
		// La UI statica e' servita da un subtree handler, non da un path esatto.
		if route == "/ui/" {
			continue
		}
		if !documented[route] {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Errorf("route registrate ma assenti da openapi.yaml (%d):\n  %s\n\n"+
			"Aggiungi i path in internal/server/ui/static/openapi.yaml.",
			len(missing), strings.Join(missing, "\n  "))
	}

	t.Logf("route registrate: %d, documentate nella spec: %d", len(registered), len(documented))
}

// TestOpenAPISchemaRefsResolve verifica che ogni $ref della spec punti a uno
// schema definito: un ref pendente rompe i generatori di client.
func TestOpenAPISchemaRefsResolve(t *testing.T) {
	specBytes, err := os.ReadFile(filepath.Join("ui", "static", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	spec := string(specBytes)

	refRegex := regexp.MustCompile(`#/components/schemas/([A-Za-z0-9_]+)`)
	schemaRegex := regexp.MustCompile(`(?m)^    ([A-Za-z0-9_]+):`)

	defined := map[string]bool{}
	for _, m := range schemaRegex.FindAllStringSubmatch(spec, -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("nessuno schema trovato: la regex non corrisponde più al formato")
	}

	var dangling []string
	for _, m := range refRegex.FindAllStringSubmatch(spec, -1) {
		if !defined[m[1]] {
			dangling = append(dangling, m[1])
		}
	}
	if len(dangling) > 0 {
		t.Errorf("$ref pendenti verso schemi inesistenti: %v", dangling)
	}
	t.Logf("schemi definiti: %d, ref risolti: tutti", len(defined))
}
