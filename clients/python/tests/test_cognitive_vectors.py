"""
Reproduction tests for the zero-vector bug in the cognitive session layer.

Background
----------
``CognitiveSession.save_memory`` and ``recall`` used to send a hardcoded
4-dimensional zero-vector (``[0.0, 0.0, 0.0, 0.0]``) instead of embedding the
text.  On an index whose dimension is not 4 (every real index: 384 with the
built-in ONNX embedder, 768 with Ollama) the server rejects the write with
``vector dimension mismatch: expected 384, got 4``.

Existing tests never caught this because they create 4-dimensional indexes on
purpose (``vadd(index, "seed", [0.1, 0.2, 0.3, 0.4])``), so the hardcoded
4-dim vector happens to match.

These tests use a realistic dimension and are expected to fail before the fix.
"""

import uuid

import pytest

from kektordb_client import KektorDBClient
from kektordb_client.cognitive import CognitiveSession


# Dimension used by the built-in ONNX embedder (all-MiniLM-L6-v2).
REAL_DIM = 384


class TestSessionVectorDimension:
    """The session layer must use real embeddings, not zero-vectors."""

    def test_save_memory_on_real_dimension_index(
        self, client: KektorDBClient, seeded_index
    ):
        """save_memory must succeed on an index whose dimension is realistic.

        Before the fix this raises:
            APIError: vector dimension mismatch: expected 384, got 4
        """
        index, dim = seeded_index
        with CognitiveSession(client, index, context="dim test") as session:
            result = session.save_memory("a cat sleeping on the sofa")
            assert result["status"] == "ok"
            assert result["id"]

    def test_recall_ranks_by_similarity(
        self, client: KektorDBClient, seeded_index
    ):
        """recall must return the semantically closest memory first.

        This is the behaviour the bug destroyed: with a zero vector the server
        has no signal, so ordering is arbitrary.
        """
        index, dim = seeded_index
        with CognitiveSession(client, index, context="sim test") as session:
            session.save_memory("a cat sleeping on the sofa")
            session.save_memory("a dog barking in the garden")
            session.save_memory("a car engine running")

            results = session.recall("a feline resting", k=3)

        assert len(results) >= 1, "recall returned nothing"
        # Top hit must be the cat, not the dog or the car.
        top_id = results[0] if isinstance(results[0], str) else results[0].get("id")
        top = client.vget(index, top_id)
        assert "cat" in str(top.get("metadata", {})).lower(), (
            f"expected the cat memory first, got: {top.get('metadata')}"
        )

    def test_saved_vector_is_not_zero(
        self, client: KektorDBClient, seeded_index
    ):
        """The stored vector must carry semantic signal, not be all zeros."""
        index, dim = seeded_index
        with CognitiveSession(client, index, context="vec test") as session:
            result = session.save_memory("a cat sleeping on the sofa")
            mem_id = result["id"]

        data = client.vget(index, mem_id)
        vec = data.get("vector")
        assert vec is not None, "stored vector not retrievable"
        assert len(vec) == REAL_DIM, f"expected {REAL_DIM} dims, got {len(vec)}"
        assert any(abs(v) > 1e-6 for v in vec), (
            "stored vector is all zeros: semantic search cannot work"
        )


class TestEntitiesStillUseZeroVectors:
    """The zero-vector mechanism is legitimate for graph entities.

    These tests guard the fix: it must not break the intentional
    "node without semantic content" path.
    """

    def test_entity_node_can_be_created_without_vector(
        self, client: KektorDBClient, seeded_index
    ):
        """vadd(vector=None) must still create a zero-vector entity node."""
        index, dim = seeded_index
        client.vadd(index, "entity::marco", None, {"is_entity": True})
        data = client.vget(index, "entity::marco")
        assert data["metadata"]["is_entity"] is True

    def test_entity_does_not_pollute_semantic_search(
        self, client: KektorDBClient, seeded_index
    ):
        """A zero-vector entity must not outrank real memories."""
        index, dim = seeded_index
        with CognitiveSession(client, index, context="pollution test") as s:
            s.save_memory("a cat sleeping on the sofa")

        client.vadd(index, "entity::noise", None, {"is_entity": True})

        vec = client.embed(["a feline resting"])[0]
        results = client.vsearch(index, query_vector=vec, k=3)
        assert "entity::noise" not in results[:1], (
            "zero-vector entity outranked a semantically matching memory"
        )
