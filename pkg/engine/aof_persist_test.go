package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core/distance"
)

// TestWriteFailsWhenAOFIsBroken covers the path where every caller checked an
// error that could never be non-nil.
//
// LazyAOFWriter.Write is fire-and-forget: it enqueues and returns nil, and the
// real write happens in a goroutine whose failure is recorded in fatalErr. The
// engine checked only the enqueue result, so once the file became unwritable
// every operation kept reporting success while its command was dropped.
//
// The test breaks the underlying file, forces the writer to notice, and then
// checks that an ordinary write reports the failure instead of pretending.
func TestWriteFailsWhenAOFIsBroken(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close()

	// A first write proves the happy path.
	if err := eng.KVSet("healthy", []byte("1")); err != nil {
		t.Fatalf("healthy KVSet: %v", err)
	}

	// Break the file underneath the writer: close the descriptor the AOF holds,
	// so the next flush fails with a real OS error.
	f := eng.AOF.File()
	if f == nil {
		t.Fatal("the AOF writer exposes no file to break")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing the underlying file: %v", err)
	}

	// Force the background goroutine to attempt a flush, which records fatalErr.
	// The Flush itself returns the error; that part always worked.
	_ = eng.AOF.Flush()
	// Give the fatal error a moment to be recorded if the flush was queued.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := eng.AOF.Err(); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := eng.AOF.Err(); err == nil {
		t.Skip("could not provoke a persistent AOF failure in this environment")
	}

	// Now the operation must fail rather than report success.
	if err := eng.KVSet("broken", []byte("2")); err == nil {
		t.Fatal("KVSet reported success while the AOF is failing: the command was " +
			"dropped and the caller has no way to know")
	}
}

// TestWriteFailsOnGraphLink mirrors the same guarantee for the graph path.
func TestWriteFailsOnGraphLink(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close()

	if err := eng.VCreate("g", distance.Cosine, 16, 200, distance.Float32, "english", nil, nil, nil); err != nil {
		t.Fatalf("vcreate: %v", err)
	}
	_ = eng.VAdd("g", "a", []float32{1, 0}, nil)

	f := eng.AOF.File()
	if f == nil {
		t.Skip("no file handle exposed")
	}
	_ = f.Close()
	_ = eng.AOF.Flush()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := eng.AOF.Err(); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := eng.AOF.Err(); err == nil {
		t.Skip("could not provoke a persistent AOF failure in this environment")
	}

	if err := eng.VLink("g", "a", "a", "self", "", 1.0, nil); err == nil {
		t.Fatal("VLink reported success while the AOF is failing")
	}
}

// TestHealthyAOFSillWorks guards against an over-eager check: a working writer
// must not start rejecting operations.
func TestHealthyAOFSillWorks(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer eng.Close()

	for i := 0; i < 20; i++ {
		if err := eng.KVSet(string(rune('a'+i)), []byte("v")); err != nil {
			t.Fatalf("write %d failed on a healthy AOF: %v", i, err)
		}
	}
	if _, ok := eng.KVGet("a"); !ok {
		t.Error("the value is not readable in memory")
	}
}

// TestAOFWriterSurvivesReopen is a sanity check on the file after a clean close.
func TestAOFWriterSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	opts := DefaultOptions(dir)
	opts.AutoSaveInterval = 0
	opts.AutoSaveThreshold = 0
	eng, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := eng.KVSet("persisted", []byte("yes")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "kektordb.aof")); err != nil {
		t.Fatalf("the AOF file is missing after close: %v", err)
	}

	opts2 := DefaultOptions(dir)
	opts2.AutoSaveInterval = 0
	opts2.AutoSaveThreshold = 0
	reopened, err := Open(opts2)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if got, ok := reopened.KVGet("persisted"); !ok || string(got) != "yes" {
		t.Errorf("value after reopen = %q ok=%v, want yes", got, ok)
	}
}
