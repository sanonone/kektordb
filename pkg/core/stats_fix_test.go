package core

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// --- Set 3.1 (fix 6): DeleteMetadata must not decrement TotalDocs for nodes
// without text in a field ---

func TestDeleteMetadataTotalDocsConsistency(t *testing.T) {
	db := NewDB()
	defer db.Close()
	if err := db.CreateVectorIndex("idx", distance.Cosine, 16, 100, distance.Float32, "english", ""); err != nil {
		t.Fatal(err)
	}

	idx, _ := db.GetVectorIndex("idx")
	vec := []float32{0.1, 0.2, 0.3, 0.4}

	// Node A: text content → contributes to the "content" field stats.
	idA, _ := idx.Add("a", vec)
	if err := db.AddMetadata("idx", idA, map[string]any{"content": "hello world", "age": 30}); err != nil {
		t.Fatal(err)
	}
	// Node B: numeric-only metadata → never in the "content" DocLengths.
	idB, _ := idx.Add("b", vec)
	if err := db.AddMetadata("idx", idB, map[string]any{"count": 10}); err != nil {
		t.Fatal(err)
	}

	stats := db.textIndexStats["idx"]["content"]
	if stats == nil || stats.TotalDocs != 1 {
		t.Fatalf("expected content TotalDocs=1 after node A, got %+v", stats)
	}

	// Deleting node B (no text in "content") must NOT decrement TotalDocs.
	if err := db.DeleteMetadata("idx", idB); err != nil {
		t.Fatal(err)
	}
	if stats.TotalDocs != 1 {
		t.Errorf("TotalDocs=%d after deleting a numeric-only node, want 1 (no negative drift)", stats.TotalDocs)
	}

	// Deleting node A (had text) decrements to 0.
	if err := db.DeleteMetadata("idx", idA); err != nil {
		t.Fatal(err)
	}
	if stats.TotalDocs != 0 {
		t.Errorf("TotalDocs=%d after deleting text node, want 0", stats.TotalDocs)
	}

	// Repeated deletion of the same node must not go negative.
	if err := db.DeleteMetadata("idx", idA); err != nil {
		t.Fatal(err)
	}
	if stats.TotalDocs != 0 {
		t.Errorf("TotalDocs=%d after repeated delete, want 0 (clamped)", stats.TotalDocs)
	}
}

// --- Set 3.2 (fix 5): numeric-looking string values match "=" via the
// inverted index fallback ---

func TestEqualsFilterNumericStringValue(t *testing.T) {
	db := NewDB()
	defer db.Close()
	if err := db.CreateVectorIndex("idx", distance.Cosine, 16, 100, distance.Float32, "english", ""); err != nil {
		t.Fatal(err)
	}

	idx, _ := db.GetVectorIndex("idx")
	idStr, _ := idx.Add("str10", []float32{0.1, 0.2, 0.3, 0.4})
	if err := db.AddMetadata("idx", idStr, map[string]any{"age": "10"}); err != nil { // string
		t.Fatal(err)
	}
	idNum, _ := idx.Add("num10", []float32{0.2, 0.3, 0.4, 0.5})
	if err := db.AddMetadata("idx", idNum, map[string]any{"age": 10.0}); err != nil { // float64
		t.Fatal(err)
	}
	idOther, _ := idx.Add("other", []float32{0.3, 0.4, 0.5, 0.6})
	if err := db.AddMetadata("idx", idOther, map[string]any{"age": "11"}); err != nil {
		t.Fatal(err)
	}

	// `age = 10` must find BOTH the float64 and the string "10" (lenient union).
	set, err := db.FindIDsByFilter("idx", "age = 10")
	if err != nil {
		t.Fatalf("age = 10: %v", err)
	}
	if !set.Contains(idStr) || !set.Contains(idNum) || set.Contains(idOther) {
		t.Errorf("age = 10: want {str10, num10}, got %v", set)
	}

	// Quoted form behaves the same.
	set, err = db.FindIDsByFilter("idx", `age = "10"`)
	if err != nil {
		t.Fatalf(`age = "10": %v`, err)
	}
	if !set.Contains(idStr) || !set.Contains(idNum) {
		t.Errorf(`age = "10": want {str10, num10}, got %v`, set)
	}
}

