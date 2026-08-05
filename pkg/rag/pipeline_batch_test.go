package rag

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// fakePipelineStore records batch writes and signals completion via SetState.
type fakePipelineStore struct {
	mu        sync.Mutex
	batches   [][]types.BatchObject
	stateKeys []string
	setState  chan struct{}
}

func newFakePipelineStore() *fakePipelineStore {
	return &fakePipelineStore{setState: make(chan struct{}, 16)}
}

func (f *fakePipelineStore) AddBatch(indexName string, items []types.BatchObject) error {
	f.mu.Lock()
	f.batches = append(f.batches, items)
	f.mu.Unlock()
	return nil
}

func (f *fakePipelineStore) Delete(indexName, id string) error { return nil }
func (f *fakePipelineStore) CreateVectorIndex(name, metric string, m, efC int, precision, lang string) error {
	return nil
}
func (f *fakePipelineStore) IndexExists(name string) bool { return true }

func (f *fakePipelineStore) SetState(key string, value []byte) error {
	f.mu.Lock()
	f.stateKeys = append(f.stateKeys, key)
	f.mu.Unlock()
	select {
	case f.setState <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakePipelineStore) GetState(key string) ([]byte, bool) { return nil, false }
func (f *fakePipelineStore) Search(indexName string, query []float32, k int) ([]string, error) {
	return nil, nil
}
func (f *fakePipelineStore) GetMany(indexName string, ids []string) ([]core.VectorData, error) {
	return nil, nil
}
func (f *fakePipelineStore) Link(indexName, sourceID, targetID, relationType, inverseRelationType string) error {
	return nil
}

func (f *fakePipelineStore) totalBatchObjects() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, b := range f.batches {
		total += len(b)
	}
	return total
}

// countingEmbedder records EmbedBatch calls and can simulate a failing batch.
type countingEmbedder struct {
	mu          sync.Mutex
	batchCalls  [][]string
	serialCalls int
	failBatch   bool
}

func hashVec16(text string) []float32 {
	h := fnv.New32a()
	h.Write([]byte(text))
	base := h.Sum32()
	v := make([]float32, 16)
	for i := range v {
		v[i] = float32((base>>(i*2))%1000) / 1000.0
	}
	return v
}

func (c *countingEmbedder) Embed(text string) ([]float32, error) {
	c.mu.Lock()
	c.serialCalls++
	c.mu.Unlock()
	return hashVec16(text), nil
}

func (c *countingEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batchCalls = append(c.batchCalls, append([]string(nil), texts...))
	if c.failBatch {
		return nil, fmt.Errorf("batch unsupported")
	}
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		vecs[i] = hashVec16(t)
	}
	return vecs, nil
}

func newTestPipeline(dir string, emb *countingEmbedder) *Pipeline {
	cfg := DefaultConfig()
	cfg.Name = "batch_test"
	cfg.SourcePath = dir
	cfg.IndexName = "idx"
	cfg.ChunkSize = 100
	cfg.ChunkOverlap = 20
	cfg.Parser = ParserConfig{Type: "internal"}
	return NewPipeline(cfg, newFakePipelineStore(), emb, nil, nil)
}

func waitForPipelineDone(t *testing.T, store *fakePipelineStore) {
	t.Helper()
	select {
	case <-store.setState:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for pipeline to finish")
	}
}

func TestPipelineEmbedsAllChunksInSingleBatch(t *testing.T) {
	dir := t.TempDir()
	// ~20 chunks of 100 chars: multiple chunks, single batch call expected.
	content := strings.Repeat("This is a chunk of text with enough words to be split by the splitter. ", 30)
	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	emb := &countingEmbedder{}
	p := newTestPipeline(dir, emb)
	p.Trigger()
	waitForPipelineDone(t, p.store.(*fakePipelineStore))

	emb.mu.Lock()
	defer emb.mu.Unlock()
	if len(emb.batchCalls) != 1 {
		t.Fatalf("EmbedBatch called %d times, want exactly 1", len(emb.batchCalls))
	}
	if emb.serialCalls != 0 {
		t.Errorf("serial Embed called %d times, want 0", emb.serialCalls)
	}

	store := p.store.(*fakePipelineStore)
	chunks := len(emb.batchCalls[0])
	if chunks < 2 {
		t.Fatalf("expected multiple chunks, got %d", chunks)
	}
	if got := store.totalBatchObjects(); got != chunks {
		t.Errorf("stored %d batch objects, want %d (one per chunk)", got, chunks)
	}
}

func TestPipelineFallsBackToSerialEmbeddingWhenBatchFails(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("Fallback test chunk content with several words to split. ", 20)
	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	emb := &countingEmbedder{failBatch: true}
	p := newTestPipeline(dir, emb)
	p.Trigger()
	waitForPipelineDone(t, p.store.(*fakePipelineStore))

	emb.mu.Lock()
	defer emb.mu.Unlock()
	if len(emb.batchCalls) != 1 {
		t.Fatalf("EmbedBatch attempted %d times, want exactly 1 (failed) attempt", len(emb.batchCalls))
	}
	if emb.serialCalls < 2 {
		t.Errorf("serial Embed called %d times, want >= 2 (per-chunk fallback)", emb.serialCalls)
	}

	store := p.store.(*fakePipelineStore)
	if got := store.totalBatchObjects(); got != emb.serialCalls {
		t.Errorf("stored %d batch objects, want %d (one per serial embed)", got, emb.serialCalls)
	}
}
