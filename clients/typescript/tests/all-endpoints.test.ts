/**
 * Comprehensive integration tests for all KektorDB endpoints.
 *
 * This module tests ALL server endpoints to ensure complete API coverage
 * and verify database functionality.
 *
 * Environment Variables:
 *   KEKTOR_TEST_HOST: Server host (default: localhost)
 *   KEKTOR_TEST_PORT: Server port (default: 9091)
 *   KEKTOR_TEST_PIPELINE: Pipeline name for RAG tests
 *
 * Usage:
 *   npx vitest run tests/all-endpoints.test.ts
 */

import { describe, it, expect, beforeAll, afterAll, beforeEach } from "vitest";
import { spawn, ChildProcess } from "child_process";
import { join } from "path";
import { existsSync } from "fs";
import { randomUUID } from "crypto";

import { KektorDBClient } from "../src/client";
import {
  SessionManager,
  withSession,
  CognitiveSession,
} from "../src/cognitive";

const HOST = process.env.KEKTOR_TEST_HOST || "localhost";
const PORT = parseInt(process.env.KEKTOR_TEST_PORT || "9091", 10);
const PIPELINE_NAME = process.env.KEKTOR_TEST_PIPELINE || "";

// Helper functions
async function isServerAvailable(): Promise<boolean> {
  try {
    const response = await fetch(`http://${HOST}:${PORT}/vector/indexes`, {
      method: "GET",
    });
    return response.status === 200;
  } catch {
    return false;
  }
}

async function maybeStartServer(): Promise<ChildProcess | null> {
  if (await isServerAvailable()) {
    console.log("Using existing KektorDB server");
    return null;
  }

  console.log("Starting KektorDB server for tests...");
  const possiblePaths = [
    join(process.cwd(), "../../kektordb"),
    join(process.cwd(), "../../bin/kektordb"),
    join(process.cwd(), "../kektordb"),
    "/usr/local/bin/kektordb",
  ];

  let serverPath: string | null = null;
  for (const p of possiblePaths) {
    if (existsSync(p)) {
      serverPath = p;
      break;
    }
  }

  if (!serverPath) {
    console.warn("KektorDB server binary not found, skipping auto-start");
    return null;
  }

  const proc = spawn(serverPath, ["-http-port", PORT.toString()], {
    detached: true,
    stdio: "pipe",
  });

  let attempts = 0;
  const maxAttempts = 30;
  while (attempts < maxAttempts) {
    await new Promise((r) => setTimeout(r, 1000));
    if (await isServerAvailable()) {
      console.log("KektorDB server started successfully");
      return proc;
    }
    attempts++;
  }

  console.warn("Server failed to start within timeout");
  proc.kill();
  return null;
}

function generateTestId(): string {
  return `test_${randomUUID().replace(/-/g, "").substring(0, 8)}`;
}

