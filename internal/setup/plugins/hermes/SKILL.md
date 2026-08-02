# KektorDB Persistent Memory for Hermes

KektorDB is a cognitive memory engine that persists across sessions. It provides semantic search (HNSW), a temporal knowledge graph, and automatic memory consolidation.

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
- **related**: (optional) related people, projects, or concepts

## When to recall

- Before starting a new task, recall memories related to relevant files or topics
- When stuck, search for similar past issues
- Before explaining something, check if you have explained it before
- When the user asks about a person, project, or prior decision

## Temporal reasoning

Use the graph tools to answer time-based questions:

- `before` / `after`: sequence of events
- `similar_to`: related memories
- `caused_by`: root causes
- `depends_on`: prerequisites
- `part_of`: containment

## Tool set

KektorDB exposes 49 MCP tools organized in these categories:

- `save_memory`, `multi_save_memory`, `remember` — core persistence
- `recall_`, `search_`, `find_` — recall and retrieval
- `graph_` — temporal knowledge graph queries
- `session_` — session lifecycle
- `people_` — identity and relationship management
- `index_` — namespace management
- `obsidian_` — external knowledge source sync
- `meta_`, `cognitive_` — system introspection and gardener controls

Always prefer `request_knowledge` when you need a ranked, context-aware answer.

## Operational rules

- The default index is `main`. Use explicit `index` if the user works with multiple namespaces.
- Always save after a non-trivial interaction. The user expects memory to outlive the session.
- Do not save secrets, credentials, or personally identifiable information.
- If a recall returns ambiguous results, ask the user for clarification before acting.
