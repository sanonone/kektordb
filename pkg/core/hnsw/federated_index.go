package hnsw

import (
	"fmt"
	"hash/fnv"
	"runtime"
	"sync"

	"github.com/RoaringBitmap/roaring"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/core/types"
)

type federatedScoredResult struct {
	idx   int
	score float64
	shard int
	docID uint32
}

type FederatedIndex struct {
	shards    []*Index
	numShards int
	shardMu   []sync.RWMutex
	idToShard map[string]uint32
	mu        sync.RWMutex
}

func NewFederatedIndex(m int, efConstruction int, metric distance.DistanceMetric, precision distance.PrecisionType, numShardsArg int) (*FederatedIndex, error) {
	numShards := numShardsArg
	if numShards <= 0 {
		numShards = runtime.NumCPU()
	}
	if numShards < 4 {
		numShards = 4
	}

	shards := make([]*Index, numShards)
	for i := 0; i < numShards; i++ {
		idx, err := New(m, efConstruction, metric, precision, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to create shard %d: %w", i, err)
		}
		shards[i] = idx
	}

	return &FederatedIndex{
		shards:    shards,
		numShards: numShards,
		shardMu:   make([]sync.RWMutex, numShards),
		idToShard: make(map[string]uint32),
	}, nil
}

func (f *FederatedIndex) getShardIndex(id string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(id))
	return h.Sum32() % uint32(f.numShards)
}

func (f *FederatedIndex) getShard(id string) *Index {
	shardIdx := f.getShardIndex(id)
	return f.shards[shardIdx]
}

func (f *FederatedIndex) AddBatch(objects []types.BatchObject) error {
	if len(objects) == 0 {
		return nil
	}

	shardObjects := make([][]types.BatchObject, f.numShards)
	for _, obj := range objects {
		shardIdx := f.getShardIndex(obj.Id)
		shardObjects[shardIdx] = append(shardObjects[shardIdx], obj)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, f.numShards)

	for i := 0; i < f.numShards; i++ {
		if len(shardObjects[i]) == 0 {
			continue
		}
		wg.Add(1)
		go func(shardIdx int, objs []types.BatchObject) {
			defer wg.Done()
			if err := f.shards[shardIdx].AddBatch(objs); err != nil {
				errCh <- fmt.Errorf("shard %d: %w", shardIdx, err)
			}
		}(i, shardObjects[i])
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	f.mu.Lock()
	for _, obj := range objects {
		f.idToShard[obj.Id] = f.getShardIndex(obj.Id)
	}
	f.mu.Unlock()

	return nil
}

func (f *FederatedIndex) SearchWithScores(query []float32, k int, allowList *roaring.Bitmap, efSearch int) []types.SearchResult {
	type shardResult struct {
		results  []types.SearchResult
		shardIdx int
	}
	resultsCh := make(chan shardResult, f.numShards)
	var wg sync.WaitGroup

	queryK := k * 2
	if queryK < 50 {
		queryK = 50
	}

	for i := 0; i < f.numShards; i++ {
		wg.Add(1)
		go func(shardIdx int) {
			defer wg.Done()
			results := f.shards[shardIdx].SearchWithScores(query, queryK, allowList, efSearch)
			resultsCh <- shardResult{results: results, shardIdx: shardIdx}
		}(i)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	allScored := make([]federatedScoredResult, 0, k*f.numShards)
	idxCounter := 0
	for sr := range resultsCh {
		for _, r := range sr.results {
			allScored = append(allScored, federatedScoredResult{
				idx:   idxCounter,
				score: r.Score,
				shard: sr.shardIdx,
				docID: r.DocID,
			})
			idxCounter++
		}
	}

	if len(allScored) <= k {
		return convertToSearchResults(allScored, f)
	}

	heap := make([]federatedScoredResult, len(allScored))
	for i, r := range allScored {
		heap[i] = r
	}

	for i := len(heap)/2 - 1; i >= 0; i-- {
		federatedMaxHeapify(heap, len(heap), i)
	}

	for i := len(heap) - 1; i >= len(heap)-k; i-- {
		heap[0], heap[i] = heap[i], heap[0]
		federatedMaxHeapify(heap, i, 0)
	}

	topK := make([]federatedScoredResult, k)
	copy(topK, heap[len(heap)-k:])

	return convertToSearchResults(topK, f)
}

func convertToSearchResults(results []federatedScoredResult, f *FederatedIndex) []types.SearchResult {
	ret := make([]types.SearchResult, len(results))
	for i, r := range results {
		ret[i] = types.SearchResult{
			DocID: uint32(r.shard)<<24 | r.docID,
			Score: r.score,
		}
	}
	return ret
}

func federatedMaxHeapify(heap []federatedScoredResult, n, i int) {
	largest := i
	left := 2*i + 1
	right := 2*i + 2

	// For distance metrics (Euclidean, Cosine), LOWER score is BETTER
	// So we want a MIN-heap: smaller scores rise to the top
	if left < n && heap[left].score < heap[largest].score {
		largest = left
	}
	if right < n && heap[right].score < heap[largest].score {
		largest = right
	}

	if largest != i {
		heap[i], heap[largest] = heap[largest], heap[i]
		federatedMaxHeapify(heap, n, largest)
	}
}

func (f *FederatedIndex) GetExternalIDFromDocID(docID uint32) (string, bool) {
	shardIdx := int(docID >> 24)
	localID := docID & 0xFFFFFF

	if shardIdx >= f.numShards {
		return "", false
	}

	extID, found := f.shards[shardIdx].GetExternalID(localID)
	if !found {
		return "", false
	}

	return extID, true
}

func (f *FederatedIndex) GetNodeData(id string) (types.NodeData, bool) {
	shard := f.getShard(id)
	return shard.GetNodeData(id)
}

func (f *FederatedIndex) Delete(id string) {
	shard := f.getShard(id)
	shard.Delete(id)
	f.mu.Lock()
	delete(f.idToShard, id)
	f.mu.Unlock()
}

func (f *FederatedIndex) GetExternalID(internalID uint32, shardIdx uint32) (string, bool) {
	if int(shardIdx) >= f.numShards {
		return "", false
	}
	return f.shards[shardIdx].GetExternalID(internalID)
}

func (f *FederatedIndex) GetInfo() (distance.DistanceMetric, int, int, distance.PrecisionType, int, string) {
	var totalCount int
	var metric distance.DistanceMetric
	var m, ef int
	var precision distance.PrecisionType
	var lang string

	for _, shard := range f.shards {
		var count int
		metric, m, ef, precision, count, lang = shard.GetInfo()
		totalCount += count
	}

	return metric, m, ef, precision, totalCount, lang
}

func (f *FederatedIndex) NumShards() int {
	return f.numShards
}

func (f *FederatedIndex) Close() error {
	for _, shard := range f.shards {
		if err := shard.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (f *FederatedIndex) Add(id string, vector []float32) (uint32, error) {
	err := f.AddBatch([]types.BatchObject{{Id: id, Vector: vector}})
	if err != nil {
		return 0, err
	}
	return 0, nil
}

func (f *FederatedIndex) Metric() distance.DistanceMetric {
	return f.shards[0].Metric()
}

func (f *FederatedIndex) Precision() distance.PrecisionType {
	return f.shards[0].Precision()
}

func (f *FederatedIndex) GetArenaDir() string {
	return ""
}

func (f *FederatedIndex) GetInternalID(externalID string) (uint32, bool) {
	shard := f.getShard(externalID)
	return shard.GetInternalID(externalID)
}

func (f *FederatedIndex) GetDimension() int {
	return f.shards[0].GetDimension()
}
