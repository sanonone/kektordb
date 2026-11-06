package engine

import (
	"testing"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
)

// Tests for the ownership contract of retrieved vectors.
//
// Vectors live in a memory-mapped arena. Handing out a window onto it meant a
// caller could (a) corrupt the index by writing to the slice, or (b) crash the
// process by reading it after Close unmapped the file. The public read path now
// copies, and the index blocks Close while a fetch is in flight.

func newOwnershipEngine(t *testing.T, idx string) *Engine {
	t.Helper()
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	if err := eng.VCreate(idx, distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	return eng
}

// TestVGetReturnsOwnedCopy is the guard against reintroducing the alias.
func TestVGetReturnsOwnedCopy(t *testing.T) {
	eng := newOwnershipEngine(t, "own")
	if err := eng.VAdd("own", "doc", []float32{1, 0, 0, 0}, map[string]any{"content": "x"}); err != nil {
		t.Fatal(err)
	}

	data, err := eng.VGet("own", "doc")
	if err != nil {
		t.Fatal(err)
	}
	original := data.Vector[0]

	// A caller treating the result as its own must not reach the index.
	data.Vector[0] = 999

	again, err := eng.VGet("own", "doc")
	if err != nil {
		t.Fatal(err)
	}
	if again.Vector[0] != original {
		t.Fatalf("writing to a retrieved vector changed the index: got %v, want %v",
			again.Vector[0], original)
	}
}

// TestVGetManyReturnsOwnedCopies covers the batch path.
func TestVGetManyReturnsOwnedCopies(t *testing.T) {
	eng := newOwnershipEngine(t, "ownmany")
	for _, id := range []string{"a", "b", "c"} {
		if err := eng.VAdd("ownmany", id, []float32{1, 0, 0, 0}, map[string]any{"content": id}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := eng.VGetMany("ownmany", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results", len(res))
	}
	for i := range res {
		res[i].Vector[0] = 999
	}

	again, err := eng.VGetMany("ownmany", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range again {
		if again[i].Vector[0] == 999 {
			t.Errorf("result %d came back modified: the batch path aliases the arena", i)
		}
	}
}

// TestVGetAfterCloseDoesNotCrash is the fault that was reproduced: a background
// goroutine calling VGet after Close touched unmapped memory and the OS killed
// the process. It must now return an error or skip, never fault.
func TestVGetAfterCloseDoesNotCrash(t *testing.T) {
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := eng.VCreate("closed", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd("closed", "doc", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reading after Close must not take the process down.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("VGet panicked after Close: %v", r)
		}
	}()
	if _, err := eng.VGet("closed", "doc"); err == nil {
		t.Log("VGet returned data after Close; acceptable as long as it did not fault")
	}
}

// TestIterateAfterCloseDoesNotCrash covers the other aliasing read path.
func TestIterateAfterCloseDoesNotCrash(t *testing.T) {
	opts := DefaultOptions(t.TempDir())
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := eng.VCreate("it", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	if err := eng.VAdd("it", "doc", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("iteration panicked after Close: %v", r)
		}
	}()
	eng.DB.IterateVectorIndexes(func(indexName string, index *hnsw.Index, data core.VectorData) {
		_ = data.Vector
	})
}

// TestNilVectorStaysNil keeps the distinction between "no vector" and "empty".
func TestNilVectorStaysNil(t *testing.T) {
	if got := cloneVector(nil); got != nil {
		t.Errorf("cloneVector(nil) = %v, want nil", got)
	}
	empty := []float32{}
	if got := cloneVector(empty); got == nil {
		t.Error("cloneVector of an empty slice returned nil; the two cases are distinct")
	}
}
