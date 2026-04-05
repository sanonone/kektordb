"""
Integration tests for KektorDB Python client.

These tests verify end-to-end functionality by:
1. Creating an index
2. Inserting data
3. Starting a session
4. Saving memories
5. Ending session
6. Checking subconscious/reflections
"""

import time
import uuid
import pytest

from kektordb_client import KektorDBClient, CognitiveSession


class TestBasicOperations:
    """Test basic CRUD operations."""

    def test_create_index(self, client: KektorDBClient, test_index_name: str):
        """Test index creation."""
        client.vcreate(
            index_name=test_index_name, metric="cosine", m=16, ef_construction=200
        )
        # Verify it was created
        info = client.get_index_info(test_index_name)
        assert info["name"] == test_index_name

    def test_add_and_search(self, client: KektorDBClient, test_index_name: str):
        """Test adding vectors and searching."""
        # Create index
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)

        # Add a vector
        client.vadd(
            index_name=test_index_name,
            item_id="test_1",
            vector=[0.1, 0.2, 0.3, 0.4],
            metadata={"content": "Test memory about Python programming"},
        )

        # Search
        results = client.vsearch(
            index_name=test_index_name, query_vector=[0.1, 0.2, 0.3, 0.4], k=5
        )
        assert len(results) > 0


class TestSessionManagement:
    """Test session lifecycle."""

    def test_start_and_end_session(self, client: KektorDBClient, test_index_name: str):
        """Test basic session start/end."""
        # Create index first
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)
        # Add seed vector so index dimension is known
        client.vadd(test_index_name, "seed", [0.1, 0.2, 0.3, 0.4])

        # Start session
        session_result = client.start_session(
            index_name=test_index_name,
            context="Integration test session",
            agent_id="test_agent",
            user_id="test_user",
        )
        assert "session_id" in session_result
        assert session_result["status"] == "active"

        session_id = session_result["session_id"]

        # End session
        end_result = client.end_session(session_id, test_index_name)
        assert end_result["status"] == "ended"

    def test_cognitive_session_context_manager(
        self, client: KektorDBClient, test_index_name: str
    ):
        """Test CognitiveSession as context manager."""
        # Create index
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)
        # Add seed vector
        client.vadd(test_index_name, "seed", [0.1, 0.2, 0.3, 0.4])

        with CognitiveSession(
            client=client,
            index_name=test_index_name,
            context="Context manager test",
            agent_id="test_agent",
        ) as session:
            # Session should be started
            assert session.session_id is not None
            assert session._started

            # Save a memory
            result = session.save_memory(
                content="Memory saved within context manager",
                layer="episodic",
                tags=["test", "context_manager"],
            )
            assert "id" in result

        # After exiting context, session should be ended
        # (We can't directly verify this, but no exception should be raised)


class TestAdaptiveRetrieval:
    """Test adaptive retrieval with graph expansion."""

    def test_adaptive_retrieve(self, client: KektorDBClient, test_index_name: str):
        """Test adaptive retrieval endpoint."""
        # This test requires a RAG pipeline to be configured
        # Skip if no pipeline available
        try:
            result = client.adaptive_retrieve(
                pipeline_name=test_index_name,
                query="test query",
                k=3,
                include_provenance=True,
            )
            assert "context_text" in result
            assert "sources" in result or "chunks_used" in result
        except Exception as e:
            if "pipeline" in str(e).lower():
                pytest.skip(f"RAG pipeline not configured: {e}")
            raise

    def test_rag_with_provenance(self, client: KektorDBClient, test_index_name: str):
        """Test RAG retrieval with source provenance."""
        try:
            result = client.rag_retrieve(
                pipeline_name=test_index_name,
                query="test",
                k=3,
                include_provenance=True,
            )

            # Should return enriched response
            assert "response" in result
            assert "sources" in result
            assert isinstance(result["sources"], list)

            if len(result["sources"]) > 0:
                source = result["sources"][0]
                assert "chunk_id" in source
                assert "content" in source
                assert "relevance" in source
        except Exception as e:
            if "pipeline" in str(e).lower():
                pytest.skip(f"RAG pipeline not configured: {e}")
            raise


class TestUserProfiles:
    """Test user profile operations."""

    def test_list_user_profiles(self, client: KektorDBClient, test_index_name: str):
        """Test listing user profiles."""
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)
        result = client.list_user_profiles(test_index_name)
        assert "profiles" in result
        assert "count" in result
        assert isinstance(result["profiles"], list)

    def test_get_user_profile_not_found(
        self, client: KektorDBClient, test_index_name: str
    ):
        """Test getting non-existent user profile."""
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)
        try:
            result = client.get_user_profile("nonexistent_user_12345", test_index_name)
            assert result.get("user_id") == "nonexistent_user_12345"
        except Exception as e:
            assert "not found" in str(e).lower() or "404" in str(e)


class TestCognitiveEngine:
    """Test cognitive engine features."""

    def test_check_subconscious(self, client: KektorDBClient, test_index_name: str):
        """Test checking subconscious/reflections."""
        # Create index with auto-maintenance enabled
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)

        # Add some data to trigger reflections
        for i in range(5):
            client.vadd(
                test_index_name,
                f"mem_{i}",
                [0.1 * i, 0.2 * i, 0.3 * i, 0.4 * i],
                metadata={"content": f"Memory {i} about testing", "type": "memory"},
            )

        # Give time for reflections to be generated
        time.sleep(2)

        # Check subconscious (reflections)
        reflections = client.get_reflections(test_index_name)
        assert isinstance(reflections, list)
        # Note: Reflections might be empty if not enough data or time


class TestFullWorkflow:
    """Test complete end-to-end workflows."""

    def test_full_conversational_workflow(
        self, client: KektorDBClient, test_index_name: str
    ):
        """
        Complete workflow: Create index → Start session → Save memories →
        End session → Check reflections.
        """
        # 1. Create index
        client.vcreate(test_index_name, metric="cosine", m=16, ef_construction=200)
        # Add seed vector so index dimension is known for sessions
        client.vadd(test_index_name, "seed", [0.1, 0.2, 0.3, 0.4])

        # 2. Start session
        session_result = client.start_session(
            index_name=test_index_name,
            context="Customer support conversation",
            agent_id="support_bot",
            user_id="customer_123",
        )
        session_id = session_result["session_id"]

        try:
            # 3. Save multiple memories
            memories = [
                "Customer reported login issue",
                "Suggested password reset",
                "Customer confirmed issue resolved",
            ]
            for i, mem in enumerate(memories):
                client.vadd(
                    test_index_name,
                    f"mem_{i}_{uuid.uuid4().hex[:4]}",
                    [0.1, 0.2, 0.3, 0.4],
                    metadata={
                        "content": mem,
                        "type": "memory",
                        "session_id": session_id,
                        "tags": ["support", "login"],
                    },
                )

            # 4. Search within session
            search_results = client.vsearch(
                test_index_name,
                query_vector=[0.1, 0.2, 0.3, 0.4],
                k=10,
                filter_str=f"session_id='{session_id}'",
            )
            assert len(search_results) >= 3

        finally:
            # 5. End session
            client.end_session(session_id, test_index_name)

        # 6. Check for reflections (might need more time in real scenario)
        time.sleep(2)
        reflections = client.get_reflections(test_index_name)
        assert isinstance(reflections, list)


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
