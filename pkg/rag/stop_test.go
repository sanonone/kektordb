package rag

import (
	"sync"
	"testing"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/core/types"
)

// idleStore satisfies Store so NewPipeline can start without a real engine.
// State lookups return nothing, so the initial scan finds no work.
type idleStore struct{}

func (idleStore) AddBatch(string, []types.BatchObject) error { return nil }
func (idleStore) Delete(string, string) error                { return nil }
func (idleStore) CreateVectorIndex(string, string, int, int, string, string) error {
	return nil
}
func (idleStore) IndexExists(string) bool        { return true }
func (idleStore) SetState(string, []byte) error  { return nil }
func (idleStore) GetState(string) ([]byte, bool) { return nil, false }
func (idleStore) Search(string, []float32, int) ([]string, error) {
	return nil, nil
}
func (idleStore) GetMany(string, []string) ([]core.VectorData, error) { return nil, nil }
func (idleStore) Link(string, string, string, string, string) error   { return nil }

// hangingLLM blocks until released, standing in for an LLM call stuck on a dead
// connection.
type hangingLLM struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newHangingLLM() *hangingLLM {
	return &hangingLLM{
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
}

func (h *hangingLLM) Chat(systemPrompt, userQuery string) (string, error) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return "", nil
}

func (h *hangingLLM) ChatWithImages(string, string, [][]byte) (string, error) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return "", nil
}

// TestStopReturnsWhenWorkerIsStuckInLLM covers an unbounded wait.
//
// extractionWorker calls the LLM per job and Stop waited on the WaitGroup with no
// deadline. A call stuck on a dead connection therefore kept Stop blocked
// forever — and Stop is on the shutdown path, so the process would not exit.
func TestStopReturnsWhenWorkerIsStuckInLLM(t *testing.T) {
	llm := newHangingLLM()
	p := NewPipeline(Config{
		GraphEntityExtraction: true,
		StopTimeout:           200 * time.Millisecond,
		PollingInterval:       time.Hour, // no background scans during the test
	}, idleStore{}, nil, llm, nil)
	p.Start()

	// Put one job in the queue so the worker enters the LLM call and blocks.
	select {
	case p.extractionChan <- extractionJob{ChunkID: "c1", Text: "some text to extract"}:
	case <-time.After(2 * time.Second):
		t.Fatal("could not enqueue a job")
	}

	select {
	case <-llm.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never reached the LLM call")
	}

	stopped := make(chan struct{})
	go func() {
		p.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Stop returned despite the stuck worker: the deadline works.
	case <-time.After(5 * time.Second):
		close(llm.release)
		t.Fatal("Stop never returned: the wait on the extraction worker has no bound")
	}

	close(llm.release)
}

// TestStopDoesNotPanicOnConcurrentSend covers a race the old Stop created by
// closing extractionChan while producers were selecting on a send to it.
//
// With both stopCh and extractionChan closed, Go picks a ready case at random,
// and the send branch panics: "send on closed channel". The fix stops closing the
// channel and signals through stopCh only.
func TestStopDoesNotPanicOnConcurrentSend(t *testing.T) {
	p := NewPipeline(Config{
		GraphEntityExtraction: true,
		StopTimeout:           50 * time.Millisecond,
		PollingInterval:       time.Hour,
	}, idleStore{}, nil, newHangingLLM(), nil)
	p.Start()

	panics := make(chan any, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				panics <- r
			}
		}()
		for i := 0; i < 2000; i++ {
			select {
			case <-p.stopCh:
				return
			case p.extractionChan <- extractionJob{ChunkID: "x", Text: "y"}:
			default:
			}
		}
	}()

	time.Sleep(5 * time.Millisecond)
	p.Stop()
	wg.Wait()

	select {
	case r := <-panics:
		t.Fatalf("a concurrent enqueue panicked during Stop: %v", r)
	default:
	}
}

// TestStopIsIdempotent keeps the existing contract.
func TestStopIsIdempotent(t *testing.T) {
	p := NewPipeline(Config{GraphEntityExtraction: false, PollingInterval: time.Hour}, idleStore{}, nil, nil, nil)
	p.Start()
	p.Stop()
	p.Stop() // must not panic (stopOnce)
}

// TestStopWithoutExtractionReturns keeps the fast path fast.
func TestStopWithoutExtractionReturns(t *testing.T) {
	p := NewPipeline(Config{GraphEntityExtraction: false, PollingInterval: time.Hour}, idleStore{}, nil, nil, nil)
	p.Start()

	done := make(chan struct{})
	go func() { p.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked although extraction is disabled")
	}
}
