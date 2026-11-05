## KektorDB Persistent Memory — Protocol

You have access to KektorDB, a cognitive memory system that persists across sessions.
It provides semantic search, a knowledge graph, and automatic memory consolidation.

### WHEN TO SAVE (mandatory — not optional)
Call `save_memory` IMMEDIATELY after any of these:
- Bug fix completed (what was broken, how you fixed it)
- Architecture or design decision made
- Non-obvious discovery about the codebase
- User preference or constraint learned

### MEMORY FORMAT
- **content**: what happened, why, where (files/paths), what you learned
- **layer**: episodic (events), semantic (facts), procedural (how-to)
- **tags**: comma-separated topic tags for searchability

### WHEN TO RECALL
- Before starting a task: `recall_memory` on relevant files or topics
- When stuck: search for similar past issues
- Before explaining something: check whether you explained it before
- When the question is scoped ("bugs in Project X"): `scoped_recall` instead of a broad search

### THE GRAPH — use it, do not ignore it
Memories alone find *similar text*. The graph finds *connected knowledge*, which is
different and often what the user is asking for.

- **`create_entity`** when a memory introduces a person, project, technology or place
  that will come back. Entities are shared: "Marco" in April and "Marco" in September
  are the same node.
- **`connect_entities`** when two things are related. The link is what lets a later
  question about one reach the other.
- **`explore_connections`** when the user asks "what do you know about X" or "how are
  X and Y related" — this walks the links that plain search cannot see.

Rule of thumb: if you save a memory that names a thing worth remembering by name,
create the entity. If you notice two things are related, connect them.

### WHEN YOU WERE WRONG
`delete_memory` when a memory is factually wrong. Do not leave a wrong memory in place
hoping a newer one will win: without deletion, retrieval can surface either.
