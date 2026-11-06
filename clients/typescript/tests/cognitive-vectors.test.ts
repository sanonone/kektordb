/**
 * Reproduction tests for the zero-vector bug in the cognitive session layer.
 *
 * Background
 * ----------
 * `CognitiveSession.saveMemory` and `recall` used to send a hardcoded
 * 4-dimensional zero-vector (`[0, 0, 0, 0]`) instead of embedding the text.
 * On an index whose dimension is not 4 (every real index: 384 with the
 * built-in ONNX embedder, 768 with Ollama) the server rejects the write with
 * `vector dimension mismatch: expected 384, got 4`.
 *
 * Existing tests never caught this because they create 4-dimensional indexes on
 * purpose, so the hardcoded vector happens to match.
 *
 * Environment:
 *   KEKTOR_TEST_HOST - Server host (default: localhost)
 *   KEKTOR_TEST_PORT - Server port (default: 9091)
 */

import { describe, it, expect, beforeAll } from "vitest";
import { KektorDBClient } from "../src/client";
import { CognitiveSession } from "../src/cognitive";

const HOST = process.env.KEKTOR_TEST_HOST || "localhost";
const PORT = parseInt(process.env.KEKTOR_TEST_PORT || "9091", 10);

// Dimension used by the built-in ONNX embedder (all-MiniLM-L6-v2).
const REAL_DIM = 384;

async function serverAvailable(): Promise<boolean> {
  try {
    const res = await fetch(`http://${HOST}:${PORT}/vector/indexes`);
    return res.status === 200;
  } catch {
    return false;
  }
}

describe("Cognitive session vector dimension", () => {
  let client: KektorDBClient;
  let available = false;
  let indexName: string;

  beforeAll(async () => {
    available = await serverAvailable();
    if (!available) return;
    client = new KektorDBClient({ host: HOST, port: PORT });
    indexName = `test_ts_${Date.now().toString(36)}`;
    await client.vcreate({ indexName, metric: "cosine", m: 16, efConstruction: 200 });
    // Seed with a real embedding so the index learns its true dimension.
    const [seed] = await client.embed(["seed memory"]);
    expect(seed.length).toBe(REAL_DIM);
    await client.vadd(indexName, "seed", seed, { content: "seed" });
  });

  it("saveMemory succeeds on a realistic-dimension index", async () => {
    if (!available) return;
    await using_session_save(client, indexName);
  });

  it("recall ranks by similarity", async () => {
    if (!available) return;

    const session = new CognitiveSession(client, {
      indexName,
      context: "similarity test",
    });
    await session.start();
    try {
      await session.saveMemory("a cat sleeping on the sofa");
      await session.saveMemory("a dog barking in the garden");
      await session.saveMemory("a car engine running");

      const results: any = await session.recall("a feline resting", 3);
      const ids: string[] = Array.isArray(results)
        ? results.map((r: any) => (typeof r === "string" ? r : r.id))
        : [];
      expect(ids.length).toBeGreaterThan(0);

      const top = await client.vget(indexName, ids[0]);
      expect(JSON.stringify((top as any).metadata).toLowerCase()).toContain("cat");
    } finally {
      await session.end();
    }
  });
});

async function using_session_save(c: KektorDBClient, idx: string) {
  const session = new CognitiveSession(c, { indexName: idx, context: "dim test" });
  await session.start();
  try {
    const result = await session.saveMemory("a cat sleeping on the sofa");
    expect(result.status).toBe("ok");
    expect(result.id).toBeTruthy();
  } finally {
    await session.end();
  }
}
