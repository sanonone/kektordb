/**
 * KektorDB — OpenCode plugin adapter
 *
 * Connects OpenCode's event system to KektorDB's HTTP API.
 * The KektorDB Go binary runs as a separate process (via MCP or HTTP server).
 *
 * Flow:
 *   OpenCode events → this plugin → HTTP calls → KektorDB API → Engine
 */

// ─── Configuration ───────────────────────────────────────────────────────────

const KEKTORDB_PORT = parseInt(process.env.KEKTORDB_PORT ?? "9091")
const KEKTORDB_URL = `http://127.0.0.1:${KEKTORDB_PORT}`
const KEKTORDB_BIN = process.env.KEKTORDB_BIN ?? "kektordb"

// ─── Memory Instructions ─────────────────────────────────────────────────────
// Injected into the agent's system prompt so it knows when and how to save.

const MEMORY_INSTRUCTIONS = `## KektorDB Persistent Memory — Protocol

You have access to KektorDB, a cognitive memory system that persists across sessions.
It provides semantic search, a knowledge graph, and automatic memory consolidation.

### WHEN TO SAVE (mandatory — not optional)
Call save_memory IMMEDIATELY after any of these:
- Bug fix completed (what was broken, how you fixed it)
- Architecture or design decision made
- Non-obvious discovery about the codebase
- User preference or constraint learned

### MEMORY FORMAT
For save_memory, use this structured format:
- **content**: What happened, why, where (files/paths), and what you learned
- **layer**: episodic (events), semantic (facts), or procedural (how-to)
- **tags**: comma-separated topic tags for searchability
- **session_id**: (optional) link to current session

### WHEN TO RECALL
- Before starting a new task, recall memories related to relevant files or topics
- When stuck, search for similar past issues
- Before explaining something, check if you've explained it before
`

// ─── Plugin Implementation ───────────────────────────────────────────────────

interface PluginContext {
  // Provided by OpenCode runtime
}

interface SystemPromptInput {
  // Provided by OpenCode
}

export default {
  /**
   * Called when a new OpenCode session starts.
   * Ensures the KektorDB memory index exists.
   */
  async sessionStart(_input: any) {
    try {
      await fetch(`${KEKTORDB_URL}/vector/actions/create`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: "mcp_memory",
          metric: "cosine",
          dim: 384,
          text_language: "english",
        }),
      })
    } catch {
      // Index may already exist — this is fine.
    }
  },

  /**
   * Inject memory instructions into the agent's system prompt.
   * Tells the agent when and how to use KektorDB tools.
   */
  async systemPrompt(_input: any): Promise<{ additions: string[] }> {
    return {
      additions: [MEMORY_INSTRUCTIONS],
    }
  },
}
