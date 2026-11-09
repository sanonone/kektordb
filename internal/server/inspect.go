package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
	"github.com/sanonone/kektordb/pkg/engine"
)

// Inspect: explain one memory.
//
// A memory store that cannot answer "why does this exist?" is a black box. The
// engine records provenance as graph edges and decay as a computed factor; this
// endpoint exposes both for one node, so a reader can see where a memory came
// from and why it ranks where it does.

// inspectProvenanceRelations are the edges that mean "this node produced that
// one". Direction matters: consolidation links the source to the master with
// `consolidated_into`, so the sources are the incoming edges.
var inspectProvenanceRelations = []string{
	"consolidated_into", // Gardener merged this into a master memory
	"extracted_from",    // a core fact was extracted from this memory
	"derived_from",      // inverse of consolidated_into
	"evolution_of",      // the node was produced by evolving this one
	"compiled_from",     // the artifact was compiled from these sources
}

const (
	// inspectMaxNodes bounds a provenance walk. Beyond this the answer stops
	// fitting on a screen, and a bounded answer beats a slow one.
	inspectMaxNodes = 50
	// inspectMaxDepth bounds recursion, as a second defence against a graph
	// that is deep as well as wide.
	inspectMaxDepth = 10
	// inspectContentPreview is how much of a source's content to inline.
	inspectContentPreview = 120
)

// InspectResponse is the answer for one memory.
type InspectResponse struct {
	Found      bool               `json:"found"`
	Memory     *InspectMemory     `json:"memory,omitempty"`
	Decay      *engine.DecayInfo  `json:"decay,omitempty"`
	Provenance *InspectProvenance `json:"provenance,omitempty"`
	Error      string             `json:"error,omitempty"`
}

