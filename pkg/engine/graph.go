package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/RoaringBitmap/roaring"
	"github.com/sanonone/kektordb/pkg/core/hnsw"
	"github.com/sanonone/kektordb/pkg/persistence"
)

const (
	maxPropKeyLength   = 256
	maxPropValueLength = 4096
	maxPropsPerEdge    = 100
)

// validateProps sanitizes and validates user-provided properties to prevent injection and abuse.
func validateProps(props map[string]any) error {
	if len(props) > maxPropsPerEdge {
		return fmt.Errorf("too many properties: %d (max %d)", len(props), maxPropsPerEdge)
	}

	for key, value := range props {
		if len(key) > maxPropKeyLength {
			return fmt.Errorf("property key too long: %d chars (max %d)", len(key), maxPropKeyLength)
		}
		for _, r := range key {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
				return fmt.Errorf("invalid character in property key: %q (allowed: alphanumeric, _, -)", r)
			}
		}
		if str, ok := value.(string); ok {
			if len(str) > maxPropValueLength {
				return fmt.Errorf("property value for key %q too long: %d chars (max %d)", key, len(str), maxPropValueLength)
			}
		}
	}
	return nil
}

// VLink creates a rich directed edge between two nodes and automatically updates the reverse index.
func (e *Engine) VLink(sourceID, targetID, relationType, inverseRelationType string, weight float32, props map[string]any) error {
	if props != nil {
		if err := validateProps(props); err != nil {
			return fmt.Errorf("invalid properties: %w", err)
		}
	}

	now := time.Now().UnixNano()

	// Serialize Props to raw bytes for AOF & RAM
	var rawProps []byte
	if len(props) > 0 {
		b, err := json.Marshal(props)
		if err != nil {
			return fmt.Errorf("failed to marshal edge properties: %w", err)
		}
		rawProps = b
	}

	// 1. Persistence (AOF)
	// We use the new native command: GLINK <source> <target> <rel> <inverseRel> <weight> <props>
	weightStr := strconv.FormatFloat(float64(weight), 'f', -1, 32)
	timeStr := strconv.FormatInt(now, 10)
	cmd := persistence.FormatCommand("GLINK", []byte(sourceID), []byte(targetID), []byte(relationType), []byte(inverseRelationType), []byte(weightStr), rawProps, []byte(timeStr))

	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	// 2. In-Memory Update (Blazing fast O(1))
	e.DB.AddEdge(sourceID, targetID, relationType, weight, rawProps, now)

	// 3. Explicit Inverse Relation
	if inverseRelationType != "" {
		e.DB.AddEdge(targetID, sourceID, inverseRelationType, weight, rawProps, now)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VUnlink removes a directed edge and its reverse entry.
// If hardDelete is true, the record is physically removed.
// If hardDelete is false, it sets DeletedAt (Soft Delete).
func (e *Engine) VUnlink(sourceID, targetID, relationType, inverseRelationType string, hardDelete bool) error {
	now := time.Now().UnixNano()

	// 1. Persistence (AOF)
	hdStr := "false"
	if hardDelete {
		hdStr = "true"
	}
	timeStr := strconv.FormatInt(now, 10)
	cmd := persistence.FormatCommand("GUNLINK", []byte(sourceID), []byte(targetID), []byte(relationType), []byte(inverseRelationType), []byte(hdStr), []byte(timeStr))
	if err := e.AOF.Write(cmd); err != nil {
		return err
	}

	// 2. In-Memory Update
	e.DB.RemoveEdge(sourceID, targetID, relationType, hardDelete, now)

	// 3. Explicit Inverse Relation
	if inverseRelationType != "" {
		e.DB.RemoveEdge(targetID, sourceID, inverseRelationType, hardDelete, now)
	}

	atomic.AddInt64(&e.dirtyCounter, 1)
	return nil
}

// VGetLinks retrieves ACTIVE links. (O(1) Memory lookup)
func (e *Engine) VGetLinks(sourceID, relationType string) ([]string, bool) {
	edges, found := e.DB.GetOutEdges(sourceID, relationType, 0)
	if !found {
		return nil, false
	}
	targets := make([]string, len(edges))
	for i, edge := range edges {
		targets[i] = edge.TargetID
	}
	return targets, true
}

// VGetIncoming retrieves ACTIVE incoming links. (O(1) Reverse Index)
func (e *Engine) VGetIncoming(targetID, relationType string) ([]string, bool) {
	return e.DB.GetInEdges(targetID, relationType, 0)
}

// resolveGraphFilter traverses the graph and returns a set of allowed Internal IDs.
func (e *Engine) resolveGraphFilter(indexName string, q GraphQuery) (*roaring.Bitmap, error) {
	if q.RootID == "" {
		return nil, nil
	}

	idx, ok := e.DB.GetVectorIndex(indexName)
	if !ok {
		return nil, fmt.Errorf("index not found")
	}
	hnswIdx, ok := idx.(*hnsw.Index)
	if !ok {
		return nil, fmt.Errorf("index does not support graph operations")
	}

	allowedSet := roaring.New()
	visited := make(map[string]struct{})

	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{id: q.RootID, depth: 0}}
	visited[q.RootID] = struct{}{}

	maxDepth := q.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 5 {
		maxDepth = 5
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr.depth >= maxDepth {
			continue
		}

		processNeighbors := func(neighbors []string) {
			for _, target := range neighbors {
				if _, seen := visited[target]; !seen {
					visited[target] = struct{}{}
					if internalID, found := hnswIdx.GetInternalID(target); found {
						allowedSet.Add(internalID)
					}
					queue = append(queue, queueItem{id: target, depth: curr.depth + 1})
				}
			}
		}

		if q.Direction == "out" || q.Direction == "both" || q.Direction == "" {
			for _, rel := range q.Relations {
				targets, _ := e.VGetLinks(curr.id, rel)
				processNeighbors(targets)
			}
		}

		if q.Direction == "in" || q.Direction == "both" {
			for _, rel := range q.Relations {
				sources, _ := e.VGetIncoming(curr.id, rel)
				processNeighbors(sources)
			}
		}
	}

	return allowedSet, nil
}

// VGetRelations retrieves ALL active outgoing relationships for a node.
func (e *Engine) VGetRelations(sourceID string) map[string][]string {
	return e.DB.GetAllRelations(sourceID, "out")
}

// VGetIncomingRelations retrieves ALL active incoming relationships for a node.
func (e *Engine) VGetIncomingRelations(targetID string) map[string][]string {
	return e.DB.GetAllRelations(targetID, "in")
}

// --- Advanced Graph Operations ---

type SubgraphNode struct {
	ID       string         `json:"id"`
	Metadata map[string]any `json:"metadata"`
}

type SubgraphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Dir      string `json:"dir"`
}

