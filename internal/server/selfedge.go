package server

// selfEdgeRelations are the relation types a node may hold towards itself.
//
// A self-edge is almost always a mistake for a caller: `doc-1 -> doc-1` is a
// cycle that says nothing. But the cognitive engine uses them deliberately, as a
// per-node marker meaning "this detector has already looked at this node":
// detectSentimentShifts, detectCentralityShifts, detectForgettingPatterns,
// detectEntityLinks and detectKnowledgeEvolution each write one, and the next
// cycle reads it to skip the node.
//
// The public API had no way to express that distinction, so it refused every
// self-edge — which made an exported store impossible to reimport, because the
// markers are in every archive of a store that ran maintenance.
//
// The allowlist is explicit rather than "allow relations ending in _analyzed":
// a caller-invented name must not silently acquire a privilege.
var selfEdgeRelations = map[string]bool{
	"decay_analyzed":      true, // detectForgettingPatterns
	"sentiment_analyzed":  true, // detectSentimentShifts
	"centrality_analyzed": true, // detectCentralityShifts
	"entities_analyzed":   true, // detectEntityLinks
	"evolution_analyzed":  true, // detectKnowledgeEvolution
}