// --- Set 3.3 (fix 8): AddMetadata auto-initializes the secondary maps for a
// manually registered index (no mid-update error / nil-map panic) ---

func TestAddMetadataAutoInitializesMaps(t *testing.T) {
	db := NewDB()
	defer db.Close()

	// Simulate an index registered without CreateVectorIndex (no secondary maps).
	idx, err := hnsw.New(16, 100, distance.Cosine, distance.Float32, "english", "")
	if err != nil {
		t.Fatal(err)
	}
	db.vectorIndexes["manual"] = idx
	db.indexLocks["manual"] = &sync.RWMutex{}

	id, err := idx.Add("n1", []float32{0.1, 0.2, 0.3, 0.4})
	if err != nil {
		t.Fatal(err)
	}

	// Before the fix: error mid-update ("metadata index not found") or a
	// nil-map assignment panic on textIndex.
	if err := db.AddMetadata("manual", id, map[string]any{
		"content": "hello world",
		"age":     30.0,
	}); err != nil {
		t.Fatalf("AddMetadata on manually-registered index: %v", err)
	}

	// The metadata must be fully indexed: filterable and searchable.
	set, err := db.FindIDsByFilter("manual", "age = 30")
	if err != nil || !set.Contains(id) {
		t.Errorf("age = 30 should match after auto-init: set=%v err=%v", set, err)
	}
}

// nonHNSWIndex is a minimal VectorIndex that is NOT *hnsw.Index — used to
// verify the type assertion in FindIDsByTextSearch fails cleanly.
type nonHNSWIndex struct{}

func (nonHNSWIndex) Add(id string, vector []float32) (uint32, error) { return 0, nil }
func (nonHNSWIndex) Delete(id string)                                {}
func (nonHNSWIndex) SearchWithScores(query []float32, k int, allowList *roaring.Bitmap, efSearch int) []types.SearchResult {
	return nil
}
func (nonHNSWIndex) Metric() distance.DistanceMetric   { return distance.Cosine }
func (nonHNSWIndex) Precision() distance.PrecisionType { return distance.Float32 }
func (nonHNSWIndex) GetArenaDir() string               { return "" }
func (nonHNSWIndex) Close() error                      { return nil }

// --- Set 3.4 (fix 10): non-HNSW index → clean error, not a panic ---

func TestFindIDsByTextSearchNonHNSWIndex(t *testing.T) {
	db := NewDB()
	defer db.Close()

	// Register a non-HNSW index. FindIDsByTextSearch rejects it at the type
	// assertion before touching any secondary map.
	db.vectorIndexes["bf"] = nonHNSWIndex{}
	db.indexLocks["bf"] = &sync.RWMutex{}

	// Must return a clean error instead of panicking on a nil *hnsw.Index.
	_, err := db.FindIDsByTextSearch("bf", "content", "hello")
	if err == nil || !strings.Contains(err.Error(), "not an HNSW index") {
		t.Errorf("expected 'not an HNSW index' error, got %v", err)
	}
}

// --- Set 3.5 (fix 11): BM25 with avgLen == 0 returns 0, not NaN ---

func TestBM25AvgLenZeroNoNaN(t *testing.T) {
	stats := &TextIndexStats{
		TotalDocs:      1,
		DocLengths:     map[uint32]int{42: 0},
		AvgFieldLength: 0, // all documents have zero indexed tokens
	}
	postings := PostingList{{DocID: 42, TermFrequency: 1}}

	db := &DB{}
	score := db.calculateBM25TermScore("hello", 42, 1, stats, map[string]PostingList{"hello": postings})
	if math.IsNaN(score) {
		t.Fatal("BM25 score is NaN for avgLen == 0")
	}
	if score != 0 {
		t.Errorf("expected 0 score when avgLen == 0, got %v", score)
	}
}
