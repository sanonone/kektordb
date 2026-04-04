# KektorDB Go Client

Official Go client for [KektorDB](https://github.com/sanonone/kektordb) — The Local Memory Layer for AI Agents.

## Installation

```bash
go get github.com/sanonone/kektordb/pkg/client
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    
    "github.com/sanonone/kektordb/pkg/client"
)

func main() {
    // Initialize client
    c := client.New("localhost", 9091, "")
    
    // Create an index
    err := c.VCreateFull("memories", "cosine", "float32", 16, 200, nil, nil, nil)
    if err != nil {
        log.Fatal(err)
    }
    
    // Add a memory
    err = c.VAdd("memories", "mem_1", []float32{0.1, 0.2, 0.3}, map[string]interface{}{
        "content": "Go is great for systems programming",
        "tags": []string{"golang"},
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Search
    results, err := c.VSearch(client.SearchParams{
        IndexName:   "memories",
        QueryVector: []float32{0.1, 0.2, 0.3},
        K:           5,
    })
    if err != nil {
        log.Fatal(err)
    }
    
    for _, r := range results {
        fmt.Printf("Found: %s (score: %.3f)\n", r.ID, r.Score)
    }
}
```

## API Reference

### Client Initialization

```go
// Basic client
client := client.New("localhost", 9091, "")

// With API key
client := client.New("localhost", 9091, "your-api-key")

// Custom HTTP timeout
client.SetTimeout(30 * time.Second)
```

### Key-Value Store

```go
// Set key
err := client.Set("key", []byte("value"))

// Get key
value, err := client.Get("key")

// Delete key
err := client.Delete("key")
```

### Index Management

```go
// Create index
err := client.VCreateFull("idx", "cosine", "float32", 16, 200, nil, nil, nil)

// List indexes
indexes, err := client.ListIndexes()

// Get index info
info, err := client.GetIndexInfo("idx")

// Delete index
err := client.DeleteIndex("idx")

// Compress to int8
task, err := client.VCompress("idx", "int8")
err = task.Wait(10*time.Second, 5*time.Minute)
```

### Vectors

```go
// Add single vector
err := client.VAdd("idx", "id", []float32{0.1, 0.2}, map[string]interface{}{
    "content": "hello",
})

// Add zero-vector entity (graph node)
err := client.VAdd("idx", "entity_1", nil, map[string]interface{}{
    "name": "Python",
    "type": "entity",
})

// Batch add
vectors := []client.VectorAddObject{
    {Id: "a", Vector: []float32{0.1}, Metadata: map[string]interface{}{}},
}
resp, err := client.VAddBatch("idx", vectors)

// Delete
err := client.VDelete("idx", "id")

// Get single
vec, err := client.VGet("idx", "id")

// Get multiple
vecs, err := client.VGetMany("idx", []string{"a", "b"})

// Reinforce (boost relevance)
err := client.VReinforce("idx", []string{"id1", "id2"})

// Export
export, err := client.VExport("idx", 1000, 0)
```

### Search

```go
// Pure vector search
results, err := client.VSearch(client.SearchParams{
    IndexName:   "idx",
    QueryVector: []float32{0.1, 0.2},
    K:           10,
})

// With metadata filter
results, err := client.VSearch(client.SearchParams{
    IndexName:   "idx",
    QueryVector: []float32{0.1, 0.2},
    K:           10,
    Filter:      "type='article' AND year>2023",
})

// With scores
results, err := client.VSearchWithScores("idx", []float32{0.1, 0.2}, 5)
```

### Graph Operations

```go
// Link entities
err := client.VLink(client.LinkParams{
    IndexName:           "idx",
    SourceID:            "A",
    TargetID:            "B",
    RelationType:        "mentions",
    InverseRelationType: "mentioned_in",
})

// Unlink
err := client.VUnlink("idx", "A", "B", "mentions", "")

// Get links
links, err := client.VGetLinks("idx", "A", "mentions")

// Get incoming
incoming, err := client.VGetIncoming("idx", "B", "mentions")

// Get connections (hydrated)
connections, err := client.VGetConnections("idx", "A", "mentions")

// Get all relations
allRels, err := client.VGetAllRelations("idx", "A")

// Traverse graph
node, err := client.VTraverse("idx", "root", []string{"mentions", "parent"})

// Extract subgraph
subgraph, err := client.VExtractSubgraph("idx", "root", []string{"mentions"}, 2, 0, nil, 0)

// Find path
path, err := client.FindPath("idx", "A", "B", []string{"mentions", "related_to"}, 0)
```

### Cognitive Engine

```go
// Get reflections
reflections, err := client.GetReflections("idx", "")
unresolved, err := client.GetReflections("idx", "unresolved")

// Resolve reflection
err := client.ResolveReflection("idx", "reflection_123", "The newer memory is correct", "")

// Trigger manual think cycle
err := client.Think("idx")
```

### Auto-Links

```go
// Set auto-link rules
rules := []client.AutoLinkRule{
    {MetadataField: "project_id", RelationType: "belongs_to"},
}
err := client.SetAutoLinks("idx", rules)

// Get rules
rules, err := client.GetAutoLinks("idx")
```

### Auth (RBAC)

```go
// Create API key
key, err := client.CreateApiKey("write", "tenant_A")

// List keys
keys, err := client.ListApiKeys()

// Revoke key
err := client.RevokeApiKey("key_id")
```

### System

```go
// Save snapshot
err := client.Save()

// AOF rewrite
task, err := client.AOFRewrite()
err = task.Wait(10*time.Second, 5*time.Minute)

// Get task status
status, err := client.GetTaskStatus("task_id")
```

## 🧠 Session Management & Cognitive Package

For high-level abstractions, use the `cognitive` subpackage:

```go
import (
    "github.com/sanonone/kektordb/pkg/client"
    "github.com/sanonone/kektordb/pkg/client/cognitive"
)

c := client.New("localhost", 9091, "")

// Create session manager
manager := cognitive.NewSessionManager(c)

// Create a session
session, err := manager.CreateSession(cognitive.SessionOptions{
    UserID:   "user_123",
    Metadata: map[string]interface{}{"context": "support"},
})
if err != nil {
    log.Fatal(err)
}

// Add messages
session.AddMessage("user", "How do I reset my password?")
session.AddMessage("assistant", "You can reset your password by...")

// Get conversation history
history := session.GetContext()

// Clean up
err = manager.EndSession(session.ID)
```

### Using withSession Pattern

```go
// Automatic session lifecycle
err := cognitive.WithSession(manager, cognitive.SessionOptions{
    UserID: "user_123",
}, func(session *cognitive.ManagedSession) error {
    // Session automatically started
    session.AddMessage("user", "Hello!")
    
    // Access session data
    data, err := session.Context().Value("key")
    
    return err
})  // Session automatically ended
```

### Context-Aware Retrieval

```go
// Create context assembler
assembler := cognitive.NewContextAssembler(c, nil)

// Retrieve with graph expansion
resp, err := assembler.Retrieve("my_pipeline", "query", 5)
fmt.Printf("Context: %s\n", resp.ContextText)
fmt.Printf("Chunks: %d\n", resp.ChunksUsed)

// Retrieve within session context
resp, err = assembler.RetrieveWithContext(session, "my_pipeline", "query", 5)
```

### Multi-Agent Coordination

```go
// Create coordinator
coordinator := cognitive.NewMultiAgentCoordinator(c)
defer coordinator.Cleanup()

// Register agents
planner, err := coordinator.RegisterAgent(cognitive.AgentConfig{
    ID:   "planner",
    Name: "Task Planner",
    Role: cognitive.AgentRolePlanner,
})

retriever, err := coordinator.RegisterAgent(cognitive.AgentConfig{
    ID:           "retriever",
    Name:         "Data Retriever",
    Role:         cognitive.AgentRoleRetriever,
    PipelineName: "my_pipeline",
})

// Execute pipeline
results, err := coordinator.ExecutePipeline("What are the key insights?")
for _, r := range results {
    fmt.Printf("Agent %s: success=%v\n", r.AgentID, r.Success)
}

// Execute in parallel
results, err = coordinator.ExecuteParallel(
    []string{"agent1", "agent2"},
    "parallel query",
)
```

## 📚 Adaptive Retrieval with Source Attribution

```go
// Adaptive retrieve with provenance
resp, err := client.AdaptiveRetrieve(client.AdaptiveRetrieveRequest{
    PipelineName:      "my_pipeline",
    Query:             "What are the key features?",
    K:                 5,
    Strategy:          "graph",
    ExpansionDepth:    2,
    IncludeProvenance: true,
})

fmt.Printf("Context: %s\n", resp.ContextText)
fmt.Printf("Tokens: %d\n", resp.TotalTokens)

// Access source attribution
for _, src := range resp.Sources {
    fmt.Printf("Source: %s (%.2f)\n", src.Filename, src.Relevance)
    fmt.Printf("  Path: %s\n", src.GraphPath.Formatted)
    fmt.Printf("  Content: %s...\n", src.Content[:100])
}

// Format sources for display
formatted := cognitive.FormatSources(resp.Sources)
fmt.Println(formatted)

// Filter sources
filtered := cognitive.FilterSources(resp.Sources, cognitive.SourceFilter{
    MinRelevance: 0.8,
    VerifiedOnly: true,
})

// Group by document
grouped := cognitive.GroupSourcesByDocument(resp.Sources)
// grouped["doc_123"] = []SourceAttribution{...}
```

## 👤 User Profiles

```go
// List user profiles
profiles, err := client.ListUserProfiles()
for _, p := range profiles {
    fmt.Printf("User: %s (confidence: %.2f)\n", p.UserID, p.Confidence)
}

// Get specific profile
profile, err := client.GetUserProfile("user_123")
fmt.Printf("Communication style: %s\n", profile.CommunicationStyle)
fmt.Printf("Expertise: %v\n", profile.ExpertiseAreas)
fmt.Printf("Language: %s\n", profile.Language)
```

## Error Handling

```go
result, err := client.VSearch(params)
if err != nil {
    if apiErr, ok := err.(*client.APIError); ok {
        log.Printf("API error (status %d): %s", apiErr.StatusCode, apiErr.Message)
    } else {
        log.Printf("Connection error: %v", err)
    }
}
```

## License

Apache 2.0
