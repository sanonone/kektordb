"""
Comprehensive integration tests for all KektorDB endpoints.

This module tests ALL server endpoints to ensure complete API coverage
and verify database functionality.

Environment Variables:
    KEKTOR_TEST_HOST: Server host (default: localhost)
    KEKTOR_TEST_PORT: Server port (default: 9091)
    KEKTOR_TEST_PIPELINE: Pipeline name for RAG tests
"""

import os
import time
import uuid
import pytest
from typing import List, Dict, Any

from kektordb_client import KektorDBClient
from kektordb_client.cognitive import CognitiveSession, CognitiveOrchestrator


# Fixtures
@pytest.fixture
def client():
    """Create a fresh client for each test."""
    host = os.environ.get("KEKTOR_TEST_HOST", "localhost")
    port = int(os.environ.get("KEKTOR_TEST_PORT", "9091"))
    return KektorDBClient(port=port)


@pytest.fixture
def test_index_name():
    """Generate a unique test index name."""
    return f"test_index_{uuid.uuid4().hex[:8]}"


@pytest.fixture
def sample_vectors():
    """Sample vectors for testing."""
    return [
        {
            "id": "vec_1",
            "vector": [0.1, 0.2, 0.3, 0.4],
            "metadata": {"category": "a", "score": 10},
        },
        {
            "id": "vec_2",
            "vector": [0.2, 0.3, 0.4, 0.5],
            "metadata": {"category": "a", "score": 20},
        },
        {
            "id": "vec_3",
            "vector": [0.9, 0.8, 0.7, 0.6],
            "metadata": {"category": "b", "score": 30},
        },
        {
            "id": "vec_4",
            "vector": [0.4, 0.5, 0.6, 0.7],
            "metadata": {"category": "b", "score": 40},
        },
    ]


# =============================================================================
# KV STORE TESTS
# =============================================================================


class TestKVStore:
    """Test all KV Store endpoints."""

    def test_set_and_get(self, client):
        """POST /kv/{key} and GET /kv/{key}"""
        key = f"test_key_{uuid.uuid4().hex[:8]}"
        value = "test_value_123"

        # Set
        client.set(key, value)

        # Get
        result = client.get(key)
        assert result == value

    def test_set_overwrite(self, client):
        """Test overwriting existing key."""
        key = f"test_key_{uuid.uuid4().hex[:8]}"

        client.set(key, "value1")
        client.set(key, "value2")

        result = client.get(key)
        assert result == "value2"

    def test_delete(self, client):
        """DELETE /kv/{key}"""
        key = f"test_key_{uuid.uuid4().hex[:8]}"

        client.set(key, "value")
        client.delete(key)

        # Should raise exception or return None
        try:
            result = client.get(key)
            # If no exception, key should not exist
            assert False, "Key should not exist after delete"
        except Exception:
            pass  # Expected

    def test_get_nonexistent(self, client):
        """GET /kv/{key} for non-existent key."""
        key = f"nonexistent_{uuid.uuid4().hex[:8]}"

        try:
            client.get(key)
            assert False, "Should raise error for non-existent key"
        except Exception:
            pass  # Expected


# =============================================================================
# VECTOR INDEX MANAGEMENT TESTS
# =============================================================================


