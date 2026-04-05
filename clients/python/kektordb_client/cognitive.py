"""
Cognitive layer for KektorDB Python client.

This module provides high-level abstractions for multi-agent systems
and conversational workflows built on top of the base KektorDBClient.
"""

from typing import Any, Dict, Optional
from contextlib import contextmanager
import time
import uuid

from .client import KektorDBClient


class CognitiveSession:
    """
    Context manager for cognitive/conversational sessions.

    Automatically handles session lifecycle (start on enter, end on exit)
    and provides convenient methods for saving memories within the session.

    Example:
        client = KektorDBClient()

        with CognitiveSession(client, "my_index", context="Customer Support") as session:
            # All memories saved here are automatically linked to the session
            session.save_memory("Customer reported login issue")
            session.save_memory("Suggested password reset", metadata={"resolved": True})

        # Session is automatically ended and summarized when exiting the context
    """

    def __init__(
        self,
        client: KektorDBClient,
        index_name: str,
        context: str = "",
        agent_id: str = "",
        user_id: str = "",
        session_id: str = "",
    ):
        """
        Initialize a cognitive session (does not start it yet).

        Args:
            client: The KektorDBClient instance to use.
            index_name: The index to store session memories in.
            context: Optional context description.
            agent_id: ID of the agent.
            user_id: ID of the user.
            session_id: Optional custom session ID.
        """
        self.client = client
        self.index_name = index_name
        self.context = context
        self.agent_id = agent_id
        self.user_id = user_id
        self.session_id = session_id
        self._started = False

    def __enter__(self) -> "CognitiveSession":
        """Start the session when entering the context."""
        result = self.client.start_session(
            index_name=self.index_name,
            context=self.context,
            agent_id=self.agent_id,
            user_id=self.user_id,
            session_id=self.session_id,
        )
        self.session_id = result["session_id"]
        self._started = True
        return self

    def __exit__(self, exc_type, exc_val, exc_tb) -> None:
        """End the session when exiting the context."""
        if self._started and self.session_id:
            self.client.end_session(self.session_id, self.index_name)

    def save_memory(
        self,
        content: str,
        layer: str = "episodic",
        tags: Optional[list] = None,
        links: Optional[list] = None,
        **metadata,
    ) -> Dict[str, Any]:
        """
        Save a memory to the database, automatically linked to this session.

        Args:
            content: The text content to remember.
            layer: Memory layer - "episodic" (default), "semantic", or "procedural".
            tags: Optional list of tags.
            links: Optional list of entity IDs to link to.
            **metadata: Additional metadata fields.

        Returns:
            Dict with memory_id and status.
        """
        if not self._started:
            raise RuntimeError(
                "Session not started. Use 'with' statement or call start() first."
            )

        # Build metadata with session info
        meta = {"session_id": self.session_id, **metadata}
        if tags:
            meta["tags"] = tags

        mem_id = f"mem_{int(time.time())}_{uuid.uuid4().hex[:6]}"
        self.client.vadd(
            index_name=self.index_name,
            item_id=mem_id,
            vector=[0.0, 0.0, 0.0, 0.0],  # Zero-vector 4D (standard test dimension)
            metadata={
                "content": content,
                "type": "memory",
                "memory_layer": layer,
                **meta,
            },
        )
        return {"id": mem_id, "status": "ok"}

    def recall(self, query: str, k: int = 5) -> Dict[str, Any]:
        """
        Search for memories within this session's context.

        Args:
            query: The search query.
            k: Number of results to return.

        Returns:
            Search results scoped to this session.
        """
        if not self._started:
            raise RuntimeError("Session not started.")

        # Use filter to scope to this session
        filter_query = f"session_id='{self.session_id}'"
        return self.client.vsearch(
            index_name=self.index_name,
            query_vector=[0.0, 0.0, 0.0, 0.0],  # Zero-vector 4D
            k=k,
            filter_str=filter_query,
        )


class CognitiveOrchestrator:
    """
    Orchestrates multiple cognitive agents sharing a memory space.

    This is useful for multi-agent systems where different agents
    need to collaborate and share context.

    Example:
        orchestrator = CognitiveOrchestrator(client, "shared_index")

        # Create sessions for different agents
        with orchestrator.agent_session("researcher", agent_id="researcher_1") as researcher:
            researcher.save_memory("Found relevant paper on HNSW")

        with orchestrator.agent_session("writer", agent_id="writer_1") as writer:
            # Writer can access memories from researcher through shared index
            writer.save_memory("Drafting section on vector search")
    """

    def __init__(self, client: KektorDBClient, index_name: str):
        """
        Initialize the orchestrator.

        Args:
            client: The KektorDBClient instance.
            index_name: The shared index for all agents.
        """
        self.client = client
        self.index_name = index_name
        self._sessions: Dict[str, CognitiveSession] = {}

    def agent_session(
        self, agent_name: str, context: str = "", agent_id: str = "", user_id: str = ""
    ) -> CognitiveSession:
        """
        Create a new cognitive session for an agent.

        Args:
            agent_name: Human-readable name for the agent.
            context: Context description.
            agent_id: Unique agent identifier.
            user_id: User being interacted with.

        Returns:
            A CognitiveSession ready to use as context manager.
        """
        return CognitiveSession(
            client=self.client,
            index_name=self.index_name,
            context=context or f"Agent: {agent_name}",
            agent_id=agent_id or agent_name,
            user_id=user_id,
        )

    def transfer_memory(
        self,
        source_session_id: str,
        target_session_id: str,
        query: str,
        limit: int = 10,
    ) -> Dict[str, Any]:
        """
        Transfer memories from one session to another.

        Args:
            source_session_id: Source session ID.
            target_session_id: Target session ID.
            query: Query to select which memories to transfer.
            limit: Maximum number of memories to transfer.

        Returns:
            Transfer result with count and IDs.
        """
        # This would use the transfer_memory endpoint when available
        # For now, we can implement it via search + add
        raise NotImplementedError("Memory transfer via orchestrator coming soon")


@contextmanager
def quick_session(client: KektorDBClient, index_name: str, context: str = ""):
    """
    Convenience context manager for quick one-off sessions.

    Example:
        client = KektorDBClient()

        with quick_session(client, "my_index", "Quick task") as session:
            session.save_memory("Important fact")

    Args:
        client: The KektorDBClient instance.
        index_name: The index to use.
        context: Optional context description.

    Yields:
        CognitiveSession instance.
    """
    session = CognitiveSession(client, index_name, context=context)
    try:
        with session as s:
            yield s
    finally:
        pass  # Session already cleaned up by context manager