type SubgraphResult struct {
	RootID string         `json:"root_id"`
	Nodes  []SubgraphNode `json:"nodes"`
	Edges  []SubgraphEdge `json:"edges"`
}

// VExtractSubgraph performs a BFS to retrieve the local neighborhood.
func (e *Engine) VExtractSubgraph(indexName, rootID string, relations []string, maxDepth int, atTime int64, guideQuery []float32, threshold float64) (*SubgraphResult, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 3 {
		maxDepth = 3
	}

	visited := make(map[string]bool)
	nodesMap := make(map[string]SubgraphNode)
	edges := make([]SubgraphEdge, 0)

	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{id: rootID, depth: 0}}
	visited[rootID] = true

	rootData, _ := e.VGet(indexName, rootID)
	nodesMap[rootID] = SubgraphNode{ID: rootID, Metadata: rootData.Metadata}

	shouldTraverse := func(targetID string) bool {
		if len(guideQuery) == 0 {
			return true
		}
		dist, err := e.computeDistance(indexName, targetID, guideQuery)
		if err != nil {
			return true
		}
		return dist <= threshold
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		for _, rel := range relations {
			// A. Outgoing Edges
			outEdges, found := e.VGetEdges(current.id, rel, atTime)
			if found {
				for _, edge := range outEdges {
					target := edge.TargetID
					if !shouldTraverse(target) {
						continue
					}

					edges = append(edges, SubgraphEdge{Source: current.id, Target: target, Relation: rel, Dir: "out"})

					if !visited[target] {
						visited[target] = true
						data, _ := e.VGet(indexName, target)
						nodesMap[target] = SubgraphNode{ID: target, Metadata: data.Metadata}
						queue = append(queue, queueItem{id: target, depth: current.depth + 1})
					}
				}
			}

			// B. Incoming Edges
			inEdges, found := e.VGetIncomingEdges(current.id, rel, atTime)
			if found {
				for _, edge := range inEdges {
					source := edge.TargetID // See API contract in VGetIncomingEdges
					if !shouldTraverse(source) {
						continue
					}

					edges = append(edges, SubgraphEdge{Source: source, Target: current.id, Relation: rel, Dir: "in"})

					if !visited[source] {
						visited[source] = true
						data, _ := e.VGet(indexName, source)
						nodesMap[source] = SubgraphNode{ID: source, Metadata: data.Metadata}
						queue = append(queue, queueItem{id: source, depth: current.depth + 1})
					}
				}
			}
		}
	}

	nodeList := make([]SubgraphNode, 0, len(nodesMap))
	for _, n := range nodesMap {
		nodeList = append(nodeList, n)
	}

	return &SubgraphResult{RootID: rootID, Nodes: nodeList, Edges: edges}, nil
}

