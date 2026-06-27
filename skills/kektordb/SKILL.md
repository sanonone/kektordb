## KektorDB Persistent Memory — Protocol

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