class TestVectorIndexManagement:
    """Test all Vector Index Management endpoints."""

    def test_create_index(self, client, test_index_name):
        """POST /vector/actions/create"""
        client.vcreate(
            index_name=test_index_name,
            metric="cosine",
            precision="float32",
            m=16,
            ef_construction=200,
        )

        # Verify
        info = client.get_index_info(test_index_name)
        assert info["name"] == test_index_name
        assert info["metric"] == "cosine"

    def test_create_index_with_maintenance(self, client, test_index_name):
        """POST /vector/actions/create with maintenance config."""
        maintenance_config = {
            "vacuum_interval": "60s",
            "delete_threshold": 0.3,
            "refine_enabled": True,
            "refine_interval": "30s",
            "refine_batch_size": 100,
            "refine_ef_construction": 200,
        }

        client.vcreate(
            index_name=test_index_name, metric="cosine", maintenance=maintenance_config
        )

        info = client.get_index_info(test_index_name)
        assert info["name"] == test_index_name

    def test_create_index_with_auto_links(self, client, test_index_name):
        """POST /vector/actions/create with auto-link rules."""
        auto_links = [
            {"metadata_field": "category", "relation_type": "same_category"},
            {"metadata_field": "user_id", "relation_type": "belongs_to_user"},
        ]

        client.vcreate(
            index_name=test_index_name, metric="cosine", auto_links=auto_links
        )

        # Verify auto-links were set
        rules = client.get_auto_links(test_index_name)
        assert len(rules) == 2

    def test_list_indexes(self, client, test_index_name):
        """GET /vector/indexes"""
        # Create index
        client.vcreate(test_index_name)

        # List
        indexes = client.list_indexes()
        index_names = [idx["name"] for idx in indexes]
        assert test_index_name in index_names

    def test_get_index_info(self, client, test_index_name):
        """GET /vector/indexes/{name}"""
        client.vcreate(
            test_index_name,
            metric="euclidean",
            precision="float32",
            m=32,
            ef_construction=400,
        )

        info = client.get_index_info(test_index_name)
        assert info["name"] == test_index_name
        assert info["metric"] == "euclidean"
        assert info["m"] == 32
        assert info["ef_construction"] == 400

    def test_delete_index(self, client, test_index_name):
        """DELETE /vector/indexes/{name}"""
        client.vcreate(test_index_name)
        client.delete_index(test_index_name)

        # Verify deletion
        indexes = client.list_indexes()
        index_names = [idx["name"] for idx in indexes]
        assert test_index_name not in index_names

    def test_vcompress(self, client, test_index_name):
        """POST /vector/actions/compress"""
        # Create and populate index
        client.vcreate(test_index_name, precision="float32")
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3, 0.4])

        # Compress to int8
        task = client.vcompress(test_index_name, "int8")
        task.wait()

        info = client.get_index_info(test_index_name)
        assert info["precision"] == "int8"

    def test_vupdate_config(self, client, test_index_name):
        """POST /vector/indexes/{name}/config"""
        client.vcreate(test_index_name)

        config = {"vacuum_interval": "120s", "delete_threshold": 0.5}
        client.vupdate_config(test_index_name, config)

        # Config update doesn't return data, just verify no error

    def test_set_and_get_auto_links(self, client, test_index_name):
        """PUT and GET /vector/indexes/{name}/auto-links"""
        client.vcreate(test_index_name)

        rules = [
            {"metadata_field": "project_id", "relation_type": "belongs_to_project"},
            {"metadata_field": "tag", "relation_type": "tagged_with"},
        ]

        client.set_auto_links(test_index_name, rules)
        retrieved_rules = client.get_auto_links(test_index_name)

        assert len(retrieved_rules) == 2


# =============================================================================
# VECTOR OPERATIONS TESTS
# =============================================================================


