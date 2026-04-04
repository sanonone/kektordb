/**
 * Integration tests for KektorDB TypeScript client
 * Tests sessions, adaptive retrieval, user profiles, and cognitive workflows
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
import { spawn, ChildProcess } from "child_process";
import { join } from "path";
import { existsSync } from "fs";

import { KektorClient } from "../src/client";
import { SessionManager, withSession, CognitiveSession } from "../src/cognitive";
import type { SourceAttribution, GraphPath, UserProfile } from "../src/types";

const HOST = process.env.KEKTOR_TEST_HOST || "localhost";
const PORT = parseInt(process.env.KEKTOR_TEST_PORT || "9091", 10);
const PIPELINE_NAME = process.env.KEKTOR_TEST_PIPELINE || "";

// Helper to check if server is available
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

// Helper to auto-start server if needed
async function maybeStartServer(): Promise<ChildProcess | null> {
  if (await isServerAvailable()) {
    console.log("Using existing KektorDB server");
    return null;
  }

  console.log("Starting KektorDB server for tests...");

  // Try to find the server binary
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

  // Wait for server to be ready
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

describe("KektorDB Integration Tests", () => {
  let client: KektorClient;
  let serverProc: ChildProcess | null = null;
  let serverAvailable = false;

  beforeAll(async () => {
    serverProc = await maybeStartServer();
    serverAvailable = await isServerAvailable();

    if (!serverAvailable) {
      console.warn(
        "WARNING: KektorDB server not available, tests will be skipped"
      );
    }

    client = new KektorClient(HOST, PORT);
  }, 60000);

  afterAll(() => {
    if (serverProc) {
      console.log("Shutting down test server...");
      serverProc.kill();
    }
  });

  describe("Basic Connectivity", () => {
    it("should connect to server", async () => {
      if (!serverAvailable) {
        console.warn("Server not available, skipping");
        return;
      }

      const indexes = await client.listIndexes();
      expect(Array.isArray(indexes)).toBe(true);
    });
  });

  describe("Session Management", () => {
    it("should start and end a session", async () => {
      if (!serverAvailable) return;

      const result = await client.startSession({
        user_id: "test-user",
        metadata: { test: true },
      });

      expect(result.session_id).toBeDefined();
      expect(result.session_id.length).toBeGreaterThan(0);

      const endResult = await client.endSession(result.session_id);
      expect(endResult.success).toBe(true);
    });

    it("should start session with initial context", async () => {
      if (!serverAvailable) return;

      const result = await client.startSession({
        user_id: "test-user",
        conversation: [
          { role: "system", content: "You are a helpful assistant" },
          { role: "user", content: "Hello" },
        ],
      });

      expect(result.session_id).toBeDefined();
      expect(result.conversation).toBeDefined();
      expect(result.conversation?.length).toBe(2);

      await client.endSession(result.session_id);
    });
  });

  describe("SessionManager (Cognitive)", () => {
    it("should create and manage sessions", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client);
      const session = await manager.createSession({
        userId: "test-user",
        metadata: { test: true },
      });

      expect(session.id).toBeDefined();
      expect(session.getContext()).toEqual([]);

      // Add messages
      session.addMessage("user", "Hello");
      session.addMessage("assistant", "Hi there!");

      const context = session.getContext();
      expect(context.length).toBe(2);
      expect(context[0].role).toBe("user");
      expect(context[0].content).toBe("Hello");

      await manager.endSession(session.id);
    });

    it("should list active sessions", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client);
      const session1 = await manager.createSession({});
      const session2 = await manager.createSession({});

      const sessions = manager.listSessions();
      expect(sessions.length).toBeGreaterThanOrEqual(2);
      expect(sessions).toContain(session1.id);
      expect(sessions).toContain(session2.id);

      await manager.endSession(session1.id);
      await manager.endSession(session2.id);
    });

    it("should support withSession pattern", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client);
      let sessionId: string | undefined;

      await withSession(
        manager,
        { userId: "test-user" },
        async (session: CognitiveSession) => {
          sessionId = session.id;
          session.addMessage("user", "Test message");
          expect(session.getContext().length).toBe(1);
        }
      );

      // Session should be ended after withSession
      const sessions = manager.listSessions();
      expect(sessions).not.toContain(sessionId);
    });
  });

  describe("User Profiles", () => {
    it("should list user profiles", async () => {
      if (!serverAvailable) return;

      const profiles = await client.listUserProfiles();
      expect(Array.isArray(profiles)).toBe(true);
    });

    it("should get user profile", async () => {
      if (!serverAvailable) return;

      // First list to get a user ID
      const profiles = await client.listUserProfiles();
      if (profiles.length === 0) {
        console.warn("No user profiles available, skipping get test");
        return;
      }

      const userId = profiles[0].user_id;
      const profile = await client.getUserProfile(userId);

      expect(profile.user_id).toBe(userId);
      expect(typeof profile.confidence).toBe("number");
    });
  });

  describe("Adaptive Retrieval", () => {
    it("should perform adaptive retrieve", async () => {
      if (!serverAvailable) return;
      if (!PIPELINE_NAME) {
        console.warn("KEKTOR_TEST_PIPELINE not set, skipping adaptive retrieval test");
        return;
      }

      const result = await client.adaptiveRetrieve({
        pipeline_name: PIPELINE_NAME,
        query: "test query",
        k: 5,
        strategy: "graph",
        expansion_depth: 2,
        include_provenance: true,
      });

      expect(result.context_text).toBeDefined();
      expect(typeof result.chunks_used).toBe("number");
      expect(typeof result.total_tokens).toBe("number");
      expect(result.expansion_stats).toBeDefined();
    });

    it("should retrieve with provenance", async () => {
      if (!serverAvailable) return;
      if (!PIPELINE_NAME) {
        console.warn("KEKTOR_TEST_PIPELINE not set, skipping provenance test");
        return;
      }

      const result = await client.ragRetrieve({
        pipeline_name: PIPELINE_NAME,
        query: "test query",
        k: 5,
        include_provenance: true,
      });

      expect(result.response).toBeDefined();
      expect(Array.isArray(result.sources)).toBe(true);
      expect(typeof result.confidence).toBe("number");
      expect(result.provenance).toBe(true);

      // Check source attribution structure if sources exist
      if (result.sources.length > 0) {
        const source: SourceAttribution = result.sources[0];
        expect(source.chunk_id).toBeDefined();
        expect(typeof source.relevance).toBe("number");
        expect(source.graph_path).toBeDefined();
      }
    });
  });

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

      const formatted = KektorClient.formatSources(sources);
      expect(formatted).toContain("file.pdf");
      expect(formatted).toContain("0.95");
    });

    it("should filter sources by relevance", async () => {
      const sources: SourceAttribution[] = [
        { chunk_id: "1", relevance: 0.9, graph_depth: 0 } as SourceAttribution,
        { chunk_id: "2", relevance: 0.5, graph_depth: 0 } as SourceAttribution,
        { chunk_id: "3", relevance: 0.95, graph_depth: 0 } as SourceAttribution,
      ];

      const filtered = KektorClient.filterSources(sources, { minRelevance: 0.8 });
      expect(filtered.length).toBe(2);
    });

    it("should group sources by document", async () => {
      const sources: SourceAttribution[] = [
        { chunk_id: "c1", document_id: "doc-a" } as SourceAttribution,
        { chunk_id: "c2", document_id: "doc-a" } as SourceAttribution,
        { chunk_id: "c3", document_id: "doc-b" } as SourceAttribution,
      ];

      const grouped = KektorClient.groupSourcesByDocument(sources);
      expect(grouped["doc-a"].length).toBe(2);
      expect(grouped["doc-b"].length).toBe(1);
    });
  });

  describe("Full Workflow", () => {
    it("should run complete cognitive workflow", async () => {
      if (!serverAvailable) return;

      const manager = new SessionManager(client);

      // Create session
      const session = await manager.createSession({
        user_id: "workflow-test-user",
        metadata: { workflow: "integration-test" },
      });

      expect(session.id).toBeDefined();

      // Add conversation context
      session.addMessage("system", "You are a helpful assistant");
      session.addMessage("user", "What can you tell me about this topic?");

      // If pipeline available, perform retrieval
      if (PIPELINE_NAME) {
        try {
          const result = await client.adaptiveRetrieve({
            pipeline_name: PIPELINE_NAME,
            query: "Tell me about this topic",
            k: 5,
            strategy: "graph",
            include_provenance: true,
          });

          // Add retrieval result to context
          session.addMessage(
            "assistant",
            `Retrieved ${result.chunks_used} chunks with ${result.total_tokens} tokens`
          );
        } catch (e) {
          console.warn("Adaptive retrieve failed (may need configured pipeline):", e);
        }
      }

      // Verify final context
      const finalContext = session.getContext();
      expect(finalContext.length).toBeGreaterThanOrEqual(2);

      // Cleanup
      await manager.endSession(session.id);
    });
  });
});
