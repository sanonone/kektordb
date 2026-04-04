/**
 * Cognitive layer for KektorDB TypeScript client.
 *
 * This module provides high-level abstractions for multi-agent systems
 * and conversational workflows built on top of the base KektorDBClient.
 */

import { KektorDBClient } from "./client";
import type { StartSessionResult, SourceAttribution } from "./types";

/**
 * Options for creating a CognitiveSession.
 */
export interface CognitiveSessionOptions {
  indexName: string;
  context?: string;
  agentId?: string;
  userId?: string;
  sessionId?: string;
}

/**
 * Represents a cognitive/conversational session.
 *
 * Automatically handles session lifecycle and provides convenient
 * methods for saving memories within the session context.
 *
 * @example
 * ```typescript
 * const client = new KektorDBClient();
 * const session = new CognitiveSession(client, {
 *   indexName: "my_index",
 *   context: "Customer Support"
 * });
 *
 * await session.start();
 * await session.saveMemory("Customer reported login issue");
 * await session.end();
 * ```
 */
export class CognitiveSession {
  private client: KektorDBClient;
  private indexName: string;
  private context?: string;
  private agentId?: string;
  private userId?: string;
  private _sessionId?: string;
  private _started = false;

  constructor(client: KektorDBClient, options: CognitiveSessionOptions) {
    this.client = client;
    this.indexName = options.indexName;
    this.context = options.context;
    this.agentId = options.agentId;
    this.userId = options.userId;
    if (options.sessionId) {
      this._sessionId = options.sessionId;
    }
  }

  /**
   * Start the session.
   */
  async start(): Promise<void> {
    const result = await this.client.startSession({
      indexName: this.indexName,
      context: this.context,
      agentId: this.agentId,
      userId: this.userId,
      sessionId: this._sessionId,
    });
    this._sessionId = result.session_id;
    this._started = true;
  }

  /**
   * End the session.
   */
  async end(): Promise<void> {
    if (this._started && this._sessionId) {
      await this.client.endSession(this._sessionId, {
        indexName: this.indexName,
      });
      this._started = false;
    }
  }

  /**
   * Save a memory to the database, automatically linked to this session.
   */
  async saveMemory(
    content: string,
    options: {
      layer?: "episodic" | "semantic" | "procedural";
      tags?: string[];
      links?: string[];
      metadata?: Record<string, any>;
    } = {}
  ): Promise<{ id: string; status: string }> {
    if (!this._started) {
      throw new Error(
        "Session not started. Call start() first or use withSession()."
      );
    }

    const metadata: Record<string, any> = {
      content,
      type: "memory",
      memory_layer: options.layer ?? "episodic",
      session_id: this._sessionId,
      ...options.metadata,
    };

    if (options.tags) {
      metadata.tags = options.tags;
    }

    // Generate unique ID for the memory
    const id = `mem_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
    await this.client.vadd(this.indexName, id, null, metadata);
    return { id, status: "ok" };
  }

  /**
   * Search for memories within this session's context.
   */
  async recall(query: string, k = 5): Promise<any> {
    if (!this._started) {
      throw new Error("Session not started.");
    }

    return this.client.vsearch({
      indexName: this.indexName,
      queryVector: [], // Will be embedded
      k,
      filter: `session_id='${this._sessionId}'`,
    });
  }

  get sessionId(): string | undefined {
    return this._sessionId;
  }

  get isStarted(): boolean {
    return this._started;
  }
}

/**
 * Manages multiple cognitive sessions.
 *
 * Useful for multi-agent systems where different agents
 * need to collaborate and share context.
 */
export class SessionManager {
  private client: KektorDBClient;
  private indexName: string;
  private sessions: Map<string, CognitiveSession> = new Map();

  constructor(client: KektorDBClient, indexName: string) {
    this.client = client;
    this.indexName = indexName;
  }

  /**
   * Create and start a new session for an agent.
   */
  async createSession(
    agentName: string,
    options: {
      context?: string;
      agentId?: string;
      userId?: string;
    } = {}
  ): Promise<CognitiveSession> {
    const session = new CognitiveSession(this.client, {
      indexName: this.indexName,
      context: options.context ?? `Agent: ${agentName}`,
      agentId: options.agentId ?? agentName,
      userId: options.userId,
    });

    await session.start();
    this.sessions.set(agentName, session);
    return session;
  }

  /**
   * Get an existing session by agent name.
   */
  getSession(agentName: string): CognitiveSession | undefined {
    return this.sessions.get(agentName);
  }

  /**
   * End all managed sessions.
   */
  async endAllSessions(): Promise<void> {
    const promises = Array.from(this.sessions.values()).map((session) =>
      session.end().catch(() => {
        // Ignore errors during cleanup
      })
    );
    await Promise.all(promises);
    this.sessions.clear();
  }
}

/**
 * Execute a function within a session context.
 *
 * Automatically handles session lifecycle.
 *
 * @example
 * ```typescript
 * await withSession(client, { indexName: "my_index" }, async (session) => {
 *   await session.saveMemory("Important fact");
 *   // Session automatically ends after this function
 * });
 * ```
 */
export async function withSession<T>(
  client: KektorDBClient,
  options: CognitiveSessionOptions,
  fn: (session: CognitiveSession) => Promise<T>
): Promise<T> {
  const session = new CognitiveSession(client, options);
  await session.start();
  try {
    return await fn(session);
  } finally {
    await session.end();
  }
}

/**
 * Re-export SourceAttribution for convenience.
 */
export type { SourceAttribution };