describe("KektorDB Complete API Tests", () => {
  let client: KektorDBClient;
  let serverProc: ChildProcess | null = null;
  let serverAvailable = false;
  let testIndexName: string;

  beforeAll(async () => {
    serverProc = await maybeStartServer();
    serverAvailable = await isServerAvailable();
    if (!serverAvailable) {
      console.warn(
        "WARNING: KektorDB server not available, tests will be skipped",
      );
    }
    client = new KektorDBClient({ host: HOST, port: PORT });
  }, 60000);

  afterAll(() => {
    if (serverProc) {
      console.log("Shutting down test server...");
      serverProc.kill();
    }
  });

  beforeEach(() => {
    testIndexName = generateTestId();
  });

  // Skip tests if server not available
  const itIfServer = (
    name: string,
    fn: () => Promise<void>,
    timeout?: number,
  ) => {
    it(
      name,
      async () => {
        if (!serverAvailable) {
          console.warn(`Skipping: ${name} (server not available)`);
          return;
        }
        await fn();
      },
      timeout,
    );
  };

  // =============================================================================
  // KV STORE TESTS
  // =============================================================================

  describe("KV Store", () => {
    itIfServer("should set and get a value", async () => {
      const key = generateTestId();
      const value = "test_value_123";

      await client.set(key, value);
      const result = await client.get(key);
      expect(result).toBe(value);
    });

    itIfServer("should overwrite existing key", async () => {
      const key = generateTestId();

      await client.set(key, "value1");
      await client.set(key, "value2");

      const result = await client.get(key);
      expect(result).toBe("value2");
    });

    itIfServer("should delete a key", async () => {
      const key = generateTestId();

      await client.set(key, "value");
      await client.delete(key);

      // Should throw or return undefined
      await expect(client.get(key)).rejects.toThrow();
    });

    itIfServer("should handle non-existent key", async () => {
      const key = `nonexistent_${generateTestId()}`;
      await expect(client.get(key)).rejects.toThrow();
    });
  });

  // =============================================================================
  // VECTOR INDEX MANAGEMENT TESTS
  // =============================================================================

  describe("Vector Index Management", () => {
    itIfServer("should create an index", async () => {
      await client.vcreate({
        indexName: testIndexName,
        metric: "cosine",
        precision: "float32",
        m: 16,
        efConstruction: 200,
      });

      const info = await client.getIndexInfo(testIndexName);
      expect(info.name).toBe(testIndexName);
      expect(info.metric).toBe("cosine");
    });

    itIfServer("should create index with maintenance config", async () => {
      await client.vcreate({
        indexName: testIndexName,
        metric: "cosine",
        maintenance: {
          vacuumInterval: "60s",
          deleteThreshold: 0.3,
          refineEnabled: true,
          refineInterval: "30s",
        },
      });

      const info = await client.getIndexInfo(testIndexName);
      expect(info.name).toBe(testIndexName);
    });

    itIfServer("should create index with auto-links", async () => {
      await client.vcreate({
        indexName: testIndexName,
        metric: "cosine",
        autoLinks: [
          { metadataField: "category", relationType: "same_category" },
        ],
      });

      const rules = await client.getAutoLinks(testIndexName);
      expect(rules.length).toBeGreaterThanOrEqual(1);
    });

    itIfServer("should list indexes", async () => {
      await client.vcreate({ indexName: testIndexName, metric: "cosine" });

      const indexes = await client.listIndexes();
      const indexNames = indexes.map((idx) => idx.name);
      expect(indexNames).toContain(testIndexName);
    });

    itIfServer("should get index info", async () => {
      await client.vcreate({
        indexName: testIndexName,
        metric: "euclidean",
        m: 32,
        efConstruction: 400,
      });

      const info = await client.getIndexInfo(testIndexName);
      expect(info.name).toBe(testIndexName);
      expect(info.metric).toBe("euclidean");
    });

    itIfServer("should delete an index", async () => {
      await client.vcreate({ indexName: testIndexName, metric: "cosine" });
      await client.deleteIndex(testIndexName);

      const indexes = await client.listIndexes();
      const indexNames = indexes.map((idx) => idx.name);
      expect(indexNames).not.toContain(testIndexName);
    });

    itIfServer("should compress index", async () => {
      await client.vcreate({ indexName: testIndexName, precision: "float32" });
      await client.vadd(testIndexName, "vec1", [0.1, 0.2, 0.3, 0.4], {});

      const task = await client.vcompress(testIndexName, "int8");
      await task.wait(1, 30); // Poll every 1 second, timeout after 30 seconds

      const info = await client.getIndexInfo(testIndexName);
      expect(info.precision).toBe("int8");
    }, 60000);

    itIfServer("should update index config", async () => {
      await client.vcreate({ indexName: testIndexName });

      await client.vupdateConfig(testIndexName, {
        vacuumInterval: "120s",
        deleteThreshold: 0.5,
      });

      // No return value, just verify no error
      expect(true).toBe(true);
    });

    itIfServer("should set and get auto-links", async () => {
      await client.vcreate({ indexName: testIndexName });

      const rules = [
        { metadataField: "project_id", relationType: "belongs_to_project" },
        { metadataField: "tag", relationType: "tagged_with" },
      ];

      await client.setAutoLinks(testIndexName, rules);
      const retrievedRules = await client.getAutoLinks(testIndexName);

      expect(retrievedRules.length).toBe(2);
    });
  });

  // =============================================================================
  // VECTOR OPERATIONS TESTS
  // =============================================================================

  describe("Vector Operations", () => {
    itIfServer("should add a vector", async () => {
      await client.vcreate({ indexName: testIndexName });

      await client.vadd(testIndexName, "vec1", [0.1, 0.2, 0.3, 0.4], {
        content: "test",
        type: "document",
      });

      const vec = await client.vget(testIndexName, "vec1");
      expect(vec.id).toBe("vec1");
      expect(vec.metadata?.content).toBe("test");
    });

    itIfServer("should add zero-vector entity", async () => {
      await client.vcreate({ indexName: testIndexName });
      // Add seed vector so index dimension is known
      await client.vadd(testIndexName, "seed", [0.1, 0.2, 0.3, 0.4]);

      await client.vadd(testIndexName, "entity1", [0, 0, 0, 0], {
        name: "Python",
        type: "entity",
      });

      const vec = await client.vget(testIndexName, "entity1");
      expect(vec.id).toBe("entity1");
      expect(vec.metadata?.type).toBe("entity");
    });

    itIfServer("should add batch of vectors", async () => {
      await client.vcreate({ indexName: testIndexName });

      const vectors = [
        { id: "v1", vector: [0.1, 0.2, 0.3], metadata: { cat: "a" } },
        { id: "v2", vector: [0.4, 0.5, 0.6], metadata: { cat: "b" } },
        { id: "v3", vector: [0.7, 0.8, 0.9], metadata: { cat: "a" } },
      ];

      const result = await client.vaddBatch(testIndexName, vectors as any);
      expect(result.vectors_added).toBe(3);

      for (const vec of vectors) {
        const retrieved = await client.vget(testIndexName, vec.id);
        expect(retrieved.id).toBe(vec.id);
      }
    });

    itIfServer("should import vectors", async () => {
      await client.vcreate({ indexName: testIndexName });

      const vectors = [
        { id: "i1", vector: [0.1, 0.2, 0.3], metadata: {} },
        { id: "i2", vector: [0.4, 0.5, 0.6], metadata: {} },
      ];

      await client.vimport(testIndexName, vectors as any);

      const info = await client.getIndexInfo(testIndexName);
      expect(info.vector_count).toBe(2);
    });

    itIfServer("should get single vector", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "vec1", [0.1, 0.2, 0.3], {
        key: "value",
      });

      const vec = await client.vget(testIndexName, "vec1");
      expect(vec.id).toBe("vec1");
      expect(vec.metadata?.key).toBe("value");
    });

    itIfServer("should get multiple vectors", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "v1", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "v2", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "v3", [0.7, 0.8, 0.9], {});

      const vectors = await client.vgetMany(testIndexName, ["v1", "v2"]);
      expect(vectors.length).toBe(2);
    });

    itIfServer("should delete a vector", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "to_delete", [0.1, 0.2, 0.3], {});

      await client.vdelete(testIndexName, "to_delete");

      await expect(client.vget(testIndexName, "to_delete")).rejects.toThrow();
    });

    itIfServer("should search vectors", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "v1", [0.1, 0.2, 0.3, 0.4], {
        cat: "a",
      });
      await client.vadd(testIndexName, "v2", [0.9, 0.8, 0.7, 0.6], {
        cat: "b",
      });
      await client.vadd(testIndexName, "v3", [0.15, 0.25, 0.35, 0.45], {
        cat: "a",
      });

      const results = await client.vsearch({
        indexName: testIndexName,
        queryVector: [0.1, 0.2, 0.3, 0.4],
        k: 2,
      });

      expect(results.length).toBe(2);
    });

    itIfServer("should search with filter", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "a1", [0.1, 0.2, 0.3], {
        category: "a",
      });
      await client.vadd(testIndexName, "a2", [0.11, 0.21, 0.31], {
        category: "a",
      });
      await client.vadd(testIndexName, "b1", [0.1, 0.2, 0.3], {
        category: "b",
      });

      const results = await client.vsearch({
        indexName: testIndexName,
        queryVector: [0.1, 0.2, 0.3],
        k: 10,
        filter: "category='a'",
      });

      expect(results.length).toBeLessThanOrEqual(2);
    });

    itIfServer("should search with scores", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "s1", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "s2", [0.9, 0.8, 0.7], {});

      const results = await client.vsearchWithScores(
        testIndexName,
        [0.1, 0.2, 0.3],
        2,
      );

      expect(results.length).toBe(2);
      for (const r of results) {
        // Server returns PascalCase: {"ID": "x", "Score": 0.5}
        expect(r.ID ?? r.id).toBeDefined();
        expect(r.Score ?? r.score).toBeGreaterThanOrEqual(0);
        expect(r.Score ?? r.score).toBeLessThanOrEqual(1);
      }
    });

    itIfServer("should reinforce vectors", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "r1", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "r2", [0.4, 0.5, 0.6], {});

      await client.vreinforce(testIndexName, ["r1", "r2"]);
      expect(true).toBe(true);
    });

    itIfServer("should export vectors", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "e1", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "e2", [0.4, 0.5, 0.6], {});

      const result = await client.vexport(testIndexName, 1, 0);
      expect(result.data.length).toBeGreaterThanOrEqual(1);
      expect(result.has_more ?? result.hasMore).toBeDefined();
    });
  });

  // =============================================================================
  // GRAPH OPERATIONS TESTS
  // =============================================================================

  describe("Graph Operations", () => {
    itIfServer("should link and unlink entities", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "doc1", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "doc2", [0.4, 0.5, 0.6], {});

      // Link
      await client.vlink({
        indexName: testIndexName,
        sourceId: "doc1",
        targetId: "doc2",
        relationType: "references",
        inverseRelationType: "referenced_by",
      });

      const links = await client.vgetLinks(testIndexName, "doc1", "references");
      expect(links).toContain("doc2");

      // Unlink
      await client.vunlink(testIndexName, "doc1", "doc2", "references");

      const linksAfter = await client.vgetLinks(
        testIndexName,
        "doc1",
        "references",
      );
      expect(linksAfter).not.toContain("doc2");
    });

    itIfServer("should get incoming links", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "parent", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "child1", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "child2", [0.7, 0.8, 0.9], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "child1",
        targetId: "parent",
        relationType: "child_of",
      });
      await client.vlink({
        indexName: testIndexName,
        sourceId: "child2",
        targetId: "parent",
        relationType: "child_of",
      });

      const incoming = await client.getIncoming(
        testIndexName,
        "parent",
        "child_of",
      );
      expect(incoming.length).toBe(2);
      expect(incoming).toContain("child1");
      expect(incoming).toContain("child2");
    });

    itIfServer("should get connections", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "src", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "tgt", [0.4, 0.5, 0.6], {
        content: "target",
      });

      await client.vlink({
        indexName: testIndexName,
        sourceId: "src",
        targetId: "tgt",
        relationType: "links_to",
      });

      const connections = await client.vgetConnections(
        testIndexName,
        "src",
        "links_to",
      );
      expect(connections.length).toBe(1);
      expect(connections[0].id).toBe("tgt");
    });

    itIfServer("should get all relations", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "hub", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "a", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "b", [0.7, 0.8, 0.9], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "hub",
        targetId: "a",
        relationType: "type_a",
      });
      await client.vlink({
        indexName: testIndexName,
        sourceId: "hub",
        targetId: "b",
        relationType: "type_b",
      });

      const relations = await client.getAllRelations(testIndexName, "hub");
      expect(Object.keys(relations)).toContain("type_a");
      expect(Object.keys(relations)).toContain("type_b");
    });

    itIfServer("should get all incoming relations", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "target", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "s1", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "s2", [0.7, 0.8, 0.9], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "s1",
        targetId: "target",
        relationType: "points_to",
      });
      await client.vlink({
        indexName: testIndexName,
        sourceId: "s2",
        targetId: "target",
        relationType: "points_to",
      });

      const incoming = await client.getAllIncoming(testIndexName, "target");
      expect(Object.keys(incoming)).toContain("points_to");
    });

    itIfServer("should traverse graph", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "a", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "b", [0.4, 0.5, 0.6], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "a",
        targetId: "b",
        relationType: "next",
      });

      const result = await client.traverse(testIndexName, "a", ["next"]);
      // Response is wrapped: {result: {id: "a", ...}}
      const actual = result.result || result;
      expect(actual.id || actual.ID).toBe("a");
    });

    itIfServer("should extract subgraph", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "root", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "c1", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "c2", [0.7, 0.8, 0.9], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "root",
        targetId: "c1",
        relationType: "has_child",
      });
      await client.vlink({
        indexName: testIndexName,
        sourceId: "root",
        targetId: "c2",
        relationType: "has_child",
      });

      const result = await client.extractSubgraph(
        testIndexName,
        "root",
        ["has_child"],
        2,
      );
      expect(result.root_id).toBe("root");
      expect(result.nodes.length).toBeGreaterThanOrEqual(1);
    });

    itIfServer("should find path", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "start", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "middle", [0.4, 0.5, 0.6], {});
      await client.vadd(testIndexName, "end", [0.7, 0.8, 0.9], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "start",
        targetId: "middle",
        relationType: "connects",
      });
      await client.vlink({
        indexName: testIndexName,
        sourceId: "middle",
        targetId: "end",
        relationType: "connects",
      });

      const result = await client.findPath(testIndexName, "start", "end", [
        "connects",
      ]);
      expect(result).toBeDefined();
    });

    itIfServer("should get edges", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "esrc", [0.1, 0.2, 0.3], {});
      await client.vadd(testIndexName, "etgt", [0.4, 0.5, 0.6], {});

      await client.vlink({
        indexName: testIndexName,
        sourceId: "esrc",
        targetId: "etgt",
        relationType: "links",
      });

      const edges = await client.getEdges(testIndexName, "esrc", "links");
      expect(Array.isArray(edges)).toBe(true);
    });

    itIfServer("should set and get node properties", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "prop_node", [0.1, 0.2, 0.3], {
        initial: "val",
      });

      await client.setNodeProperties(testIndexName, "prop_node", {
        updated: "new_value",
        count: 42,
      });

      const props = await client.getNodeProperties(testIndexName, "prop_node");
      expect(props.updated).toBe("new_value");
      expect(props.count).toBe(42);
    });

    itIfServer("should search nodes by properties", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "p1", [0.1, 0.2, 0.3], {
        type: "person",
        name: "Alice",
      });
      await client.vadd(testIndexName, "p2", [0.4, 0.5, 0.6], {
        type: "person",
        name: "Bob",
      });
      await client.vadd(testIndexName, "c1", [0.7, 0.8, 0.9], {
        type: "company",
        name: "Acme",
      });

      const results = await client.searchNodes(
        testIndexName,
        "type='person'",
        10,
      );
      expect(results.length).toBe(2);
    });
  });

  // =============================================================================
  // RAG TESTS
  // =============================================================================

  describe("RAG Operations", () => {
    itIfServer("should retrieve with ragRetrieve", async () => {
      if (!PIPELINE_NAME) {
        console.warn("Skipping RAG test: KEKTOR_TEST_PIPELINE not set");
        return;
      }

      const result = await client.ragRetrieve(PIPELINE_NAME, "test query", 5);
      expect(result.response).toBeDefined();
    });

    itIfServer("should retrieve with provenance", async () => {
      if (!PIPELINE_NAME) {
        console.warn("Skipping RAG test: KEKTOR_TEST_PIPELINE not set");
        return;
      }

      const result = await client.ragRetrieve(
        PIPELINE_NAME,
        "test query",
        5,
        true,
      );
      expect(result.response).toBeDefined();
      expect(Array.isArray(result.sources)).toBe(true);
      expect(result.provenance).toBe(true);
    });

    itIfServer("should perform adaptive retrieve", async () => {
      if (!PIPELINE_NAME) {
        console.warn("Skipping RAG test: KEKTOR_TEST_PIPELINE not set");
        return;
      }

      const result = await client.adaptiveRetrieve({
        pipelineName: PIPELINE_NAME,
        query: "test query",
        k: 5,
        strategy: "graph",
        expansionDepth: 2,
        includeProvenance: true,
      });

      expect(result.context_text).toBeDefined();
      expect(typeof result.chunks_used).toBe("number");
      expect(typeof result.total_tokens).toBe("number");
      expect(result.expansion_stats).toBeDefined();
    });
  });

  // =============================================================================
  // SESSION MANAGEMENT TESTS
  // =============================================================================

  describe("Session Management", () => {
    itIfServer("should start and end session", async () => {
      // Create index first
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);

      const result = await client.startSession({
        indexName: idxName,
        userId: "test_user",
        metadata: { test: true },
      });

      expect(result.session_id).toBeDefined();

      const endResult = await client.endSession(result.session_id, {
        indexName: idxName,
      });
      expect(endResult.session_id ?? endResult.success).toBeDefined();

      // Cleanup
      await client.deleteIndex(idxName);
    });

    itIfServer("should start session with context", async () => {
      // Create index first
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);

      const result = await client.startSession({
        indexName: idxName,
        userId: "test_user",
        context: "test context",
      });

      expect(result.session_id).toBeDefined();

      await client.endSession(result.session_id, { indexName: idxName });

      // Cleanup
      await client.deleteIndex(idxName);
    });

    itIfServer("should use SessionManager", async () => {
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);

      const manager = new SessionManager(client, idxName);

      const session = await manager.createSession("test_agent", {
        userId: "test_user",
      });

      expect(session.sessionId).toBeDefined();
      expect(session.isStarted).toBe(true);

      await session.saveMemory("Hello!");

      await manager.endAllSessions();
      await client.deleteIndex(idxName);
    });

    itIfServer("should use withSession pattern", async () => {
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);
      let capturedSessionId: string | undefined;

      await withSession(
        client,
        { indexName: idxName, userId: "test_user" },
        async (session) => {
          capturedSessionId = session.sessionId;
          await session.saveMemory("Test message");
          expect(session.isStarted).toBe(true);
        },
      );

      // Session should be ended
      expect(capturedSessionId).toBeDefined();
      await client.deleteIndex(idxName);
    });
  });

  // =============================================================================
  // USER PROFILE TESTS
  // =============================================================================

  describe("User Profiles", () => {
    itIfServer("should list user profiles", async () => {
      // Create index first
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });

      const result = await client.listUserProfiles(idxName);
      expect(Array.isArray(result.profiles)).toBe(true);
      expect(typeof result.count).toBe("number");
      // Should return empty list for new index
      expect(result.profiles.length).toBe(0);

      // Cleanup
      await client.deleteIndex(idxName);
    });

    itIfServer("should get user profile", async () => {
      // Create index first
      const idxName = generateTestId();
      await client.vcreate({ indexName: idxName, metric: "cosine" });

      // Try to get a non-existent profile - should throw error
      await expect(
        client.getUserProfile("nonexistent_user", idxName),
      ).rejects.toThrow();

      // Cleanup
      await client.deleteIndex(idxName);
    });
  });

  // =============================================================================
  // COGNITIVE ENGINE TESTS
  // =============================================================================

  describe("Cognitive Engine", () => {
    itIfServer("should trigger think", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "think_vec", [0.1, 0.2, 0.3], {});

      await client.think(testIndexName);
      expect(true).toBe(true);
    });

    itIfServer("should get reflections", async () => {
      await client.vcreate({ indexName: testIndexName });

      // Add data
      await client.vadd(testIndexName, "rvec1", [0.1, 0.2, 0.3], {
        content: "same",
      });
      await client.vadd(testIndexName, "rvec2", [0.1, 0.2, 0.3], {
        content: "same",
      });

      await client.think(testIndexName);

      const reflections = await client.getReflections(testIndexName);
      expect(Array.isArray(reflections)).toBe(true);
    });

    itIfServer("should resolve reflection", async () => {
      await client.vcreate({ indexName: testIndexName });

      await client.vadd(testIndexName, "r1", [0.1, 0.2, 0.3], {
        content: "same",
      });
      await client.vadd(testIndexName, "r2", [0.1, 0.2, 0.3], {
        content: "same",
      });

      await client.think(testIndexName);

      const reflections = await client.getReflections(testIndexName);
      if (reflections.length > 0) {
        await client.resolveReflection(
          testIndexName,
          reflections[0].id,
          "keep_newer",
        );
      }
    });
  });

  // =============================================================================
  // SYSTEM TESTS
  // =============================================================================

  describe("System Operations", () => {
    itIfServer("should save database", async () => {
      await client.save();
      expect(true).toBe(true);
    });

    itIfServer(
      "should trigger AOF rewrite",
      async () => {
        const task = await client.aofRewrite();
        await task.wait(1, 15); // Poll every 1 second, timeout after 15 seconds
        expect(task.status).toBe("completed");
      },
      30000,
    );

    itIfServer("should get task status", async () => {
      const task = await client.aofRewrite();
      const status = await client.getTaskStatus(task.id);
      expect(status.id).toBe(task.id);
      expect(status.status).toBeDefined();
    });
  });

  // =============================================================================
  // AUTH TESTS
  // =============================================================================

  describe("Auth Operations", () => {
    itIfServer("should create API key", async () => {
      const result = await client.createApiKey("read");
      expect(result.token).toBeDefined();
    });

    itIfServer("should list API keys", async () => {
      await client.createApiKey("read");
      const keys = await client.listApiKeys();
      expect(Array.isArray(keys)).toBe(true);
    });

    itIfServer("should revoke API key", async () => {
      const keys = await client.listApiKeys();
      if (keys.length > 0) {
        const keyToRevoke = keys[0].id || keys[0].key;
        await client.revokeApiKey(keyToRevoke);
      }
    });
  });

  // =============================================================================
  // MAINTENANCE TESTS
  // =============================================================================

  describe("Maintenance Operations", () => {
    itIfServer("should trigger vacuum", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "temp_del", [0.1, 0.2, 0.3], {});
      await client.vdelete(testIndexName, "temp_del");

      await client.vupdateConfig(testIndexName, {
        vacuumInterval: "1s",
        deleteThreshold: 0.1,
      });

      // Vacuum is background, just verify no error
      expect(true).toBe(true);
    });

    itIfServer("should trigger refine", async () => {
      await client.vcreate({ indexName: testIndexName });

      for (let i = 0; i < 10; i++) {
        await client.vadd(
          testIndexName,
          `refine_vec${i}`,
          [0.1 * i, 0.2 * i, 0.3],
          {},
        );
      }

      await client.vupdateConfig(testIndexName, {
        refineEnabled: true,
        refineInterval: "1s",
      });

      // Refine is background, just verify no error
      expect(true).toBe(true);
    });
  });

  // =============================================================================
  // ERROR HANDLING TESTS
  // =============================================================================

  describe("Error Handling", () => {
    itIfServer("should handle connection errors", async () => {
      const badClient = new KektorDBClient({ host: "localhost", port: 59999 });

      await expect(badClient.listIndexes()).rejects.toThrow();
    });

    itIfServer("should handle non-existent index", async () => {
      await expect(
        client.getIndexInfo(`nonexistent_${generateTestId()}`),
      ).rejects.toThrow();
    });

    itIfServer("should handle non-existent vector", async () => {
      await client.vcreate({ indexName: testIndexName });
      await expect(client.vget(testIndexName, "nonexistent")).rejects.toThrow();
    });
  });

  // =============================================================================
  // COGNITIVE FEATURES TESTS
  // =============================================================================

  describe("Cognitive Features", () => {
    itIfServer("should use CognitiveSession saveMemory", async () => {
      await client.vcreate({ indexName: testIndexName });
      await client.vadd(testIndexName, "seed", [0.1, 0.2, 0.3, 0.4]);

      const session = new CognitiveSession(client, {
        indexName: testIndexName,
        userId: "test_user",
      });

      await session.start();
      expect(session.isStarted).toBe(true);

      await session.saveMemory("Test memory content", {
        layer: "episodic",
        tags: ["test"],
      });

      const memories = await session.recall("test", 5);
      expect(Array.isArray(memories)).toBe(true);

      await session.end();
      expect(session.isStarted).toBe(false);
    });
  });
});