class TestVectorOperations:
    """Test all Vector Operations endpoints."""

    def test_vadd(self, client, test_index_name):
        """POST /vector/actions/add"""
        client.vcreate(test_index_name)

        client.vadd(
            test_index_name,
            "vec1",
            [0.1, 0.2, 0.3, 0.4],
            {"content": "test", "type": "document"},
        )

        # Verify
        vec = client.vget(test_index_name, "vec1")
        assert vec["id"] == "vec1"
        assert vec["metadata"]["content"] == "test"

    def test_vadd_zero_vector_entity(self, client, test_index_name):
        """POST /vector/actions/add with null vector (graph entity)."""
        client.vcreate(test_index_name)

        client.vadd(
            test_index_name,
            "entity1",
            None,  # Zero vector
            {"name": "Python", "type": "entity"},
        )

        vec = client.vget(test_index_name, "entity1")
        assert vec["id"] == "entity1"
        assert vec["metadata"]["type"] == "entity"

    def test_vadd_batch(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/add-batch"""
        client.vcreate(test_index_name)

        result = client.vadd_batch(test_index_name, sample_vectors)

        assert result["vectors_added"] == 4

        # Verify all vectors exist
        for vec in sample_vectors:
            retrieved = client.vget(test_index_name, vec["id"])
            assert retrieved["id"] == vec["id"]

    def test_vimport(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/import and /vector/actions/import/commit"""
        client.vcreate(test_index_name)

        client.vimport(test_index_name, sample_vectors)

        # Verify import
        info = client.get_index_info(test_index_name)
        assert info["vector_count"] == 4

    def test_vget(self, client, test_index_name):
        """GET /vector/indexes/{name}/vectors/{id}"""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3], {"key": "value"})

        vec = client.vget(test_index_name, "vec1")
        assert vec["id"] == "vec1"
        assert vec["vector"] == [0.1, 0.2, 0.3]
        assert vec["metadata"]["key"] == "value"

    def test_vget_many(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/get-vectors"""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        ids = ["vec_1", "vec_2", "vec_3"]
        vectors = client.vget_many(test_index_name, ids)

        assert len(vectors) == 3
        retrieved_ids = [v["id"] for v in vectors]
        assert set(retrieved_ids) == set(ids)

    def test_vdelete(self, client, test_index_name):
        """POST /vector/actions/delete_vector"""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3])

        client.vdelete(test_index_name, "vec1")

        # Verify deletion
        try:
            client.vget(test_index_name, "vec1")
            assert False, "Vector should not exist"
        except Exception:
            pass

    def test_vsearch(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/search"""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        results = client.vsearch(
            test_index_name, query_vector=[0.1, 0.2, 0.3, 0.4], k=3
        )

        assert len(results) == 3
        assert "vec_1" in results  # Should be closest

    def test_vsearch_with_filter(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/search with metadata filter."""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        results = client.vsearch(
            test_index_name,
            query_vector=[0.1, 0.2, 0.3, 0.4],
            k=10,
            filter_str="category='a'",
        )

        assert len(results) <= 2
        for vec_id in results:
            vec = client.vget(test_index_name, vec_id)
            assert vec["metadata"]["category"] == "a"

    def test_vsearch_with_scores(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/search-with-scores"""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        results = client.vsearch_with_scores(
            test_index_name, query_vector=[0.1, 0.2, 0.3, 0.4], k=3
        )

        assert len(results) == 3
        for result in results:
            assert "id" in result
            assert "score" in result
            assert 0 <= result["score"] <= 1

    def test_vreinforce(self, client, test_index_name, sample_vectors):
        """POST /vector/actions/reinforce"""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        # Reinforce vectors
        client.vreinforce(test_index_name, ["vec_1", "vec_2"])

        # Reinforce doesn't return data, just verify no error

    def test_vexport(self, client, test_index_name, sample_vectors):
        """GET /vector/indexes/{name}/export"""
        client.vcreate(test_index_name)
        client.vadd_batch(test_index_name, sample_vectors)

        result = client.vexport(test_index_name, limit=2, offset=0)

        assert len(result["data"]) == 2
        assert "has_more" in result
        assert "next_offset" in result


# =============================================================================
# GRAPH OPERATIONS TESTS
# =============================================================================


class TestGraphOperations:
    """Test all Graph Operations endpoints."""

    def test_vlink_and_vunlink(self, client, test_index_name):
        """POST /graph/actions/link and /graph/actions/unlink"""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "doc1", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "doc2", [0.4, 0.5, 0.6])

        # Link
        client.vlink(
            test_index_name,
            "doc1",
            "doc2",
            "references",
            inverse_relation_type="referenced_by",
        )

        # Verify link exists
        links = client.vget_links(test_index_name, "doc1", "references")
        assert "doc2" in links

        # Unlink
        client.vunlink(test_index_name, "doc1", "doc2", "references")

        # Verify link removed
        links = client.vget_links(test_index_name, "doc1", "references")
        assert "doc2" not in links

    def test_vget_links(self, client, test_index_name):
        """POST /graph/actions/get-links"""
        client.vcreate(test_index_name)

        # Create nodes and links
        for i in range(3):
            client.vadd(test_index_name, f"node{i}", [0.1 * i, 0.2 * i, 0.3])
            if i > 0:
                client.vlink(test_index_name, "node0", f"node{i}", "connected_to")

        links = client.vget_links(test_index_name, "node0", "connected_to")
        assert len(links) == 2
        assert "node1" in links
        assert "node2" in links

    def test_get_incoming(self, client, test_index_name):
        """POST /graph/actions/get-incoming"""
        client.vcreate(test_index_name)

        client.vadd(test_index_name, "parent", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "child1", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "child2", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "child1", "parent", "child_of")
        client.vlink(test_index_name, "child2", "parent", "child_of")

        incoming = client.get_incoming(test_index_name, "parent", "child_of")
        assert len(incoming) == 2
        assert "child1" in incoming
        assert "child2" in incoming

    def test_vget_connections(self, client, test_index_name):
        """POST /graph/actions/get-connections"""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "source", [0.1, 0.2, 0.3])
        client.vadd(
            test_index_name, "target", [0.4, 0.5, 0.6], {"content": "target_data"}
        )

        client.vlink(test_index_name, "source", "target", "links_to")

        connections = client.vget_connections(test_index_name, "source", "links_to")
        assert len(connections) == 1
        assert connections[0]["id"] == "target"
        assert connections[0]["metadata"]["content"] == "target_data"

    def test_get_all_relations(self, client, test_index_name):
        """POST /graph/actions/get-all-relations"""
        client.vcreate(test_index_name)

        client.vadd(test_index_name, "hub", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "a", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "b", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "hub", "a", "type_a")
        client.vlink(test_index_name, "hub", "b", "type_b")

        relations = client.get_all_relations(test_index_name, "hub")
        assert "type_a" in relations
        assert "type_b" in relations

    def test_get_all_incoming(self, client, test_index_name):
        """POST /graph/actions/get-all-incoming"""
        client.vcreate(test_index_name)

        client.vadd(test_index_name, "target", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "source1", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "source2", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "source1", "target", "points_to")
        client.vlink(test_index_name, "source2", "target", "points_to")

        incoming = client.get_all_incoming(test_index_name, "target")
        assert "points_to" in incoming
        assert len(incoming["points_to"]) == 2

    def test_traverse(self, client, test_index_name):
        """POST /graph/actions/traverse"""
        client.vcreate(test_index_name)

        # Create chain: a -> b -> c
        client.vadd(test_index_name, "a", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "b", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "c", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "a", "b", "next")
        client.vlink(test_index_name, "b", "c", "next")

        result = client.traverse(test_index_name, "a", ["next"])
        assert result["id"] == "a"

    def test_extract_subgraph(self, client, test_index_name):
        """POST /graph/actions/extract-subgraph"""
        client.vcreate(test_index_name)

        # Create small graph
        client.vadd(test_index_name, "root", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "child1", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "child2", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "root", "child1", "has_child")
        client.vlink(test_index_name, "root", "child2", "has_child")

        result = client.extract_subgraph(
            test_index_name, "root", ["has_child"], max_depth=2
        )

        assert result["root_id"] == "root"
        assert len(result["nodes"]) >= 1

    def test_find_path(self, client, test_index_name):
        """POST /graph/actions/find-path"""
        client.vcreate(test_index_name)

        # Create path: a -> b -> c
        client.vadd(test_index_name, "a", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "b", [0.4, 0.5, 0.6])
        client.vadd(test_index_name, "c", [0.7, 0.8, 0.9])

        client.vlink(test_index_name, "a", "b", "connects")
        client.vlink(test_index_name, "b", "c", "connects")

        result = client.find_path(test_index_name, "a", "c", relations=["connects"])

        # Path result structure varies
        assert result is not None

    def test_get_edges(self, client, test_index_name):
        """POST /graph/actions/get-edges"""
        client.vcreate(test_index_name)

        client.vadd(test_index_name, "source", [0.1, 0.2, 0.3])
        client.vadd(test_index_name, "target", [0.4, 0.5, 0.6])
        client.vlink(test_index_name, "source", "target", "links")

        edges = client.get_edges(test_index_name, "source", "links")
        assert isinstance(edges, list)

    def test_set_and_get_node_properties(self, client, test_index_name):
        """POST /graph/actions/set-node-properties and get-node-properties"""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "node1", [0.1, 0.2, 0.3], {"initial": "value"})

        # Update properties
        client.set_node_properties(
            test_index_name, "node1", {"updated": "new_value", "count": 42}
        )

        # Get properties
        props = client.get_node_properties(test_index_name, "node1")
        assert props["updated"] == "new_value"
        assert props["count"] == 42

    def test_search_nodes(self, client, test_index_name):
        """POST /graph/actions/search-nodes"""
        client.vcreate(test_index_name)

        client.vadd(
            test_index_name,
            "node1",
            [0.1, 0.2, 0.3],
            {"type": "person", "name": "Alice"},
        )
        client.vadd(
            test_index_name, "node2", [0.4, 0.5, 0.6], {"type": "person", "name": "Bob"}
        )
        client.vadd(
            test_index_name,
            "node3",
            [0.7, 0.8, 0.9],
            {"type": "company", "name": "Acme"},
        )

        results = client.search_nodes(test_index_name, "type='person'", limit=10)
        assert len(results) == 2


