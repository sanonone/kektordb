package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Archive format for a memory store.
//
// Vectors are not exported. They are a function of the text and the embedder, so
// carrying them would make the archive large, unreadable and tied to the model
// that produced it. Regenerating them on import costs minutes and lets the
// archive survive a model change.
//
// The format is JSON Lines rather than one JSON document: it streams, a broken
// file loses one record instead of everything, and `git diff` on it is readable.
// The point is that an exported memory store stays inspectable with ordinary
// tools.

const (
	// FormatVersion is bumped when the layout changes incompatibly. Readers
	// refuse a version they do not know rather than guessing.
	FormatVersion = 1

	// FormatName identifies the archive kind, so a wrong directory is caught
	// before anything is written.
	FormatName = "kektordb-archive"

	manifestFile = "manifest.json"
	memoriesFile = "memories.jsonl"
	graphFile    = "graph.jsonl"
)

// Manifest describes an archive.
type Manifest struct {
	Format          string    `json:"format"`
	FormatVersion   int       `json:"format_version"`
	ExportedAt      time.Time `json:"exported_at"`
	KektorDBVersion string    `json:"kektordb_version"`
	Index           string    `json:"index"`
	Embedder        Embedder  `json:"embedder"`
	Counts          Counts    `json:"counts"`
	Checksum        Checksum  `json:"checksum"`
}

// Embedder records what produced the vectors. Advisory: an archive is importable
// into an index with a different embedder, because that is a legitimate thing to
// do, but the importer can warn about the change of vector space.
type Embedder struct {
	Model      string `json:"model,omitempty"`
	Dimensions int    `json:"dimensions,omitempty"`
}

// Counts is how many records the archive holds.
type Counts struct {
	Memories int `json:"memories"`
	Edges    int `json:"edges"`
}

// Checksum holds the per-file digests, so a truncated archive is caught before
// the import starts rather than halfway through.
type Checksum struct {
	Memories string `json:"memories"`
	Graph    string `json:"graph"`
}

// MemoryRecord is one line of memories.jsonl.
type MemoryRecord struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

// EdgeRecord is one line of graph.jsonl.
type EdgeRecord struct {
	Source    string         `json:"source"`
	Target    string         `json:"target"`
	Relation  string         `json:"relation"`
	Inverse   string         `json:"inverse,omitempty"`
	Weight    float32        `json:"weight,omitempty"`
	Props     map[string]any `json:"props,omitempty"`
	CreatedAt int64          `json:"created_at,omitempty"`
}

// internalMetadataPrefixes marks fields that describe the engine's own state
// rather than the user's memory. They are dropped on export: importing them
// would carry a stale internal marker into a fresh store.
var internalMetadataPrefixes = []string{
	"_decay_factor",
	"_last_decay_calc",
}

// nonPortableMetadata are keys that must never be archived even when they appear
// in metadata. `vector` is the one that matters: a caller can put anything in
// metadata, and a vector smuggled there would defeat the format's promise of
// carrying no vectors, making the archive large and tied to a model.
var nonPortableMetadata = map[string]bool{
	"vector":    true,
	"embedding": true,
}

// Writer streams an archive to a directory.
type Writer struct {
	dir      string
	memOut   *dualWriter
	graphOut *dualWriter

	counts Counts
}

// dualWriter writes to a file and a digest at once, so the checksum costs no
// second pass over the data.
type dualWriter struct {
	file *os.File
	hash hash.Hash
}

func (w *dualWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if n > 0 {
		_, _ = w.hash.Write(p[:n])
	}
	return n, err
}

// NewWriter creates the archive directory and opens the two streams.
func NewWriter(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}

	mem, err := os.Create(filepath.Join(dir, memoriesFile))
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", memoriesFile, err)
	}
	graph, err := os.Create(filepath.Join(dir, graphFile))
	if err != nil {
		mem.Close()
		return nil, fmt.Errorf("create %s: %w", graphFile, err)
	}

	return &Writer{
		dir:      dir,
		memOut:   &dualWriter{file: mem, hash: sha256.New()},
		graphOut: &dualWriter{file: graph, hash: sha256.New()},
	}, nil
}

// AddMemory writes one memory.
func (w *Writer) AddMemory(rec MemoryRecord) error {
	clean := make(map[string]any, len(rec.Metadata))
	for k, v := range rec.Metadata {
		if isInternalMetadata(k) || nonPortableMetadata[k] {
			continue
		}
		clean[k] = v
	}
	rec.Metadata = clean

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal memory %s: %w", rec.ID, err)
	}
	line = append(line, '\n')

	if _, err := w.memOut.Write(line); err != nil {
		return err
	}
	w.counts.Memories++
	return nil
}

