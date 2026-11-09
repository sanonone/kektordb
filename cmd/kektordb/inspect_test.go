package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/engine"
)

// Tests for the inspect command.
//
// Two things are worth pinning: the argument parsing, because the natural order
// (`inspect "query" --flag`) breaks Go's flag package, and the decision about
// when to inspect automatically versus asking.

// TestReorderFlagsFirst covers the parsing quirk.
//
// flag.Parse stops at the first non-flag argument, so the natural order a person
// types would leave every flag unparsed and fold them into the query.
func TestReorderFlagsFirst(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "query then flag",
			in:   []string{"risposte dirette", "--pick", "2"},
			want: []string{"--pick", "2", "risposte dirette"},
		},
		{
			name: "flags first unchanged",
			in:   []string{"--pick", "2", "query"},
			want: []string{"--pick", "2", "query"},
		},
		{
			name: "boolean flag does not eat the next token",
			in:   []string{"query", "--json"},
			want: []string{"--json", "query"},
		},
		{
			name: "inline value",
			in:   []string{"query", "--index=mem"},
			want: []string{"--index=mem", "query"},
		},
		{
			name: "multi word query",
			in:   []string{"a", "b", "--depth", "3"},
			want: []string{"--depth", "3", "a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderFlagsFirst(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("reorderFlagsFirst(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestLooksLikeID keeps the shortcut that avoids an embedding round trip.
func TestLooksLikeID(t *testing.T) {
	ids := []string{"mem_1759abc", "entity::marco", "core_fact_123", "reflection_9", "session_1"}
	for _, id := range ids {
		if !looksLikeID(id) {
			t.Errorf("%q was not recognised as an ID", id)
		}
	}
	queries := []string{"risposte dirette", "a memory about go", "mem", ""}
	for _, q := range queries {
		if looksLikeID(q) {
			t.Errorf("%q was mistaken for an ID", q)
		}
	}
}

// TestFlagsParseInNaturalOrder is the integration check: a person types the query
// first. If the reordering stops being applied, the flags are silently folded into
// the query and the command searches for the wrong thing.
func TestFlagsParseInNaturalOrder(t *testing.T) {
	// Reproduce what cmdInspect does with `inspect "query" --index other --depth 3`.
	args := []string{"query words", "--index", "other", "--depth", "3"}
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	var index string
	var depth int
	fs.StringVar(&index, "index", "mcp_memory", "")
	fs.IntVar(&depth, "depth", 2, "")

	if err := fs.Parse(reorderFlagsFirst(args)); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if index != "other" {
		t.Errorf("index = %q, want other: the flag was not applied", index)
	}
	if depth != 3 {
		t.Errorf("depth = %d, want 3: the flag was not applied", depth)
	}
	if got := strings.Join(fs.Args(), " "); got != "query words" {
		t.Errorf("positional = %q, want %q: flags leaked into the query", got, "query words")
	}
}

// TestAutoPickScoreIsHighEnough pins the threshold: below it, choosing for the
// reader would inspect the wrong memory and teach them something false.
func TestAutoPickScoreIsHighEnough(t *testing.T) {
	if autoPickScore < 0.9 {
		t.Errorf("autoPickScore = %.2f: too eager, the top hit may not be what the "+
			"reader meant", autoPickScore)
	}
	if autoPickScore > 1.0 {
		t.Errorf("autoPickScore = %.2f is unreachable", autoPickScore)
	}
}

// TestPrintInspectShowsTheEssentials checks the human view carries what a reader
// needs: id, content, layer, decay explanation.
func TestPrintInspectShowsTheEssentials(t *testing.T) {
	resp := server.InspectResponse{
		Found: true,
		Memory: &server.InspectMemory{
			ID:         "mem_abc",
			Index:      "mcp_memory",
			Content:    "Preferisci risposte dirette",
			Layer:      "semantic",
			Dimensions: 384,
		},
		Decay: &engine.DecayInfo{
			Factor:      0.72,
			Model:       "ebbinghaus",
			AccessCount: 4,
			Explanation: "reinforced 4 times",
		},
	}

	var buf bytes.Buffer
	printInspect(&buf, resp)
	out := buf.String()

	for _, want := range []string{
		"mem_abc", "mcp_memory", "Preferisci risposte dirette",
		"semantic", "0.72", "ebbinghaus", "4 rinforzi", "reinforced 4 times",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// TestPrintProvenanceRendersTree checks the tree is drawn with its relations.
func TestPrintProvenanceRendersTree(t *testing.T) {
	p := &server.InspectProvenance{
		DepthRequested: 2,
		DepthCovered:   2,
		TotalNodes:     2,
		Tree: []server.InspectSource{
			{
				ID: "src1", Relation: "consolidated_into", Content: "an older statement", Exists: true,
				Children: []server.InspectSource{
					{ID: "src2", Relation: "derived_from", Content: "the original", Exists: true},
				},
			},
		},
	}

	var buf bytes.Buffer
	printProvenance(&buf, p)
	out := buf.String()

	for _, want := range []string{
		"PROVENIENZA", "consolidated_into", "an older statement", "derived_from", "the original",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("provenance output missing %q:\n%s", want, out)
		}
	}
}

// TestPrintProvenanceMarksTruncation: a partial tree must say so, or the reader
// believes they saw everything.
func TestPrintProvenanceMarksTruncation(t *testing.T) {
	p := &server.InspectProvenance{
		DepthCovered: 2,
		TotalNodes:   50,
		Truncated:    true,
		Note:         "stopped at 50 nodes",
		Tree:         []server.InspectSource{{ID: "x", Relation: "derived_from", Exists: true}},
	}

	var buf bytes.Buffer
	printProvenance(&buf, p)
	out := buf.String()

	if !strings.Contains(out, "TRONCATA") {
		t.Errorf("truncation not marked:\n%s", out)
	}
	if !strings.Contains(out, "stopped at 50 nodes") {
		t.Errorf("the note explaining the truncation is missing:\n%s", out)
	}
}

// TestPrintProvenanceMarksMissingSource: a dangling edge is a fact worth showing.
func TestPrintProvenanceMarksMissingSource(t *testing.T) {
	p := &server.InspectProvenance{
		DepthCovered: 1,
		TotalNodes:   1,
		Tree: []server.InspectSource{
			{ID: "ghost", Relation: "consolidated_into", Exists: false},
		},
	}

	var buf bytes.Buffer
	printProvenance(&buf, p)
	if !strings.Contains(buf.String(), "non esiste") {
		t.Errorf("a missing source was not marked:\n%s", buf.String())
	}
}

// TestPreviewOf trims and flattens, so a candidate list stays on one line each.
func TestPreviewOf(t *testing.T) {
	if got := previewOf("short", 20); got != "short" {
		t.Errorf("previewOf truncated a short string: %q", got)
	}
	long := strings.Repeat("a", 100)
	got := previewOf(long, 20)
	// The ellipsis is a three-byte rune, so count runes rather than bytes.
	if runes := len([]rune(got)); runes > 21 {
		t.Errorf("previewOf returned %d runes for a limit of 20", runes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("previewOf did not mark the truncation: %q", got)
	}
	if got := previewOf("line one\nline two", 50); strings.Contains(got, "\n") {
		t.Error("previewOf left a newline in the preview")
	}
}

// TestAgeAndAccessStrings keeps relative times readable.
func TestAgeAndAccessStrings(t *testing.T) {
	if got := ageString(0.4); got != "oggi" {
		t.Errorf("ageString(0.4) = %q", got)
	}
	if got := ageString(1.5); got != "ieri" {
		t.Errorf("ageString(1.5) = %q", got)
	}
	if got := ageString(12); !strings.Contains(got, "12") {
		t.Errorf("ageString(12) = %q", got)
	}
	if got := accessedString(0); got != "mai" {
		t.Errorf("accessedString(0) = %q", got)
	}
	recent := time.Now().Add(-2 * time.Hour).Unix()
	if got := accessedString(recent); !strings.Contains(got, "2h") {
		t.Errorf("accessedString(2h ago) = %q", got)
	}
}