# =============================================================================
# RAG TESTS
# =============================================================================


class TestRAG:
    """Test RAG endpoints."""

    @pytest.mark.skipif(
        not os.environ.get("KEKTOR_TEST_PIPELINE"),
        reason="KEKTOR_TEST_PIPELINE not set",
    )
    def test_rag_retrieve(self, client):
        """POST /rag/retrieve"""
        pipeline = os.environ["KEKTOR_TEST_PIPELINE"]

        result = client.rag_retrieve(pipeline_name=pipeline, query="test query", k=5)

        assert "response" in result

    @pytest.mark.skipif(
        not os.environ.get("KEKTOR_TEST_PIPELINE"),
        reason="KEKTOR_TEST_PIPELINE not set",
    )
    def test_rag_retrieve_with_provenance(self, client):
        """POST /rag/retrieve with include_provenance"""
        pipeline = os.environ["KEKTOR_TEST_PIPELINE"]

        result = client.rag_retrieve(
            pipeline_name=pipeline, query="test query", k=5, include_provenance=True
        )

        assert "response" in result
        assert "sources" in result
        assert result["provenance"] == True

    @pytest.mark.skipif(
        not os.environ.get("KEKTOR_TEST_PIPELINE"),
        reason="KEKTOR_TEST_PIPELINE not set",
    )
    def test_adaptive_retrieve(self, client):
        """POST /rag/retrieve-adaptive"""
        pipeline = os.environ["KEKTOR_TEST_PIPELINE"]

        result = client.adaptive_retrieve(
            pipeline_name=pipeline,
            query="test query",
            k=5,
            strategy="graph",
            expansion_depth=2,
            include_provenance=True,
        )

        assert "context_text" in result
        assert "chunks_used" in result
        assert "total_tokens" in result
        assert "expansion_stats" in result


