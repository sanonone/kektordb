/**
 * Integration tests for KektorDB TypeScript client.
 *
 * Environment:
 *   KEKTOR_TEST_HOST - Server host (default: localhost)
 *   KEKTOR_TEST_PORT - Server port (default: 9091)
 *   KEKTOR_TEST_PIPELINE - Pipeline name for adaptive retrieval tests
 *
 * Usage:
 *   npx vitest run tests/integration.test.ts
 */

import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { KektorDBClient } from "../src/client";
import {
  SessionManager,
  withSession,
  CognitiveSession,
} from "../src/cognitive";
import type { SourceAttribution } from "../src/types";

const HOST = process.env.KEKTOR_TEST_HOST || "localhost";
const PORT = parseInt(process.env.KEKTOR_TEST_PORT || "9091", 10);
const PIPELINE_NAME = process.env.KEKTOR_TEST_PIPELINE || "";

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

describe("KektorDB Integration Tests", () => {
  let client: KektorDBClient;
  let serverAvailable = false;

  beforeAll(async () => {
    serverAvailable = await isServerAvailable();
    if (!serverAvailable) {
      console.warn(
        "WARNING: KektorDB server not available at " +
          `http://${HOST}:${PORT}, tests will be skipped`
      );
    }
    client = new KektorDBClient({ host: HOST, port: PORT });
  }, 15000);

  // --- Basic Connectivity ---

  describe("Basic Connectivity", () => {
    it("should connect to server", async () => {
      if (!serverAvailable) return;
      const indexes = await client.listIndexes();
      expect(Array.isArray(indexes)).toBe(true);
    });
  });

  // --- Index + Session Lifecycle ---

  describe("Session Management", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_integration_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
      // Add seed vector so index dimension is known for sessions
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should start and end a session", async () => {
      if (!serverAvailable) return;

      const result = await client.startSession({
        indexName: idxName,
        context: "integration test",
        userId: "test-user",
      });

      expect(result.session_id).toBeDefined();
      expect(result.session_id.length).toBeGreaterThan(0);

      const endResult = await client.endSession(result.session_id, {
        indexName: idxName,
      });
      expect(endResult.session_id).toBe(result.session_id);
    });

    it("should start session with agentId", async () => {
      if (!serverAvailable) return;

      const result = await client.startSession({
        indexName: idxName,
        context: "agent test",
        agentId: "test-agent",
        userId: "test-user",
      });

      expect(result.session_id).toBeDefined();
      await client.endSession(result.session_id, { indexName: idxName });
    });
  });

  // --- CognitiveSession ---

  describe("CognitiveSession", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_cognitive_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should start, save memory, and end", async () => {
      if (!serverAvailable) return;

      const session = new CognitiveSession(client, {
        indexName: idxName,
        context: "memory test",
        agentId: "test-agent",
      });

      await session.start();
      expect(session.isStarted).toBe(true);
      expect(session.sessionId).toBeDefined();

      const mem = await session.saveMemory("Test memory content", {
        layer: "episodic",
        tags: ["test"],
      });
      expect(mem.id).toBeDefined();
      expect(mem.status).toBe("ok");

      await session.end();
      expect(session.isStarted).toBe(false);
    });

    it("should support withSession pattern", async () => {
      if (!serverAvailable) return;

      let capturedId: string | undefined;
      await withSession(
        client,
        { indexName: idxName, context: "withSession test" },
        async (session) => {
          capturedId = session.sessionId;
          await session.saveMemory("Memory inside withSession");
          expect(session.isStarted).toBe(true);
        }
      );
      expect(capturedId).toBeDefined();
    });
  });

  // --- SessionManager ---

  describe("SessionManager", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_manager_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should create and manage sessions", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client, idxName);
      const session = await manager.createSession("researcher", {
        context: "research task",
      });

      expect(session.isStarted).toBe(true);
      expect(session.sessionId).toBeDefined();

      const got = manager.getSession("researcher");
      expect(got).toBeDefined();

      await manager.endAllSessions();
    });

    it("should support multiple agents", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client, idxName);
      await manager.createSession("agent-a");
      await manager.createSession("agent-b");

      expect(manager.getSession("agent-a")).toBeDefined();
      expect(manager.getSession("agent-b")).toBeDefined();

      await manager.endAllSessions();
    });
  });

  // --- User Profiles ---

  describe("User Profiles", () => {
    it("should list user profiles", async () => {
      if (!serverAvailable) return;

      const result = await client.listUserProfiles("default");
      expect(result).toHaveProperty("profiles");
      expect(result).toHaveProperty("count");
      expect(Array.isArray(result.profiles)).toBe(true);
    });

    it("should return 404 for non-existent profile", async () => {
      if (!serverAvailable) return;

      await expect(
        client.getUserProfile("nonexistent_xyz", "default")
      ).rejects.toThrow();
    });
  });

  // --- Vector CRUD (smoke test) ---

  describe("Vector CRUD", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_crud_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should add, get, search, and delete vectors", async () => {
      if (!serverAvailable) return;

      await client.vadd(idxName, "v1", [0.1, 0.2, 0.3, 0.4], {
        content: "hello",
      });
      await client.vadd(idxName, "v2", [0.5, 0.6, 0.7, 0.8], {
        content: "world",
      });

      const vec = await client.vget(idxName, "v1");
      expect(vec.id).toBe("v1");

      const results = await client.vsearch({
        indexName: idxName,
        queryVector: [0.1, 0.2, 0.3, 0.4],
        k: 5,
      });
      expect(results.length).toBeGreaterThanOrEqual(1);

      await client.vdelete(idxName, "v1");
      await expect(client.vget(idxName, "v1")).rejects.toThrow();
    });

    it("should support batch add", async () => {
      if (!serverAvailable) return;

      const res = await client.vaddBatch(idxName, [
        { id: "b1", vector: [0.1, 0.2, 0.3, 0.4] },
        { id: "b2", vector: [0.4, 0.5, 0.6, 0.7] },
      ]);
      expect(res.vectors_added).toBeGreaterThanOrEqual(2);
    });

    it("should return scored results", async () => {
      if (!serverAvailable) return;

      const scored = await client.vsearchWithScores(idxName, [0.1, 0.2, 0.3, 0.4], 5);
      expect(scored.length).toBeGreaterThanOrEqual(1);
      // Server returns PascalCase: {"ID": "x", "Score": 0.5}
      expect(scored[0]).toHaveProperty("Score");
    });
  });

  // --- Graph Operations (smoke test) ---

  describe("Graph Operations", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_graph_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
      await client.vadd(idxName, "node-a", [0.1, 0.2, 0.3]);
      await client.vadd(idxName, "node-b", [0.4, 0.5, 0.6]);
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should create and read links", async () => {
      if (!serverAvailable) return;

      await client.vlink({
        indexName: idxName,
        sourceId: "node-a",
        targetId: "node-b",
        relationType: "related",
      });

      const links = await client.vgetLinks(idxName, "node-a", "related");
      expect(links).toContain("node-b");
    });

    it("should get incoming links", async () => {
      if (!serverAvailable) return;

      const incoming = await client.getIncoming(idxName, "node-b", "related");
      expect(incoming).toContain("node-a");
    });

    it("should unlink", async () => {
      if (!serverAvailable) return;

      await client.vunlink(idxName, "node-a", "node-b", "related");
      const links = await client.vgetLinks(idxName, "node-a", "related");
      expect(links).not.toContain("node-b");
    });
  });

  // --- Adaptive Retrieval (requires pipeline) ---

  describe("Adaptive Retrieval", () => {
    it("should perform adaptive retrieve when pipeline configured", async () => {
      if (!serverAvailable) return;
      if (!PIPELINE_NAME) {
        console.warn("KEKTOR_TEST_PIPELINE not set, skipping");
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

      expect(result).toHaveProperty("context_text");
      expect(result).toHaveProperty("chunks_used");
      expect(result).toHaveProperty("expansion_stats");
    });

    it("should retrieve with provenance when pipeline configured", async () => {
      if (!serverAvailable) return;
      if (!PIPELINE_NAME) {
        console.warn("KEKTOR_TEST_PIPELINE not set, skipping");
        return;
      }

      const result = await client.ragRetrieve(
        PIPELINE_NAME,
        "test query",
        5,
        true
      );

      expect(result).toHaveProperty("response");
      expect(result).toHaveProperty("sources");
      expect(Array.isArray(result.sources)).toBe(true);
    });
  });

  // --- Source Attribution (static utils) ---

  describe("Source Attribution", () => {
    it("should format sources correctly", async () => {
      const sources: SourceAttribution[] = [
        {
          chunk_id: "chunk-1",
          document_id: "doc-1",
          source_file: "/path/to/file.pdf",
          filename: "file.pdf",
          chunk_index: 0,
          page_number: 1,
          content: "This is test content that is long enough to be truncated",
          relevance: 0.95,
          graph_depth: 0,
          graph_path: {
            nodes: [{ id: "doc-1", type: "document", label: "Document" }],
            edges: [],
            formatted: "doc-1",
          },
          verified: true,
        },
      ];

      const formatted = KektorDBClient.formatSources(sources);
      expect(formatted).toContain("file.pdf");
      expect(formatted).toContain("0.95");
    });

    it("should filter sources by relevance", async () => {
      const sources: SourceAttribution[] = [
        { chunk_id: "1", relevance: 0.9, graph_depth: 0 } as SourceAttribution,
        { chunk_id: "2", relevance: 0.5, graph_depth: 0 } as SourceAttribution,
        { chunk_id: "3", relevance: 0.95, graph_depth: 0 } as SourceAttribution,
      ];

      const filtered = KektorDBClient.filterSources(sources, {
        minRelevance: 0.8,
      });
      expect(filtered.length).toBe(2);
    });

    it("should group sources by document", async () => {
      const sources: SourceAttribution[] = [
        { chunk_id: "c1", document_id: "doc-a" } as SourceAttribution,
        { chunk_id: "c2", document_id: "doc-a" } as SourceAttribution,
        { chunk_id: "c3", document_id: "doc-b" } as SourceAttribution,
      ];

      const grouped = KektorDBClient.groupSourcesByDocument(sources);
      expect(grouped["doc-a"].length).toBe(2);
      expect(grouped["doc-b"].length).toBe(1);
    });
  });

  // --- Full Workflow ---

  describe("Full Workflow", () => {
    let idxName: string;

    beforeAll(async () => {
      if (!serverAvailable) return;
      idxName = `ts_workflow_${Date.now()}`;
      await client.vcreate({
        indexName: idxName,
        metric: "cosine",
        precision: "float32",
      });
      await client.vadd(idxName, "seed", [0.1, 0.2, 0.3, 0.4]);
    });

    afterAll(async () => {
      if (!serverAvailable) return;
      try {
        await client.deleteIndex(idxName);
      } catch {}
    });

    it("should run complete cognitive workflow", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client, idxName);

      const session = await manager.createSession("workflow-agent", {
        context: "integration workflow",
      });
      expect(session.isStarted).toBe(true);

      await session.saveMemory("First memory", { tags: ["workflow"] });
      await session.saveMemory("Second memory", { tags: ["workflow"] });

      const recalled = await session.recall("memory", 10);
      expect(Array.isArray(recalled)).toBe(true);

      await manager.endAllSessions();
    });
  });
});
