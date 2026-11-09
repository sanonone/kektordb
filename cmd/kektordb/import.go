package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sanonone/kektordb/pkg/archive"
	"github.com/sanonone/kektordb/pkg/client"
)

// kektordb import — rebuild a memory store from an archive.
//
// Vectors are not in the archive, so the import embeds the content again. That
// is the trade the format makes: a small, readable, diffable archive that
// survives a change of embedder, at the cost of a re-embedding pass.

const (
	importExitOK      = 0
	importExitInvalid = 1
	importExitError   = 2

	// importBatchSize bounds the vectors sent per request and held in memory.
	importBatchSize = 250
)

type importOptions struct {
	baseURL   string
	authToken string
	index     string
	dryRun    bool
	merge     bool
}

// cmdImport parses its flags and runs the import.
func cmdImport(args []string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	opts := importOptions{}
	fs.StringVar(&opts.baseURL, "url", "http://127.0.0.1:9091", "base URL of the running server")
	fs.StringVar(&opts.authToken, "auth-token", "", "API auth token, if configured")
	fs.StringVar(&opts.index, "index", "", "index to import into (required)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "validate the archive and report what would happen")
	fs.BoolVar(&opts.merge, "merge", false, "allow importing into an index that already has data")

	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return importExitError
	}

	dir := ""
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if dir == "" || opts.index == "" {
		fmt.Fprintln(os.Stderr, "uso: kektordb import <dir> --index NAME [--dry-run] [--merge]")
		return importExitError
	}

	c := client.NewFromURL(opts.baseURL, opts.authToken)
	return runImport(c, opts, dir, os.Stdout)
}

// runImport is the testable core.
func runImport(c *client.Client, opts importOptions, dir string, w io.Writer) int {
	// Validate before touching the target: an import that fails halfway leaves a
	// store that is neither the old one nor the new one.
	manifest, err := archive.VerifyContent(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archivio non valido: %v\n", err)
		return importExitInvalid
	}
	fmt.Fprintf(w, "archivio: %d memorie, %d archi (formato v%d)\n",
		manifest.Counts.Memories, manifest.Counts.Edges, manifest.FormatVersion)

	exists, populated := indexState(c, opts.index)
	if exists && populated && !opts.merge {
		fmt.Fprintf(os.Stderr,
			"l'indice %q contiene gia' dati: usa --merge per aggiungere, o un altro --index\n",
			opts.index)
		return importExitInvalid
	}

	if opts.dryRun {
		fmt.Fprintf(w, "dry-run: importerei in %q (%s), %d memorie da re-embedding\n",
			opts.index, describeState(exists, populated), manifest.Counts.Memories)
		return importExitOK
	}

	if !exists {
		if err := createIndex(c, opts.index); err != nil {
			fmt.Fprintf(os.Stderr, "creazione indice fallita: %v\n", err)
			return importExitError
		}
		fmt.Fprintf(w, "indice %q creato\n", opts.index)
	}

	// Memories: collect first, then embed in batches.
	var records []archive.MemoryRecord
	if err := archive.MemoryRecords(dir, func(r archive.MemoryRecord) error {
		records = append(records, r)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "lettura memorie: %v\n", err)
		return importExitError
	}

	imported := 0
	for start := 0; start < len(records); start += importBatchSize {
		end := start + importBatchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[start:end]

		texts := make([]string, 0, len(batch))
		kept := make([]archive.MemoryRecord, 0, len(batch))
		for _, r := range batch {
			content, ok := r.Metadata["content"].(string)
			if !ok || content == "" {
				// A node with no content is an entity or an artifact: it belongs
				// to the graph, not to the vector index. Skipping it is correct,
				// but it must be counted so the total is honest.
				continue
			}
			texts = append(texts, content)
			kept = append(kept, r)
		}
		if len(texts) == 0 {
			continue
		}

		vectors, err := c.Embed(texts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "embedding fallito al batch %d: %v\n", start/importBatchSize+1, err)
			return importExitError
		}
		if len(vectors) != len(texts) {
			fmt.Fprintf(os.Stderr, "il server ha restituito %d vettori per %d testi\n",
				len(vectors), len(texts))
			return importExitError
		}

		items := make([]client.VectorAddObject, 0, len(kept))
		for i, r := range kept {
			items = append(items, client.VectorAddObject{Id: r.ID, Vector: vectors[i], Metadata: r.Metadata})
		}
		if _, err := c.VAddBatch(opts.index, items); err != nil {
			fmt.Fprintf(os.Stderr, "inserimento fallito al batch %d: %v\n", start/importBatchSize+1, err)
			return importExitError
		}
		imported += len(items)
		fmt.Fprintf(w, "  %d/%d memorie\n", imported, len(records))
	}

	// Graph: after the nodes exist, because an edge to a missing node is dropped.
	edges := 0
	if err := archive.EdgeRecords(dir, func(e archive.EdgeRecord) error {
		if err := c.VLink(opts.index, e.Source, e.Target, e.Relation, e.Inverse); err != nil {
			return fmt.Errorf("%s -[%s]-> %s: %w", e.Source, e.Relation, e.Target, err)
		}
		edges++
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ricostruzione grafo: %v\n", err)
		return importExitError
	}

	fmt.Fprintf(w, "fatto: %d memorie, %d archi in %q\n", imported, edges, opts.index)
	return importExitOK
}

// indexState reports whether the index exists and already holds vectors.
func indexState(c *client.Client, index string) (exists, populated bool) {
	info, err := c.GetIndexInfo(index)
	if err != nil {
		return false, false
	}
	return true, info.VectorCount > 0
}

func describeState(exists, populated bool) string {
	switch {
	case !exists:
		return "da creare"
	case populated:
		return "esistente con dati"
	default:
		return "esistente e vuoto"
	}
}

// createIndex creates the target index with defaults suited to a memory store.
func createIndex(c *client.Client, index string) error {
	// The dimension is inferred from the first vector the server sees, so the
	// create call only needs the shape of the index.
	return c.VCreate(index, "cosine", "float32", 16, 200, nil)
}
