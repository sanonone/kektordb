package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sanonone/kektordb/pkg/archive"
)

// handleGraphExport dumps every edge of an index as plain records.
//
// The graph is half the value of a memory store: exporting memories without
// their relations returns a set of disconnected notes, and multi-hop recall stops
// working on the imported copy. The existing graph endpoints answer per-node
// questions, which would mean one request per memory.
func (s *Server) handleGraphExport(w http.ResponseWriter, r *http.Request) {
	indexName := r.PathValue("name")
	if indexName == "" {
		s.writeHTTPError(w, http.StatusBadRequest, fmt.Errorf("index name required"))
		return
	}
	if _, ok := s.Engine.DB.GetVectorIndex(indexName); !ok {
		s.writeHTTPError(w, http.StatusNotFound, fmt.Errorf("index not found"))
		return
	}

	edges := make([]archive.EdgeRecord, 0, 256)
	prefix := indexName + "::"

	s.Engine.DB.IterateGraphEdges(func(source, target, rel string, weight float32, props []byte, cTime, dTime int64) {
		// The store holds every index in one graph; only this index's edges
		// belong in the archive. Node IDs are prefixed with the index name.
		if !hasIndexPrefix(source, prefix) && !hasIndexPrefix(target, prefix) {
			return
		}
		// A deleted edge is history, not current state.
		if dTime != 0 {
			return
		}

		rec := archive.EdgeRecord{
			Source:    trimIndexPrefix(source, prefix, indexName),
			Target:    trimIndexPrefix(target, prefix, indexName),
			Relation:  rel,
			Weight:    weight,
			CreatedAt: cTime,
		}
		if len(props) > 0 {
			var p map[string]any
			if err := json.Unmarshal(props, &p); err == nil && len(p) > 0 {
				rec.Props = p
			}
		}
		edges = append(edges, rec)
	})

	s.writeHTTPResponse(w, http.StatusOK, map[string]any{
		"index": indexName,
		"edges": archive.SortedEdges(edges),
		"count": len(edges),
	})
}

// hasIndexPrefix reports whether an internal graph ID belongs to the index.
func hasIndexPrefix(id, prefix string) bool {
	return len(id) >= len(prefix) && id[:len(prefix)] == prefix
}

// trimIndexPrefix removes the "index::" prefix that the graph store uses to keep
// namespaces apart, so the archive is written in the caller's terms.
func trimIndexPrefix(id, prefix, indexName string) string {
	if hasIndexPrefix(id, prefix) {
		return id[len(prefix):]
	}
	return id
}
