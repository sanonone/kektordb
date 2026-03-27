# KektorDB TypeScript Client

Official TypeScript/JavaScript client for [KektorDB](https://github.com/sanonone/kektordb) — The Local Memory Layer for AI Agents.

## Install

```bash
npm install kektordb-client
```

## Quick Start

```typescript
import { KektorDBClient } from "kektordb-client";

const client = new KektorDBClient({ host: "localhost", port: 9091 });

// Create an index
await client.vcreate({
  indexName: "memories",
  metric: "cosine",
  precision: "float32",
});

// Add a memory
await client.vadd("memories", "mem_1", [0.1, 0.2, 0.3], {
  content: "TypeScript is great for type-safe development",
  tags: ["dev_tools"],
});

// Search
const results = await client.vsearch({
  indexName: "memories",
  queryVector: [0.1, 0.2, 0.3],
  k: 5,
});
console.log(results);
```

## API

### Key-Value Store

```typescript
await client.set("key", "value");
const val = await client.get("key");
await client.delete("key");
```

### Index Management

```typescript
await client.vcreate({ indexName: "idx", metric: "cosine" });
const indexes = await client.listIndexes();
const info = await client.getIndexInfo("idx");
await client.deleteIndex("idx");
const task = await client.vcompress("idx", "int8");
await task.wait();
```

### Vectors

```typescript
await client.vadd("idx", "id", [0.1, 0.2], { content: "hello" });
await client.vadd("idx", "entity_1", null, { name: "Python", type: "entity" }); // zero-vector entity
await client.vaddBatch("idx", [{ id: "a", vector: [0.1], metadata: {} }]);
await client.vdelete("idx", "id");
const data = await client.vget("idx", "id");
const many = await client.vgetMany("idx", ["a", "b"]);
await client.vreinforce("idx", ["id"]);
const exportData = await client.vexport("idx", 100, 0);
```

### Search

```typescript
// Pure vector search
const results = await client.vsearch({
  indexName: "idx",
  queryVector: [0.1, 0.2],
  k: 10,
});

// Hybrid search (vector + text)
const hybrid = await client.vsearch({
  indexName: "idx",
  queryVector: [0.1, 0.2],
  k: 10,
  textQuery: "machine learning",
  alpha: 0.5,
});

// Filtered search
const filtered = await client.vsearch({
  indexName: "idx",
  queryVector: [0.1, 0.2],
  k: 10,
  filter: "type='article' AND year>2023",
});

// Graph-scoped search
const scoped = await client.vsearch({
  indexName: "idx",
  queryVector: [0.1, 0.2],
  k: 10,
  graphFilter: { rootId: "entity_python", relations: ["mentions"], depth: 2 },
});

// Search with scores
const scored = await client.vsearchWithScores("idx", [0.1, 0.2], 5);
```

### Graph

```typescript
await client.vlink({
  indexName: "idx",
  sourceId: "A",
  targetId: "B",
  relationType: "mentions",
  inverseRelationType: "mentioned_in",
});
await client.vunlink("idx", "A", "B", "mentions");
const links = await client.vgetLinks("idx", "A", "mentions");
const incoming = await client.getIncoming("idx", "B", "mentions");
const connections = await client.vgetConnections("idx", "A", "mentions");
const allRels = await client.getAllRelations("idx", "A");
const allIncoming = await client.getAllIncoming("idx", "A");
```

### Graph Traversal & Pathfinding

```typescript
const subgraph = await client.extractSubgraph("idx", "root", ["mentions", "related_to"], 2);
const path = await client.findPath("idx", "A", "B");
const traversed = await client.traverse("idx", "root", ["mentions"]);
const edges = await client.getEdges("idx", "A", "mentions", 0); // time-travel with atTime
```

### Cognitive Engine

```typescript
// Check what the Gardener found
const reflections = await client.getReflections("idx");
const unresolved = await client.getReflections("idx", "unresolved");

// Resolve a contradiction
await client.resolveReflection("idx", "reflection_123", "The newer memory is correct");

// Trigger manual cycle
await client.think("idx");
```

### Metadata

```typescript
await client.setNodeProperties("idx", "node_1", { content: "updated" });
const props = await client.getNodeProperties("idx", "node_1");
const nodes = await client.searchNodes("idx", "type='entity'", 20);
```

### Auto-Links

```typescript
await client.setAutoLinks("idx", [
  { metadata_field: "project_id", relation_type: "belongs_to" },
]);
const rules = await client.getAutoLinks("idx");
```

### Auth (RBAC)

```typescript
const { key } = await client.createApiKey("write", "tenant_A");
const keys = await client.listApiKeys();
await client.revokeApiKey("key_id");
```

### System

```typescript
await client.save();
const task = await client.aofRewrite();
await task.wait();
```

## Error Handling

```typescript
import { APIError, ConnectionError, TimeoutError } from "kektordb-client";

try {
  await client.vsearch({ indexName: "nonexistent", queryVector: [0.1], k: 5 });
} catch (e) {
  if (e instanceof APIError) {
    console.error(`API error ${e.statusCode}: ${e.message}`);
  } else if (e instanceof ConnectionError) {
    console.error("Is KektorDB running?");
  }
}
```

## License

Apache 2.0