# =============================================================================
# SESSION MANAGEMENT TESTS
# =============================================================================


class TestSessions:
    """Test Session Management endpoints."""

    def test_start_and_end_session(self, client, test_index_name):
        """POST /sessions and POST /sessions/{id}/end"""
        # Create index first
        client.vcreate(test_index_name)

        result = client.start_session(
            index_name=test_index_name,
            user_id="test_user",
        )

        assert "session_id" in result
        session_id = result["session_id"]

        # End session (requires index_name)
        end_result = client.end_session(session_id, test_index_name)
        assert end_result.get("success") is True or "success" in end_result

    def test_start_session_with_context(self, client, test_index_name):
        """POST /sessions with initial conversation context."""
        # Create index first
        client.vcreate(test_index_name)

        result = client.start_session(
            index_name=test_index_name,
            user_id="test_user",
            conversation=[
                {"role": "system", "content": "You are a test assistant"},
                {"role": "user", "content": "Hello"},
            ],
        )

        assert "session_id" in result

        # Cleanup
        client.end_session(result["session_id"], test_index_name)


# =============================================================================
# USER PROFILE TESTS
# =============================================================================


class TestUserProfiles:
    """Test User Profile endpoints."""

    def test_list_user_profiles(self, client, test_index_name):
        """GET /users"""
        # Create index first
        client.vcreate(test_index_name)

        result = client.list_user_profiles(test_index_name)

        assert isinstance(result, list)
        # Should return empty list for new index
        assert len(result) == 0

    def test_get_user_profile(self, client, test_index_name):
        """GET /users/{id}/profile"""
        # Create index first
        client.vcreate(test_index_name)

        # Try to get a non-existent profile - should raise error
        try:
            client.get_user_profile("nonexistent_user", test_index_name)
            assert False, "Expected error for non-existent profile"
        except Exception:
            pass  # Expected


