package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sanonone/kektordb/internal/server"
	"github.com/sanonone/kektordb/pkg/client"
)

// kektordb inspect — explain one memory.
//
// The awkward part is finding the memory: IDs are mem_<nanoseconds>, which
// nobody remembers and nobody types. So the argument is usually text: the
// command searches for it and, when the match is unambiguous, inspects the top
// hit. When it is not, it lists the candidates rather than guessing.

const (
	inspectExitOK       = 0
	inspectExitNotFound = 1
	inspectExitError    = 2
)

// inspectOptions is what the flags produced.
type inspectOptions struct {
	baseURL    string
	authToken  string
	index      string
	depth      int
	pick       int
	asJSON     bool
	searchOnly bool
}

// autoPickScore is the score above which the top result is unambiguous.
//
// Below it, the candidates are close enough that choosing for the reader is
// worse than asking: inspecting the wrong memory teaches them something false
// about their own store.
const autoPickScore = 0.92

// cmdInspect parses its flags and runs the command.
func cmdInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	opts := inspectOptions{}
	fs.StringVar(&opts.baseURL, "url", "http://127.0.0.1:9091", "base URL of the running server")
	fs.StringVar(&opts.authToken, "auth-token", "", "API auth token, if configured")
	fs.StringVar(&opts.index, "index", "mcp_memory", "index to inspect")
	fs.IntVar(&opts.depth, "depth", 2, "levels of provenance to follow")
	fs.IntVar(&opts.pick, "pick", 0, "choose the Nth search result instead of asking")
	fs.BoolVar(&opts.asJSON, "json", false, "output JSON instead of the human view")
	fs.BoolVar(&opts.searchOnly, "search", false, "only list matching memories, do not inspect")
	// Go's flag package stops parsing at the first non-flag argument, so
	// `inspect "some query" --pick 2` would leave --pick unparsed and treat it
	// as part of the query. People write it that way, so the flags are moved in
	// front of the positional arguments before parsing.
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return inspectExitError
	}

	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "uso: kektordb inspect <query|id> [--index NAME] [--depth N] [--pick N]")
		return inspectExitError
	}

	c := client.NewFromURL(opts.baseURL, opts.authToken)
	return runInspect(c, opts, query, os.Stdout)
}

// runInspect is the testable core: it takes a client and a writer.
func runInspect(c *client.Client, opts inspectOptions, query string, w io.Writer) int {
	id := query
	if !looksLikeID(query) {
		resolved, code := resolveID(c, opts, query, w)
		if code != inspectExitOK {
			return code
		}
		id = resolved
	}

	resp, err := fetchInspect(c, opts, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "impossibile leggere la memoria: %v\n", err)
		return inspectExitError
	}
	if !resp.Found {
		fmt.Fprintf(w, "memoria %q non trovata nell'indice %q\n", id, opts.index)
		return inspectExitNotFound
	}

	if opts.asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "errore di scrittura: %v\n", err)
			return inspectExitError
		}
		return inspectExitOK
	}

	printInspect(w, resp)
	return inspectExitOK
}

// resolveID turns a text query into an ID, asking when the match is ambiguous.
func resolveID(c *client.Client, opts inspectOptions, query string, w io.Writer) (string, int) {
	hits, err := searchMemories(c, opts.index, query, 5)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ricerca fallita: %v\n", err)
		return "", inspectExitError
	}
	if len(hits) == 0 {
		fmt.Fprintf(w, "nessuna memoria corrisponde a %q nell'indice %q\n", query, opts.index)
		return "", inspectExitNotFound
	}

	// An explicit --pick wins over the automatic choice.
	if opts.pick > 0 {
		if opts.pick > len(hits) {
			fmt.Fprintf(os.Stderr, "--pick %d ma solo %d risultati\n", opts.pick, len(hits))
			return "", inspectExitError
		}
		return hits[opts.pick-1].ID, inspectExitOK
	}

	if len(hits) == 1 || hits[0].Score >= autoPickScore {
		return hits[0].ID, inspectExitOK
	}

	// Ambiguous: show the candidates and stop. Guessing here would be worse than
	// making the reader choose.
	fmt.Fprintf(w, "\n  %d memorie corrispondono:\n\n", len(hits))
	for i, h := range hits {
		fmt.Fprintf(w, "    [%d] %.2f  %s\n", i+1, h.Score, h.Preview)
	}
	fmt.Fprintf(w, "\n  kektordb inspect %q --pick <n>\n\n", query)
	return "", inspectExitNotFound
}

// inspectHit is one search candidate.
type inspectHit struct {
	ID      string
	Score   float64
	Preview string
}