// AddEdge writes one graph edge.
func (w *Writer) AddEdge(rec EdgeRecord) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal edge %s->%s: %w", rec.Source, rec.Target, err)
	}
	line = append(line, '\n')

	if _, err := w.graphOut.Write(line); err != nil {
		return err
	}
	w.counts.Edges++
	return nil
}

// Close writes the manifest and closes the files.
//
// The manifest goes last on purpose: it carries the counts and the digests, so a
// process killed mid-export leaves an archive with no manifest, which the reader
// refuses. An archive that claims to be complete and is not would be worse.
func (w *Writer) Close(meta Manifest) error {
	if err := w.memOut.file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", memoriesFile, err)
	}
	if err := w.graphOut.file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", graphFile, err)
	}
	if err := w.memOut.file.Close(); err != nil {
		return err
	}
	if err := w.graphOut.file.Close(); err != nil {
		return err
	}

	meta.Format = FormatName
	meta.FormatVersion = FormatVersion
	meta.Counts = w.counts
	meta.Checksum = Checksum{
		Memories: "sha256:" + hex.EncodeToString(w.memOut.hash.Sum(nil)),
		Graph:    "sha256:" + hex.EncodeToString(w.graphOut.hash.Sum(nil)),
	}
	if meta.ExportedAt.IsZero() {
		meta.ExportedAt = time.Now().UTC()
	}

	blob, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, manifestFile), append(blob, '\n'), 0o644)
}

// isInternalMetadata reports whether a key is engine state, not user memory.
func isInternalMetadata(key string) bool {
	for _, prefix := range internalMetadataPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// ReadManifest loads and validates the manifest.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest

	blob, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return m, fmt.Errorf("no %s in %s: not a KektorDB archive", manifestFile, dir)
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		return m, fmt.Errorf("malformed %s: %w", manifestFile, err)
	}
	if m.Format != FormatName {
		return m, fmt.Errorf("archive format is %q, want %q", m.Format, FormatName)
	}
	if m.FormatVersion != FormatVersion {
		return m, fmt.Errorf(
			"archive format version %d is not supported (this build reads %d)",
			m.FormatVersion, FormatVersion)
	}
	return m, nil
}

// MemoryRecords streams the memories out of an archive.
func MemoryRecords(dir string, fn func(MemoryRecord) error) error {
	return readJSONL[MemoryRecord](filepath.Join(dir, memoriesFile), fn)
}

// EdgeRecords streams the edges out of an archive.
func EdgeRecords(dir string, fn func(EdgeRecord) error) error {
	return readJSONL[EdgeRecord](filepath.Join(dir, graphFile), fn)
}

// readJSONL calls fn for each line, reporting the line number on a parse error,
// because "malformed archive" without a position is not actionable.
func readJSONL[T any](path string, fn func(T) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	line := 0
	for {
		var rec T
		if err := dec.Decode(&rec); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s line %d: %w", filepath.Base(path), line+1, err)
		}
		line++
		if err := fn(rec); err != nil {
			return err
		}
	}
}

// Verify checks the archive against its own manifest.
//
// A truncated archive that claims 5000 memories and holds 4000 is caught here,
// before the import writes anything.
func Verify(dir string) (Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return m, err
	}

	memories, edges := 0, 0
	if err := MemoryRecords(dir, func(MemoryRecord) error { memories++; return nil }); err != nil {
		return m, err
	}
	if err := EdgeRecords(dir, func(EdgeRecord) error { edges++; return nil }); err != nil {
		return m, err
	}

	if memories != m.Counts.Memories {
		return m, fmt.Errorf("archive declares %d memories but holds %d",
			m.Counts.Memories, memories)
	}
	if edges != m.Counts.Edges {
		return m, fmt.Errorf("archive declares %d edges but holds %d", m.Counts.Edges, edges)
	}
	return m, nil
}

// SortedEdges returns the edges in a stable order.
//
// Deterministic output makes the archive diff-friendly: re-exporting an unchanged
// store produces an identical file.
func SortedEdges(edges []EdgeRecord) []EdgeRecord {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Relation < b.Relation
	})
	return edges
}

// joinPath is filepath.Join, kept here so verify.go does not import path/filepath
// only for one call.
func joinPath(dir, name string) string {
	return filepath.Join(dir, name)
}