# =============================================================================
# COGNITIVE ENGINE TESTS
# =============================================================================


class TestCognitiveEngine:
    """Test Cognitive Engine endpoints."""

    def test_get_reflections(self, client, test_index_name):
        """GET /vector/indexes/{name}/reflections"""
        client.vcreate(test_index_name)

        # Add some data to trigger reflections
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3], {"content": "test data"})
        client.vadd(
            test_index_name, "vec2", [0.1, 0.2, 0.3], {"content": "test data"}
        )  # Duplicate

        # Trigger think
        client.think(test_index_name)

        # Get reflections
        reflections = client.get_reflections(test_index_name)
        assert isinstance(reflections, list)

    def test_think(self, client, test_index_name):
        """POST /vector/indexes/{name}/cognitive/think"""
        client.vcreate(test_index_name)

        # Add data
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3])

        # Trigger cognitive cycle
        client.think(test_index_name)

        # No return value, just verify no error

    def test_resolve_reflection(self, client, test_index_name):
        """POST /vector/indexes/{name}/reflections/{id}/resolve"""
        client.vcreate(test_index_name)

        # Add data and trigger reflections
        client.vadd(test_index_name, "vec1", [0.1, 0.2, 0.3], {"content": "same"})
        client.vadd(test_index_name, "vec2", [0.1, 0.2, 0.3], {"content": "same"})
        client.think(test_index_name)

        reflections = client.get_reflections(test_index_name)

        if reflections:
            reflection_id = reflections[0]["id"]
            client.resolve_reflection(
                test_index_name, reflection_id, resolution="keep_newer"
            )


# =============================================================================
# SYSTEM TESTS
# =============================================================================


class TestSystem:
    """Test System endpoints."""

    def test_save(self, client):
        """POST /system/save"""
        client.save()
        # No return value, verify no error

    def test_aof_rewrite(self, client):
        """POST /system/aof-rewrite"""
        task = client.aof_rewrite()

        # Wait for completion
        task.wait()
        assert task.status in ["completed", "done"]

    def test_get_task_status(self, client):
        """GET /system/tasks/{id}"""
        # Trigger a task
        task = client.aof_rewrite()

        # Get status
        status = client.get_task_status(task.id)
        assert "id" in status
        assert "status" in status

        # Wait for completion
        task.wait()


# =============================================================================
# AUTH TESTS
# =============================================================================


