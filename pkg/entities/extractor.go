// Package entities extracts named entities from memory content.
//
// Perche' un'astrazione: oggi l'unico estrattore disponibile e' l'LLM, ma il
// costo e' reale (una chiamata per memoria). In futuro lo stesso ruolo potra'
// essere coperto da un modello NER locale (ONNX), da regole, o da un piccolo
// classificatore: tutti gratis dal secondo uso in poi. Se l'estrattore fosse
// cablato sull'LLM, cambiare idea richiederebbe toccare ogni chiamante.
//
// L'interfaccia e' volutamente minima: dato un testo, restituisce le entita'
// con il loro tipo. Chi la implementa decide come trovarle.
package entities

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sanonone/kektordb/pkg/llm"
)

// Kind is the coarse category of an entity. Deliberately small: the point is to
// let the graph separate "people" from "technologies", not to model the world.
type Kind string

const (
	KindPerson       Kind = "person"
	KindPlace        Kind = "place"
	KindOrganization Kind = "organization"
	KindTechnology   Kind = "technology"
	KindProject      Kind = "project"
	KindConcept      Kind = "concept"
)

// Entity is one extracted item.
type Entity struct {
	// Name is the normalized surface form, used as the node id ("marco",
	// "postgresql"). Normalization matters: "Marco" and "marco" must end up on
	// the same node, otherwise the graph fills with near-duplicates and stops
	// being useful.
	Name string
	Kind Kind
	// Display is the original spelling, kept for readability in the graph.
	Display string
}

// Extractor turns text into entities.
type Extractor interface {
	// Extract returns the entities mentioned in text. An empty slice is a
	// normal result (no entity mentioned), not an error.
	Extract(ctx context.Context, text string) ([]Entity, error)
	// Name identifies the extractor in logs and reports ("llm", "ner", ...).
	Name() string
}

// Normalize prepares a surface form for use as a node id: lowercase, trimmed,
// internal whitespace collapsed. It is exported because callers building the
// graph need to agree with the extractor on the exact form.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Collapse runs of whitespace: "New   York" and "New York" are one entity.
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}

// --------------------------------------------------------------------------- LLM

// LLMExtractor asks the configured model to list the entities.
//
// Precision over recall: the prompt asks to skip generic nouns ("the user",
// "today", "a problem"). A graph polluted with generic concepts is worse than
// a sparse one, because every memory ends up connected to everything.
type LLMExtractor struct {
	client llm.Client
}

// NewLLMExtractor builds an extractor backed by the given LLM client.
func NewLLMExtractor(client llm.Client) *LLMExtractor {
	return &LLMExtractor{client: client}
}

// Name implements Extractor.
func (e *LLMExtractor) Name() string { return "llm" }

const llmEntityPrompt = `You extract named entities from a short text.

Return ONLY a JSON array. No prose, no markdown.

Each item: {"name": "...", "kind": "..."}

kind must be one of: person, place, organization, technology, project, concept

Rules:
- Include: people, places, organizations, technologies, products, named projects.
- Include: specific concepts or domains the user explicitly names ("backend engineering",
  "machine learning") when they say something about knowing or doing them.
- EXCLUDE generic nouns: "the user", "today", "a problem", "the project", "my friend".
  If it could refer to anything, it is not an entity.
- Preserve the original spelling in "name" (the caller normalizes it).
- An empty array [] is a valid answer when the text names nothing specific.

Example input: "I talked with Marco about the Alpha project; we use PostgreSQL."
Example output: [{"name":"Marco","kind":"person"},{"name":"Alpha","kind":"project"},{"name":"PostgreSQL","kind":"technology"}]`

// Extract implements Extractor.
func (e *LLMExtractor) Extract(ctx context.Context, text string) ([]Entity, error) {
	if e.client == nil || strings.TrimSpace(text) == "" {
		return nil, nil
	}

	resp, err := llm.ChatRecorded(ctx, e.client, llmEntityPrompt, text)
	if err != nil {
		return nil, fmt.Errorf("entity extraction: %w", err)
	}

	return parseLLMEntities(resp), nil
}

// parseLLMEntities is tolerant on purpose: models wrap JSON in prose or fences,
// and a malformed answer must degrade to "no entities", not break ingestion.
func parseLLMEntities(resp string) []Entity {
	if resp == "" {
		return nil
	}
	start := strings.Index(resp, "[")
	end := strings.LastIndex(resp, "]")
	if start == -1 || end == -1 || end <= start {
		return nil
	}

	var raw []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(resp[start:end+1]), &raw); err != nil {
		return nil
	}

	seen := make(map[string]bool, len(raw))
	out := make([]Entity, 0, len(raw))
	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		norm := Normalize(name)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		kind := Kind(strings.ToLower(strings.TrimSpace(r.Kind)))
		if !validKind(kind) {
			kind = KindConcept // un tipo sconosciuto non deve scartare l'entita'
		}
		out = append(out, Entity{Name: norm, Kind: kind, Display: name})
	}
	return out
}

func validKind(k Kind) bool {
	switch k {
	case KindPerson, KindPlace, KindOrganization, KindTechnology, KindProject, KindConcept:
		return true
	}
	return false
}

// --------------------------------------------------------------------------- registry

// Mode is the configured extraction strategy. "none" is the default: extraction
// costs an LLM call per memory, so it must be opted into.
type Mode string

const (
	ModeNone Mode = "none"
	ModeLLM  Mode = "llm"
)

// Build returns the extractor for a mode, or nil when extraction is disabled.
//
// This is the single place that maps configuration to implementation: adding a
// NER extractor later means adding one case here, with no change to callers.
func Build(mode Mode, client llm.Client) Extractor {
	switch Mode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ModeLLM:
		if client == nil {
			return nil // configurato ma senza LLM: meglio nessuna estrazione che un panic
		}
		return NewLLMExtractor(client)
	default:
		return nil
	}
}
