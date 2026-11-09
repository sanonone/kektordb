package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/sanonone/kektordb/internal/version"
	"github.com/sanonone/kektordb/pkg/archive"
	"github.com/sanonone/kektordb/pkg/client"
)

// kektordb export — write the memory store to a directory of text files.
//
// Talks to the running server rather than reading the files on disk: the server
// is the only coherent view. Reading the AOF directly would mean reimplementing
// the replay, and exporting while a write is in flight would produce a state that
// never existed.

const (
	exportExitOK    = 0
	exportExitError = 2
)

type exportOptions struct {
	baseURL   string
	authToken string
	index     string
	outDir    string
}

// cmdExport parses its flags and runs the export.
func cmdExport(args []string) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	opts := exportOptions{}
	fs.StringVar(&opts.baseURL, "url", "http://127.0.0.1:9091", "base URL of the running server")
	fs.StringVar(&opts.authToken, "auth-token", "", "API auth token, if configured")
	fs.StringVar(&opts.index, "index", "mcp_memory", "index to export")
	fs.StringVar(&opts.outDir, "out", "", "directory to write the archive into (required)")

	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return exportExitError
	}
	if opts.outDir == "" {
		fmt.Fprintln(os.Stderr, "uso: kektordb export --out ./backup [--index NAME] [--url URL]")
		return exportExitError
	}

	c := client.NewFromURL(opts.baseURL, opts.authToken)
	return runExport(c, opts, os.Stdout)
}

// runExport is the testable core.
func runExport(c *client.Client, opts exportOptions, w io.Writer) int {
	// A non-empty target is refused rather than merged: overwriting an archive
	// silently would destroy a backup the user still needs.
	manifestPath := filepath.Join(opts.outDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		fmt.Fprintf(os.Stderr, "la directory %s contiene gia' un archivio: "+
			"scegline un'altra o spostala prima\n", opts.outDir)
		return exportExitError
	}

	if _, err := c.GetIndexInfo(opts.index); err != nil {
		fmt.Fprintf(os.Stderr, "indice %q non leggibile: %v\n", opts.index, err)
		return exportExitError
	}

	// The dimension belongs to the embedder, not the index: recording it lets an
	// importer warn when the archive was produced in a different vector space.
	dimensions := 0
	if status, err := c.EmbedderStatus(); err == nil {
		dimensions = status.Dimension
	}

	writer, err := archive.NewWriter(opts.outDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "impossibile creare l'archivio: %v\n", err)
		return exportExitError
	}

	fmt.Fprintf(w, "esporto %q in %s\n", opts.index, opts.outDir)

	// Memories are paginated: an index can hold more than one response.
	memories, err := exportMemories(c, opts.index, w)
	if err != nil {
		fmt.Fprintf(os.Stderr, "esportazione memorie fallita: %v\n", err)
		return exportExitError
	}
	for _, m := range memories {
		if err := writer.AddMemory(m); err != nil {
			fmt.Fprintf(os.Stderr, "scrittura memoria %s: %v\n", m.ID, err)
			return exportExitError
		}
	}

	// The graph is half the value: an export that drops it returns memories
	// without relations, and multi-hop recall stops working.
	edges, err := exportEdges(c, opts.index, w)
	if err != nil {
		fmt.Fprintf(os.Stderr, "esportazione grafo fallita: %v\n", err)
		return exportExitError
	}
	for _, e := range edges {
		if err := writer.AddEdge(e); err != nil {
			fmt.Fprintf(os.Stderr, "scrittura arco: %v\n", err)
			return exportExitError
		}
	}

	meta := archive.Manifest{
		KektorDBVersion: version.Version,
		Index:           opts.index,
		Embedder: archive.Embedder{
			Dimensions: dimensions,
		},
	}
	if err := writer.Close(meta); err != nil {
		fmt.Fprintf(os.Stderr, "impossibile scrivere il manifest: %v\n", err)
		return exportExitError
	}

	fmt.Fprintf(w, "fatto: %d memorie, %d archi\n", len(memories), len(edges))
	fmt.Fprintf(w, "archivio: %s\n", opts.outDir)
	return exportExitOK
}

// exportMemories pages through the index until it stops yielding.
func exportMemories(c *client.Client, index string, w io.Writer) ([]archive.MemoryRecord, error) {
	const pageSize = 1000
	var out []archive.MemoryRecord
	offset := 0

	for {
		page, hasMore, err := fetchExportPage(c, index, offset, pageSize)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if !hasMore || len(page) == 0 {
			break
		}
		offset += len(page)
		fmt.Fprintf(w, "  %d memorie...\n", len(out))
	}
	return out, nil
}

// fetchExportPage asks the server for one page of memories.
func fetchExportPage(c *client.Client, index string, offset, limit int) ([]archive.MemoryRecord, bool, error) {
	url := fmt.Sprintf("%s/vector/indexes/%s/export?limit=%d&offset=%d",
		c.BaseURL(), index, limit, offset)
	resp, err := http.Get(url) //nolint:gosec // the URL is built from the operator's own flag
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("il server ha risposto %d", resp.StatusCode)
	}

	var body struct {
		Data []struct {
			ID       string         `json:"id"`
			Metadata map[string]any `json:"metadata"`
		} `json:"data"`
		HasMore bool `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("risposta non valida: %w", err)
	}

	out := make([]archive.MemoryRecord, 0, len(body.Data))
	for _, d := range body.Data {
		out = append(out, archive.MemoryRecord{ID: d.ID, Metadata: d.Metadata})
	}
	return out, body.HasMore, nil
}

// exportEdges reads the whole graph in one pass.
//
// The graph endpoint has no pagination: the edge store is walked in memory and
// the response is bounded by the graph size, which for a personal memory store is
// tens of thousands of edges — a few megabytes.
func exportEdges(c *client.Client, index string, w io.Writer) ([]archive.EdgeRecord, error) {
	url := fmt.Sprintf("%s/vector/indexes/%s/graph/export", c.BaseURL(), index)
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// No graph endpoint yet: an archive without edges is still useful, and
		// saying so is better than failing the whole export.
		fmt.Fprintln(w, "  (il server non espone il grafo: archivio senza archi)")
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("il server ha risposto %d", resp.StatusCode)
	}

	var body struct {
		Edges []archive.EdgeRecord `json:"edges"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("risposta non valida: %w", err)
	}
	return archive.SortedEdges(body.Edges), nil
}