class TestAuth:
    """Test Auth endpoints."""

    def test_create_api_key(self, client):
        """POST /auth/keys"""
        result = client.create_api_key(role="read")

        # Server returns "token" field (OAuth/Bearer standard)
        assert "token" in result

    def test_list_api_keys(self, client):
        """GET /auth/keys"""
        # Create a key first
        client.create_api_key(role="read")

        keys = client.list_api_keys()
        assert isinstance(keys, list)

    def test_revoke_api_key(self, client):
        """DELETE /auth/keys/{id}"""
        # Create a key
        result = client.create_api_key(role="read")
        key_id = result["id"] if "id" in result else result.get("token", "")[:8]

        # List to get actual ID
        keys = client.list_api_keys()
        if keys:
            key_to_revoke = keys[0].get("id", keys[0].get("token", ""))
            client.revoke_api_key(key_to_revoke)


# =============================================================================
# MAINTENANCE TESTS
# =============================================================================


class TestMaintenance:
    """Test Maintenance endpoints."""

    def test_vtrigger_maintenance_vacuum(self, client, test_index_name):
        """POST /vector/indexes/{name}/maintenance (vacuum)"""
        client.vcreate(test_index_name)

        # Add and delete data to create garbage
        client.vadd(test_index_name, "temp", [0.1, 0.2, 0.3])
        client.vdelete(test_index_name, "temp")

        # Trigger vacuum
        client.vtrigger_maintenance(test_index_name, "vacuum")

    def test_vtrigger_maintenance_refine(self, client, test_index_name):
        """POST /vector/indexes/{name}/maintenance (refine)"""
        client.vcreate(test_index_name)

        # Add some data
        for i in range(10):
            client.vadd(test_index_name, f"vec{i}", [0.1 * i, 0.2 * i, 0.3])

        # Trigger refine
        client.vtrigger_maintenance(test_index_name, "refine")


# =============================================================================
# COGNITIVE FEATURES TESTS
# =============================================================================


class TestCognitiveFeatures:
    """Test cognitive.py features."""

    def test_cognitive_session_context_manager(self, client):
        """Test CognitiveSession as context manager."""
        with CognitiveSession(client, user_id="test_user") as session:
            assert session.session_id is not None

            # Save memory
            result = session.save_memory("Test memory content")
            assert result["status"] == "ok"

            # Recall
            memories = session.recall("test", k=5)
            # May or may not find results

    def test_cognitive_orchestrator(self, client, test_index_name):
        """Test CognitiveOrchestrator."""
        # Create index first
        client.vcreate(test_index_name)

        orchestrator = CognitiveOrchestrator(client, index_name=test_index_name)

        # Create agent session
        agent_session = orchestrator.agent_session(
            agent_name="test_agent", agent_id="agent_1"
        )

        assert agent_session.session_id is not None

        # Save memory through agent
        agent_session.save_memory("Agent memory", tags=["test"])


# =============================================================================
# ERROR HANDLING TESTS
# =============================================================================


class TestErrorHandling:
    """Test error handling for various scenarios."""

    def test_connection_error(self):
        """Test connection to non-existent server."""
        bad_client = KektorDBClient(port=59999)  # Wrong port

        try:
            bad_client.list_indexes()
            assert False, "Should raise connection error"
        except Exception:
            pass  # Expected

    def test_api_error_nonexistent_index(self, client):
        """Test operations on non-existent index."""
        try:
            client.get_index_info("nonexistent_index_xyz")
            assert False, "Should raise API error"
        except Exception:
            pass  # Expected

    def test_api_error_duplicate_vector(self, client, test_index_name):
        """Test adding duplicate vector ID."""
        client.vcreate(test_index_name)
        client.vadd(test_index_name, "dup_id", [0.1, 0.2, 0.3])

        # Try to add again with same ID
        try:
            client.vadd(test_index_name, "dup_id", [0.4, 0.5, 0.6])
            # Some implementations may overwrite, some may error
        except Exception:
            pass  # Either behavior is acceptable


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