// --- Time Travel & Rich Data Access ---

// VGetEdges retrieves full edge details for outgoing links.
func (e *Engine) VGetEdges(sourceID, relationType string, atTime int64) ([]GraphEdge, bool) {
	coreEdges, found := e.DB.GetOutEdges(sourceID, relationType, atTime)
	if !found {
		return nil, false
	}

	res := make([]GraphEdge, len(coreEdges))
	for i, ce := range coreEdges {
		res[i] = GraphEdge{
			TargetID:  ce.TargetID,
			CreatedAt: ce.CreatedAt,
			DeletedAt: ce.DeletedAt,
			Weight:    ce.Weight,
			Props:     ce.Props,
		}
	}
	return res, true
}

// VGetIncomingEdges retrieves full edge details for incoming links (Lazy Hydration).
// NOTE: By contract, the returned GraphEdge.TargetID contains the ID of the SOURCE node.
func (e *Engine) VGetIncomingEdges(targetID, relationType string, atTime int64) ([]GraphEdge, bool) {
	sources, found := e.DB.GetInEdges(targetID, relationType, atTime)
	if !found {
		return nil, false
	}

	var res []GraphEdge
	for _, sourceID := range sources {
		outEdges, ok := e.DB.GetOutEdges(sourceID, relationType, atTime)
		if ok {
			for _, oe := range outEdges {
				if oe.TargetID == targetID {
					res = append(res, GraphEdge{
						TargetID:  sourceID, // Maintain API contract!
						CreatedAt: oe.CreatedAt,
						DeletedAt: oe.DeletedAt,
						Weight:    oe.Weight,
						Props:     oe.Props,
					})
					break
				}
			}
		}
	}
	return res, len(res) > 0
}

// RunGraphVacuum executes the cleanup of old soft-deleted edges natively in RAM.
func (e *Engine) RunGraphVacuum() {
	infos, _ := e.DB.GetVectorIndexInfoUnlocked()
	var retention time.Duration

	for _, info := range infos {
		idx, _ := e.DB.GetVectorIndex(info.Name)
		if hnswIdx, ok := idx.(*hnsw.Index); ok {
			cfg := hnswIdx.GetMaintenanceConfig()
			if cfg.GraphRetention > 0 {
				retention = time.Duration(cfg.GraphRetention)
				break
			}
		}
	}

	if retention <= 0 {
		return // Keep forever
	}

	cutoffTime := time.Now().Add(-retention).UnixNano()

	// Calls the high-performance vacuum on the Core DB
	prunedTotal := e.DB.VacuumGraph(cutoffTime)

	if prunedTotal > 0 {
		slog.Info("[GraphVacuum] Cleanup complete", "pruned_edges", prunedTotal)
	}
}
