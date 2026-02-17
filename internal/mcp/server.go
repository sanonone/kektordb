package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sanonone/kektordb/pkg/embeddings"
	"github.com/sanonone/kektordb/pkg/engine"
)

func NewMCPServer(eng *engine.Engine, embedder embeddings.Embedder) *mcp.Server {
	service := NewService(eng, embedder)

	// Create Server instance
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "KektorDB Memory",
		Version: "0.4.7",
	}, nil) // Options can be nil for default

	// Register Tools using the Generic AddTool which inspects structs!

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_memory",
		Description: "Save text/facts into long-term memory. Can be linked to existing entities.",
	}, service.SaveMemory)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_entity",
		Description: "Create a conceptual entity (node) without text content, to organize memories (e.g. 'Project X').",
	}, service.CreateEntity)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "connect_entities",
		Description: "Create a relationship link between two memory items/entities.",
	}, service.Connect)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "recall_memory",
		Description: "Search for memories semantically by query.",
	}, service.Recall)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "scoped_recall",
		Description: "Search memories semantically BUT restricted to a specific graph context (e.g. 'search bugs in Project X').",
	}, service.ScopedRecall)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "explore_connections",
		Description: "Explore the graph neighborhood of a specific node to understand context.",
	}, service.Traverse)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "find_connection",
		Description: "Discover how two concepts or memories are connected in the graph (Pathfinding).",
	}, service.FindConnection)

	return s
}