// InspectMemory is the node itself.
type InspectMemory struct {
	ID             string         `json:"id"`
	Index          string         `json:"index"`
	Content        string         `json:"content"`
	Type           string         `json:"type,omitempty"`
	Layer          string         `json:"layer,omitempty"`
	Pinned         bool           `json:"pinned"`
	CreatedAt      int64          `json:"created_at,omitempty"`
	AgeDays        float64        `json:"age_days,omitempty"`
	LastAccessedAt int64          `json:"last_accessed_at,omitempty"`
	AccessCount    int            `json:"access_count,omitempty"`
	Dimensions     int            `json:"dimensions"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// InspectProvenance is the tree of sources.
type InspectProvenance struct {
	DepthRequested int             `json:"depth_requested"`
	DepthCovered   int             `json:"depth_covered"`
	Truncated      bool            `json:"truncated"`
	TotalNodes     int             `json:"total_nodes"`
	Tree           []InspectSource `json:"tree"`
	Note           string          `json:"note,omitempty"`
}

// InspectSource is one node in the provenance tree.
type InspectSource struct {
	ID       string          `json:"id"`
	Relation string          `json:"relation"`
	Content  string          `json:"content,omitempty"`
	Exists   bool            `json:"exists"`
	Children []InspectSource `json:"children,omitempty"`
}

// handleInspect serves GET /vector/indexes/{name}/inspect/{id}?depth=N
func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	id := r.PathValue("id")
	if indexName == "" || id == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name and id are required"))
		return
	}

	// Two levels by default: the direct sources and one step of context, which
	// is what makes an answer useful without turning it into a graph dump.
	depth := 2
	if raw := r.URL.Query().Get("depth"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			s.writeHTTPError(w, http.StatusBadRequest,
				fmt.Errorf("depth must be a non-negative integer"))
			return
		}
		depth = n
	}

	resp := s.BuildInspect(indexName, id, depth)
	if !resp.Found {
		s.writeHTTPResponse(w, http.StatusNotFound, resp)
		return
	}
	s.writeHTTPResponse(w, http.StatusOK, resp)
}

// BuildInspect assembles the answer. Exported for tests and for the MCP tool.
func (s *Server) BuildInspect(indexName, id string, depth int) InspectResponse {
	data, err := s.Engine.VGet(indexName, id)
	if err != nil {
		return InspectResponse{Found: false, Error: err.Error()}
	}

	if depth > inspectMaxDepth {
		depth = inspectMaxDepth
	}

	resp := InspectResponse{
		Found:  true,
		Memory: buildInspectMemory(indexName, id, data),
	}

	if info, err := s.Engine.DecayExplanation(indexName, id); err == nil {
		resp.Decay = &info
	}

	resp.Provenance = s.buildProvenance(indexName, id, depth)
	return resp
}

// buildInspectMemory copies the fields a reader cares about out of the node.
//
// The raw metadata is included too: hiding it would make the view useless for
// the edge cases where the answer is in a field nobody thought to surface.
func buildInspectMemory(indexName, id string, data core.VectorData) *InspectMemory {
	m := &InspectMemory{
		ID:         id,
		Index:      indexName,
		Dimensions: len(data.Vector),
		Metadata:   data.Metadata,
	}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}

	if v, ok := m.Metadata["content"].(string); ok {
		m.Content = v
	}
	if v, ok := m.Metadata["type"].(string); ok {
		m.Type = v
	}
	if v, ok := m.Metadata["memory_layer"].(string); ok {
		m.Layer = v
	}
	if v, ok := m.Metadata["_pinned"].(bool); ok {
		m.Pinned = v
	}
	if v, ok := metadataFloat(m.Metadata, "_created_at"); ok {
		m.CreatedAt = int64(v)
		m.AgeDays = ageInDays(m.CreatedAt)
	}
	if v, ok := metadataFloat(m.Metadata, "_last_accessed"); ok {
		m.LastAccessedAt = int64(v)
	}
	if v, ok := metadataFloat(m.Metadata, "_access_count"); ok {
		m.AccessCount = int(v)
	}
	return m
}

// metadataFloat reads a numeric metadata field, accepting the types that survive
// a JSON round trip or a direct Go write.
func metadataFloat(meta map[string]any, key string) (float64, bool) {
	v, ok := meta[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// buildProvenance walks the incoming edges breadth-first, bounded by depth and
// total node count.
func (s *Server) buildProvenance(indexName, id string, depth int) *InspectProvenance {
	out := &InspectProvenance{
		DepthRequested: depth,
		Tree:           []InspectSource{},
	}
	if depth <= 0 {
		return out
	}

	budget := inspectMaxNodes
	visited := map[string]bool{id: true}

	var walk func(nodeID string, remaining int) ([]InspectSource, int)
	// Returns the children found and the deepest level actually reached.
	walk = func(nodeID string, remaining int) ([]InspectSource, int) {
		if remaining <= 0 || budget <= 0 {
			return nil, 0
		}

		var children []InspectSource
		deepest := 0

		for _, rel := range inspectProvenanceRelations {
			sources, found := s.Engine.VGetIncoming(indexName, nodeID, rel)
			if !found {
				continue
			}
			for _, src := range sources {
				if visited[src] {
					continue // a graph can have cycles
				}
				if budget <= 0 {
					out.Truncated = true
					break
				}
				visited[src] = true
				budget--
				out.TotalNodes++

				child := InspectSource{ID: src, Relation: rel}
				if srcData, err := s.Engine.VGet(indexName, src); err == nil {
					child.Exists = true
					child.Content = previewContent(srcData.Metadata, inspectContentPreview)
				}

				grand, depthReached := walk(src, remaining-1)
				child.Children = grand
				if 1+depthReached > deepest {
					deepest = 1 + depthReached
				}
				children = append(children, child)
			}
		}
		return children, deepest
	}

	tree, covered := walk(id, depth)
	out.Tree = tree
	if out.Truncated {
		out.DepthCovered = covered
	} else {
		out.DepthCovered = covered
		if covered < depth {
			out.DepthCovered = covered
		}
	}

	if out.Truncated {
		out.Note = fmt.Sprintf(
			"provenance walk stopped at %d nodes; use a smaller depth for a complete view",
			inspectMaxNodes)
	}

	return out
}

// previewContent pulls a short text preview out of metadata.
func previewContent(meta map[string]any, limit int) string {
	raw, ok := meta["content"]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		s = fmt.Sprintf("%v", raw)
	}
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// ageInDays converts a unix timestamp to an age.
func ageInDays(ts int64) float64 {
	if ts <= 0 {
		return 0
	}
	d := time.Since(time.Unix(ts, 0))
	if d < 0 {
		return 0
	}
	return d.Hours() / 24
}
