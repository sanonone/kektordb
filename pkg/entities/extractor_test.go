package entities

import (
	"context"
	"testing"
)

// TestNormalize: due grafie della stessa entita' devono produrre lo stesso id,
// altrimenti il grafo si riempie di duplicati e smette di essere utile.
func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Marco":      "marco",
		"  MARCO  ":  "marco",
		"New   York": "new york",
		"PostgreSQL": "postgresql",
		"":           "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseLLMEntities: la risposta del modello arriva spesso dentro prose o
// fence markdown. Il parser deve essere tollerante ma non inventare entita'.
func TestParseLLMEntities(t *testing.T) {
	resp := "Here are the entities:\n```json\n" +
		`[{"name":"Marco","kind":"person"},{"name":"PostgreSQL","kind":"technology"},` +
		`{"name":"Marco","kind":"person"},{"name":"","kind":"person"}]` +
		"\n```"
	got := parseLLMEntities(resp)

	if len(got) != 2 {
		t.Fatalf("attese 2 entita' (dedup + vuota scartata), ottenute %d: %+v", len(got), got)
	}
	if got[0].Name != "marco" || got[0].Kind != KindPerson {
		t.Errorf("prima entita': %+v", got[0])
	}
	if got[1].Name != "postgresql" || got[1].Kind != KindTechnology {
		t.Errorf("seconda entita': %+v", got[1])
	}
	if got[0].Display != "Marco" {
		t.Errorf("Display deve conservare la grafia originale, got %q", got[0].Display)
	}
}

func TestParseLLMEntitiesEmpty(t *testing.T) {
	for _, resp := range []string{"", "no entities here", "[]"} {
		if got := parseLLMEntities(resp); len(got) != 0 {
			t.Errorf("risposta %q: attese 0 entita', ottenute %d", resp, len(got))
		}
	}
}

// TestParseLLMEntitiesUnknownKind: un tipo sconosciuto non deve far perdere
// l'entita': la si classifica come concetto generico.
func TestParseLLMEntitiesUnknownKind(t *testing.T) {
	got := parseLLMEntities(`[{"name":"Alpha","kind":"spaceship"}]`)
	if len(got) != 1 {
		t.Fatalf("attesa 1 entita', ottenute %d", len(got))
	}
	if got[0].Kind != KindConcept {
		t.Errorf("tipo sconosciuto deve diventare concept, got %q", got[0].Kind)
	}
}

// TestBuildModeNone: il default non deve costruire un estrattore. L'estrazione
// costa una chiamata LLM per memoria: accenderla di default sarebbe un costo
// invisibile per chi non la vuole.
func TestBuildModeNone(t *testing.T) {
	if e := Build(ModeNone, nil); e != nil {
		t.Errorf("modalita' none non deve restituire un estrattore")
	}
	if e := Build("", nil); e != nil {
		t.Errorf("modalita' vuota non deve restituire un estrattore")
	}
}

// TestBuildModeLLMWithoutClient: configurata senza LLM, meglio nessuna
// estrazione che un panic a runtime.
func TestBuildModeLLMWithoutClient(t *testing.T) {
	if e := Build(ModeLLM, nil); e != nil {
		t.Errorf("modalita' llm senza client deve restituire nil, non un estrattore")
	}
}

func TestLLMExtractorNilClient(t *testing.T) {
	e := &LLMExtractor{client: nil}
	got, err := e.Extract(context.Background(), "some text")
	if err != nil {
		t.Fatalf("client nil non deve produrre errore: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("client nil deve restituire 0 entita', ottenute %d", len(got))
	}
}
