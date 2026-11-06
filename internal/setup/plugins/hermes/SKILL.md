---
name: kektordb-mcp
description: Persistent memory for coding agents backed by KektorDB. Save durable facts, decisions and gotchas with save_memory; recall them with recall_memory or request_knowledge before starting a task, when stuck, or when the user refers to earlier work. Supports a temporal knowledge graph for time-based questions.
---

# KektorDB Persistent Memory for Hermes

KektorDB is a cognitive memory engine that persists across sessions. It provides
semantic search (HNSW), a temporal knowledge graph, and automatic memory
consolidation.

## When to save memory

Call `save_memory` immediately after any of these events:

- Bug fix completed (what was broken, how you fixed it, files touched)
- Architecture or design decision made
- Non-obvious discovery about the codebase
- User preference or constraint learned
- A successful workaround for a flaky test or environment issue

## Memory format

For `save_memory`, use this structured format:

- **content**: What happened, why, where (files/paths), and what you learned
- **layer**: `episodic` (events), `semantic` (facts), or `procedural` (how-to)
- **tags**: comma-separated topic tags for searchability
- **session_id**: (optional) link to the current session
- **links**: (optional) list of entity IDs to connect this memory to

## When to recall

- Before starting a new task, recall memories related to relevant files or topics
- When stuck, search for similar past issues
- Before explaining something, check if you have explained it before
- When the user asks about a person, project, or prior decision

## Temporal reasoning

Build a graph with `create_entity` and `connect_entities`, then walk it with
`explore_connections` or `find_path`. Relation types are free-form: pick the
names that fit your domain (`caused_by`, `depends_on`, `part_of`, `before`,
`after`, ...). The server stores whatever you choose and uses it for traversal.

Do not guess relations that were never written. If a link does not exist,
`explore_connections` returns nothing rather than an error.

## Tool set

KektorDB registers 57 MCP tools. The ones that matter day to day:

**Saving and reading**

- `save_memory` — store a memory (content, layer, tags, links)
- `recall_memory` — semantic search over memories
- `scoped_recall` — search restricted to a session or user
- `get_memory`, `get_memories` — fetch by ID
- `delete_memory` — remove a memory (`hard_delete` to purge it fully)
- `reinforce_memory` — mark a memory as useful
- `unpin_memory` — let a pinned memory decay again

**Knowledge graph**

- `create_entity` — a node that carries meaning but no text
- `connect_entities` — link two nodes with a relation type
- `unlink_entities` — remove a link
- `explore_connections` — neighbours of a node, one hop
- `find_connection`, `find_path` — is there a path between two nodes
- `extract_subgraph` — pull a neighbourhood out

**Sessions and profiles**

- `start_session`, `end_session`, `list_sessions` — conversation lifecycle
- `get_user_profile`, `list_user_profiles`, `refresh_user_profile` — what the
  engine has learned about a user

**Retrieval that ranks for you**

- `request_knowledge` — ranked, context-aware answer. Prefer this over
  `recall_memory` when you need the best answer rather than raw matches
- `adaptive_retrieve` — retrieval with graph expansion
- `scoped_recall` — filtered by session or user

**Reflection and maintenance**

- `check_subconscious`, `trigger_reflection`, `list_reflections`
- `get_gardener_status` — what the background engine is doing
- `get_llm_usage` — token spend per feature

Run `tools/list` against the server for the complete set; the MCP client
discovers tools at runtime, so anything above is a starting point, not a limit.

## Operational rules

- The default index is `mcp_memory`. Pass `index_name` if the user works with
  several namespaces.
- Always save after a non-trivial interaction. The user expects memory to
  outlive the session.
- Do not save secrets, credentials, or personally identifiable information.
- If a recall returns ambiguous results, ask the user for clarification before
  acting.