// searchMemories embeds the query and asks the server for the nearest nodes.
func searchMemories(c *client.Client, index, query string, k int) ([]inspectHit, error) {
	vecs, err := c.Embed([]string{query})
	if err != nil {
		return nil, fmt.Errorf("l'embedder del server non risponde: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("il server ha restituito un vettore vuoto per la query")
	}

	results, err := c.VSearchWithScores(index, k, vecs[0])
	if err != nil {
		return nil, err
	}

	hits := make([]inspectHit, 0, len(results))
	for _, r := range results {
		hit := inspectHit{ID: r.ID, Score: r.Score}
		// The preview is what lets a reader recognise the memory without opening
		// it; without it the list is a column of opaque IDs.
		if data, err := c.VGet(index, r.ID); err == nil && data != nil {
			if content, ok := data.Metadata["content"].(string); ok {
				hit.Preview = previewOf(content, 70)
			}
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

// fetchInspect asks the server for the full picture of one memory.
func fetchInspect(c *client.Client, opts inspectOptions, id string) (server.InspectResponse, error) {
	var out server.InspectResponse
	url := fmt.Sprintf("%s/vector/indexes/%s/inspect/%s?depth=%d",
		strings.TrimSuffix(opts.baseURL, "/"), opts.index, id, opts.depth)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return out, err
	}
	if opts.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+opts.authToken)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return out, fmt.Errorf("il server ha risposto %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("risposta non valida: %w", err)
	}
	return out, nil
}

// reorderFlagsFirst moves flag tokens before positional ones so the standard
// parser sees them, while keeping each flag next to its value.
//
// The function asks the FlagSet which flags take a value rather than keeping a
// list: a hardcoded list goes stale the moment a command adds a flag, and the
// symptom is a flag silently folded into a query.
func reorderFlagsFirst(fs *flag.FlagSet, args []string) []string {
	takesValue := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			return // a boolean does not consume the next token
		}
		takesValue[f.Name] = true
	})

	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		inlineValue := false
		if eq := strings.Index(name, "="); eq >= 0 {
			name = name[:eq]
			inlineValue = true
		}
		flags = append(flags, a)
		if !inlineValue && takesValue[name] && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

// looksLikeID reports whether the argument is an identifier rather than a query.
//
// IDs are produced by the server with recognisable prefixes, so a match here is a
// strong signal and avoids a pointless embedding round trip.
func looksLikeID(s string) bool {
	for _, prefix := range []string{"mem_", "entity::", "core_fact_", "reflection_", "knowledge_evolution_", "session_"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// printInspect renders the human view.
func printInspect(w io.Writer, resp server.InspectResponse) {
	m := resp.Memory
	fmt.Fprintf(w, "%s · %s\n\n", m.ID, m.Index)
	if m.Content != "" {
		fmt.Fprintf(w, "  %q\n\n", m.Content)
	}

	fmt.Fprintf(w, "  layer      %-12s pinned   %s\n", orDash(m.Layer), yesNo(m.Pinned))
	if m.CreatedAt > 0 {
		fmt.Fprintf(w, "  creata     %-12s letta    %s\n",
			ageString(m.AgeDays), accessedString(m.LastAccessedAt))
	}
	if d := resp.Decay; d != nil {
		fmt.Fprintf(w, "  decay      %.2f         %s", d.Factor, d.Model)
		if d.AccessCount > 0 {
			fmt.Fprintf(w, " · %d rinforzi", d.AccessCount)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)

	if d := resp.Decay; d != nil && d.Explanation != "" {
		fmt.Fprintln(w, "  PERCHÉ QUESTO VALORE")
		fmt.Fprintf(w, "    %s\n\n", d.Explanation)
	}

	printProvenance(w, resp.Provenance)
}

func printProvenance(w io.Writer, p *server.InspectProvenance) {
	if p == nil || len(p.Tree) == 0 {
		return
	}

	header := fmt.Sprintf("  PROVENIENZA  (%d livelli · %d memorie)", p.DepthCovered, p.TotalNodes)
	if p.Truncated {
		header += " · TRONCATA"
	}
	fmt.Fprintln(w, header)

	var walk func(nodes []server.InspectSource, prefix string)
	walk = func(nodes []server.InspectSource, prefix string) {
		for i, n := range nodes {
			branch := "├─"
			childPrefix := prefix + "│  "
			if i == len(nodes)-1 {
				branch = "└─"
				childPrefix = prefix + "   "
			}
			label := n.Content
			if label == "" {
				if n.Exists {
					label = n.ID
				} else {
					label = n.ID + "  (non esiste più)"
				}
			}
			fmt.Fprintf(w, "    %s%s %-20s %s\n", prefix, branch, n.Relation, label)
			walk(n.Children, childPrefix)
		}
	}
	walk(p.Tree, "")

	if p.Note != "" {
		fmt.Fprintf(w, "    %s\n", p.Note)
	}
	fmt.Fprintln(w)
}

// --- small formatting helpers ---

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "sì"
	}
	return "no"
}

func ageString(days float64) string {
	switch {
	case days < 1:
		return "oggi"
	case days < 2:
		return "ieri"
	default:
		return fmt.Sprintf("%.0f giorni fa", days)
	}
}

func accessedString(ts int64) string {
	if ts == 0 {
		return "mai"
	}
	d := time.Since(time.Unix(ts, 0))
	if d < 0 {
		return "adesso"
	}
	return strings.TrimSuffix(formatAgo(d), " fa")
}

func previewOf(s string, limit int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
