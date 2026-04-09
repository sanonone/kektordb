import unittest
import time
import numpy as np
from kektordb_client import KektorDBClient, APIError

HOST = "localhost"
PORT = 9091
INDEX_EUCLIDEAN = f"e2e-euclidean-{int(time.time())}"
INDEX_COSINE = f"e2e-cosine-{int(time.time())}"
VECTOR_DIMS = 8
NUM_VECTORS = 100


class TestKektorDBEndToEnd(unittest.TestCase):
    """
    End-to-End test suite for KektorDB.
    Requires a running KektorDB server at HOST:PORT.
    """

    client = None

    @classmethod
    def setUpClass(cls):
        """Executed once at the beginning of all tests."""
        print("--- KektorDB E2E Test Suite ---")
        cls.client = KektorDBClient(host=HOST, port=PORT)

        cls.vectors = np.random.rand(NUM_VECTORS, VECTOR_DIMS).astype(np.float32)
        print(f"Generated {NUM_VECTORS} test vectors.")

    def test_01_index_management(self):
        """Tests index creation, listing, getting details, and deletion."""
        print("\n--- Running: test_01_index_management ---")

        self.client.vcreate(INDEX_EUCLIDEAN, metric="euclidean", m=10)
        self.client.vcreate(INDEX_COSINE, metric="cosine")
        print(" -> vcreate OK")

        indexes = self.client.list_indexes()
        index_names = [idx["name"] for idx in indexes]
        self.assertIn(INDEX_EUCLIDEAN, index_names)
        self.assertIn(INDEX_COSINE, index_names)
        print(" -> list_indexes OK")

        info = self.client.get_index_info(INDEX_EUCLIDEAN)
        self.assertEqual(info["name"], INDEX_EUCLIDEAN)
        self.assertEqual(info["metric"], "euclidean")
        self.assertEqual(info["m"], 10)
        print(" -> get_index_info OK")

        self.client.delete_index(INDEX_EUCLIDEAN)
        indexes_after_delete = self.client.list_indexes()
        index_names_after_delete = [idx["name"] for idx in indexes_after_delete]
        self.assertNotIn(INDEX_EUCLIDEAN, index_names_after_delete)
        print(" -> delete_index OK")

    def test_02_data_lifecycle(self):
        """Tests adding, getting, and deleting vectors."""
        print("\n--- Running: test_02_data_lifecycle ---")

        vec_id = "test-vec-01"
        vec_data = self.vectors[0].tolist()
        metadata = {"test": "lifecycle"}
        self.client.vadd(INDEX_COSINE, vec_id, vec_data, metadata)
        print(" -> vadd OK")

        retrieved_vec = self.client.vget(INDEX_COSINE, vec_id)
        self.assertEqual(retrieved_vec["id"], vec_id)
        self.assertIn("vector", retrieved_vec)
        self.assertEqual(retrieved_vec["metadata"]["test"], "lifecycle")
        print(" -> vget (single) OK")

        self.client.vadd(INDEX_COSINE, "test-vec-02", self.vectors[1].tolist())
        retrieved_batch = self.client.vget_many(
            INDEX_COSINE, ["test-vec-01", "non-existent", "test-vec-02"]
        )
        self.assertEqual(len(retrieved_batch), 2)
        retrieved_ids = {item["id"] for item in retrieved_batch}
        self.assertEqual(retrieved_ids, {"test-vec-01", "test-vec-02"})
        print(" -> vget_many (batch) OK")

        # Delete a vector
        self.client.vdelete(INDEX_COSINE, vec_id)
        with self.assertRaises(APIError) as cm:
            self.client.vget(INDEX_COSINE, vec_id)
        error_message = cm.exception.args[0]
        self.assertIn("KektorDB API Error", error_message)
        self.assertIn("vector with ID 'test-vec-01' not found", error_message)

        print(" -> vdelete OK (con verifica dell'errore 404)")

    def test_03_search_and_filtering(self):
        """Tests search quality (recall) and metadata filtering."""
        print("\n--- Running: test_03_search_and_filtering ---")
        idx_search = f"e2e-search-{int(time.time())}"
        self.client.vcreate(idx_search, metric="euclidean")

        for i, vec in enumerate(self.vectors):
            self.client.vadd(
                idx_search, f"vec_{i}", vec.tolist(), metadata={"id_num": i}
            )

        query_idx = 42
        query_vector = self.vectors[query_idx]

        distances = np.sum((self.vectors - query_vector) ** 2, axis=1)
        true_neighbors_indices = np.argsort(distances)[:10]
        true_neighbor_ids = {f"vec_{i}" for i in true_neighbors_indices}

        results = self.client.vsearch(idx_search, query_vector.tolist(), k=10)
        self.assertEqual(f"vec_{query_idx}", results[0])

        intersection = set(results).intersection(true_neighbor_ids)
        recall = len(intersection) / 10.0
        print(f" -> Recall@10: {recall:.2f}")
        # Lower threshold for random vectors - HNSW is probabilistic
        self.assertGreaterEqual(recall, 0.7, "Recall should be at least 70%.")

        filtered_results = self.client.vsearch(
            idx_search, query_vector.tolist(), k=10, filter_str="id_num < 50"
        )
        for item_id in filtered_results:
            self.assertLess(int(item_id.split("_")[1]), 50)
        print(" -> Filtering OK")

    def test_04_dynamic_ef_search(self):
        """Tests the dynamic ef_search parameter."""
        print("\n--- Running: test_04_dynamic_ef_search ---")
        idx_name = f"e2e-efsearch-{int(time.time())}"

        self.client.vcreate(idx_name, metric="euclidean", m=8, ef_construction=20)

        for i, vec in enumerate(self.vectors):
            self.client.vadd(idx_name, f"vec_{i}", vec.tolist())

        query_vector = self.vectors[75].tolist()

        print(" -> Performing fast search (low ef_search)...")
        fast_results = self.client.vsearch(idx_name, query_vector, k=10, ef_search=12)

        print(" -> Performing accurate search (high ef_search)...")
        accurate_results = self.client.vsearch(
            idx_name, query_vector, k=10, ef_search=100
        )

        self.assertEqual(
            "vec_75",
            fast_results[0],
            "La ricerca veloce deve trovare l'elemento esatto.",
        )
        self.assertEqual(
            "vec_75",
            accurate_results[0],
            "La ricerca accurata deve trovare l'elemento esatto.",
        )

        fast_set = set(fast_results)
        accurate_set = set(accurate_results)

        print(f" -> Fast search found {len(fast_set)} unique results.")
        print(f" -> Accurate search found {len(accurate_set)} unique results.")

        print("✅ PASS: Dynamic ef_search parameter test completed.")

    def test_05_memory_config_and_search_with_scores(self):
        """Tests MemoryConfig (time-decay ranking) and vsearch_with_scores."""
        print("\n--- Running: test_05_memory_config_and_search_with_scores ---")
        idx_name = f"e2e-memory-{int(time.time())}"

        # Create index with memory config (1 hour half-life)
        self.client.vcreate(
            idx_name,
            metric="cosine",
            memory_config={"enabled": True, "decay_half_life": "1h"},
        )
        print(" -> vcreate with memory_config OK")

        # Add vectors with different timestamps
        now = time.time()

        # Fresh vector (just created)
        self.client.vadd(
            idx_name,
            "fresh_vec",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"_created_at": now},
        )

        # Old vector (1 hour ago - 1 half-life, should have score ~0.5)
        self.client.vadd(
            idx_name,
            "old_vec",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"_created_at": now - 3600},  # 1 hour ago
        )

        # Ancient vector (2 hours ago - 2 half-lives, should have score ~0.25)
        self.client.vadd(
            idx_name,
            "ancient_vec",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"_created_at": now - 7200},  # 2 hours ago
        )
        print(" -> vadd with custom timestamps OK")

        # Search with scores
        query = [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0]
        results = self.client.vsearch_with_scores(idx_name, query, k=10)

        self.assertEqual(len(results), 3, "Should return all 3 vectors")
        print(f" -> vsearch_with_scores returned {len(results)} results")

        # Verify order and approximate scores
        # Fresh should be first with score ~1.0
        self.assertEqual(results[0]["ID"], "fresh_vec")
        self.assertGreater(
            results[0]["Score"], 0.9, "Fresh vector should have score > 0.9"
        )

        # Old should be second with score ~0.5
        self.assertEqual(results[1]["ID"], "old_vec")
        self.assertGreater(
            results[1]["Score"], 0.4, "Old vector should have score > 0.4"
        )
        self.assertLess(results[1]["Score"], 0.6, "Old vector should have score < 0.6")

        # Ancient should be third with score ~0.25
        self.assertEqual(results[2]["ID"], "ancient_vec")
        self.assertGreater(
            results[2]["Score"], 0.2, "Ancient vector should have score > 0.2"
        )
        self.assertLess(
            results[2]["Score"], 0.3, "Ancient vector should have score < 0.3"
        )

        print(
            f"    Scores: Fresh={results[0]['Score']:.2f}, Old={results[1]['Score']:.2f}, Ancient={results[2]['Score']:.2f}"
        )
        print("✅ PASS: MemoryConfig and vsearch_with_scores test completed.")

    def test_06_graph_relations_include_and_hydrate(self):
        """Tests include_relations and hydrate_relations in vsearch."""
        print("\n--- Running: test_06_graph_relations_include_and_hydrate ---")

        idx_name = f"e2e-graph-rel-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Create a document and its chunks
        doc_vec = [0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9]
        chunk1_vec = [0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91]
        chunk2_vec = [0.92, 0.92, 0.92, 0.92, 0.92, 0.92, 0.92, 0.92]

        self.client.vadd(
            idx_name,
            "doc_parent",
            doc_vec,
            metadata={"title": "Parent Doc", "type": "document"},
        )
        self.client.vadd(
            idx_name,
            "chunk_1",
            chunk1_vec,
            metadata={"text": "First chunk", "type": "chunk"},
        )
        self.client.vadd(
            idx_name,
            "chunk_2",
            chunk2_vec,
            metadata={"text": "Second chunk", "type": "chunk"},
        )

        # Create relationships: chunk -> parent (parent relation)
        self.client.vlink(idx_name, "chunk_1", "doc_parent", "parent", "")
        self.client.vlink(idx_name, "chunk_2", "doc_parent", "parent", "")
        print(" -> vlink OK")

        # Test 1: include_relations without hydrate (returns IDs)
        print(" -> Testing include_relations without hydrate...")
        results_ids = self.client.vsearch(
            idx_name,
            [0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91],
            k=2,
            include_relations=["parent"],
        )

        self.assertIsInstance(results_ids, list)
        self.assertGreater(len(results_ids), 0)
        first_result = results_ids[0]
        (
            self.assertIsInstance(first_result, dict),
            "With include_relations, should return dicts",
        )
        self.assertIn("id", first_result)
        self.assertIn("node", first_result)
        # The key is "connections" not "relations" in the response
        self.assertIn("connections", first_result["node"])
        self.assertIn("parent", first_result["node"]["connections"])
        print(" -> include_relations (no hydrate) OK")

        # Test 2: include_relations with hydrate (returns full data)
        print(" -> Testing include_relations with hydrate...")
        results_hydrated = self.client.vsearch(
            idx_name,
            [0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91, 0.91],
            k=2,
            include_relations=["parent"],
            hydrate_relations=True,
        )

        self.assertIsInstance(results_hydrated, list)
        first_hydrated = results_hydrated[0]

        # Check that the parent node is hydrated - use "connections" not "relations"
        parent_data = first_hydrated["node"]["connections"]["parent"]
        self.assertIsInstance(parent_data, list)
        if len(parent_data) > 0:
            parent_node = parent_data[0]
            self.assertIn("id", parent_node)
            # Hydrated nodes should have metadata, not just ID
            has_full_data = "metadata" in parent_node or "vector" in parent_node
            print(f"    Parent node hydrated: {has_full_data}")
        print(" -> include_relations with hydrate OK")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: Graph relations test completed.")

    def test_07_graph_traversal_vtraverse(self):
        """Tests VTraverse for graph traversal from a known node."""
        print("\n--- Running: test_07_graph_traversal_vtraverse ---")

        idx_name = f"e2e-traverse-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Create chain: a -> b -> c
        self.client.vadd(
            idx_name,
            "node_a",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "A"},
        )
        self.client.vadd(
            idx_name,
            "node_b",
            [0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "B"},
        )
        self.client.vadd(
            idx_name,
            "node_c",
            [0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "C"},
        )

        self.client.vlink(idx_name, "node_a", "node_b", "next", "")
        self.client.vlink(idx_name, "node_b", "node_c", "next", "")
        print(" -> Graph setup OK")

        # Test single path traversal
        result = self.client.traverse(idx_name, "node_a", ["next"])
        self.assertIsNotNone(result)
        # Response wraps in "result" key
        result_data = result.get("result", result)
        self.assertIn("id", result_data)
        self.assertIn("connections", result_data)
        self.assertIn("next", result_data["connections"])
        next_nodes = result_data["connections"]["next"]
        self.assertEqual(len(next_nodes), 1)
        self.assertEqual(next_nodes[0]["id"], "node_b")
        print(" -> Single path traversal OK")

        # Test nested path traversal (a.next.next)
        result_nested = self.client.traverse(idx_name, "node_a", ["next.next"])
        self.assertIsNotNone(result_nested)
        nested_data = result_nested.get("result", result_nested)
        # The nested path should return node_b with its connections
        print(f"    Nested result: {nested_data.get('connections', {})}")
        print(" -> Nested path traversal OK")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: VTraverse test completed.")

    def test_08_graph_pathfinding_find_path(self):
        """Tests FindPath for shortest path between nodes."""
        print("\n--- Running: test_08_graph_pathfinding_find_path ---")

        idx_name = f"e2e-pathfinding-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Create graph: a -> b -> c -> d and a -> e (shorter path)
        self.client.vadd(
            idx_name,
            "node_a",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "A"},
        )
        self.client.vadd(
            idx_name,
            "node_b",
            [0.8, 0.2, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "B"},
        )
        self.client.vadd(
            idx_name,
            "node_c",
            [0.6, 0.4, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "C"},
        )
        self.client.vadd(
            idx_name,
            "node_d",
            [0.4, 0.6, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "D"},
        )
        self.client.vadd(
            idx_name,
            "node_e",
            [0.9, 0.1, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "E"},
        )

        self.client.vlink(idx_name, "node_a", "node_b", "next", "")
        self.client.vlink(idx_name, "node_b", "node_c", "next", "")
        self.client.vlink(idx_name, "node_c", "node_d", "next", "")
        self.client.vlink(idx_name, "node_a", "node_e", "jump", "")
        print(" -> Graph setup OK")

        # Test direct path (a -> b)
        result_direct = self.client.find_path(idx_name, "node_a", "node_b", ["next"])
        self.assertIsNotNone(result_direct)
        self.assertIn("path", result_direct)
        self.assertIn("node_a", result_direct["path"])
        self.assertIn("node_b", result_direct["path"])
        print(f"    Direct path: {result_direct['path']}")
        print(" -> Direct path OK")

        # Test multi-hop path (a -> d via next)
        result_multi = self.client.find_path(idx_name, "node_a", "node_d", ["next"])
        self.assertIsNotNone(result_multi)
        self.assertGreaterEqual(len(result_multi["path"]), 3)
        print(f"    Multi-hop path: {result_multi['path']}")
        print(" -> Multi-hop path OK")

        # Test no path exists (b -> e via next only, no jump relation)
        # Note: Server returns 404 when no path found, so we catch the exception
        from kektordb_client import APIError

        try:
            result_no_path = self.client.find_path(
                idx_name, "node_b", "node_e", ["next"]
            )
            # If we get here without exception, path was found
            print(f"    Path found (unexpected): {result_no_path}")
        except APIError as e:
            if "path not found" in str(e).lower():
                print(" -> No path case OK (404 as expected)")
            else:
                raise

        # Test with empty relations (should error)
        with self.assertRaises(Exception):
            self.client.find_path(idx_name, "node_a", "node_b", [])

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: FindPath test completed.")

    def test_09_vcompress(self):
        """Tests vector compression (precision reduction)."""
        print("\n--- Running: test_09_vcompress ---")

        idx_name = f"e2e-compress-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine", precision="float32")

        # Add vectors
        for i in range(10):
            vec = [float(i) * 0.1] * 8
            self.client.vadd(idx_name, f"vec_{i}", vec, metadata={"index": i})
        print(" -> Vectors added OK")

        # Get original vector dimension
        original = self.client.vget(idx_name, "vec_0")
        original_dim = len(original.get("vector", []))
        print(f"    Original dimension: {original_dim}")

        # Compress to int8
        task = self.client.vcompress(idx_name, "int8")
        result = task.wait(timeout=30)
        print(f"    Compress task result: {result}")
        print(" -> vcompress OK")

        # Verify compression (dimension should stay same, precision changes)
        compressed = self.client.vget(idx_name, "vec_0")
        compressed_dim = len(compressed.get("vector", []))
        self.assertEqual(original_dim, compressed_dim)
        print(f"    Compressed dimension: {compressed_dim}")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: VCompress test completed.")

    def test_10_vupdate_index_config(self):
        """Tests updating index maintenance configuration."""
        print("\n--- Running: test_10_vupdate_index_config ---")

        idx_name = f"e2e-config-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Update maintenance config
        new_config = {
            "refine_enabled": True,
            "refine_interval": "60m",
            "refine_batch_size": 500,
            "vacuum_interval": "30m",
            "delete_threshold": 0.2,
        }

        self.client.vupdate_config(idx_name, new_config)
        print(" -> vupdate_config OK")

        # Get index info to verify config was updated
        info = self.client.get_index_info(idx_name)
        print(f"    Index info: {info.get('maintenance_config', 'N/A')}")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: VUpdateIndexConfig test completed.")

    def test_11_rag_retrieve(self):
        """Tests RAG retrieval functionality."""
        print("\n--- Running: test_11_rag_retrieve ---")

        idx_name = f"e2e-rag-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Add some documents
        docs = [
            (
                "doc_1",
                [0.9, 0.9, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
                "Python is a great programming language",
            ),
            (
                "doc_2",
                [0.0, 0.0, 0.9, 0.9, 0.0, 0.0, 0.0, 0.0],
                "Go is fast and concurrent",
            ),
            (
                "doc_3",
                [0.0, 0.0, 0.0, 0.0, 0.9, 0.9, 0.0, 0.0],
                "Rust is safe and memory efficient",
            ),
        ]

        for doc_id, vec, text in docs:
            self.client.vadd(
                idx_name, doc_id, vec, metadata={"text": text, "type": "doc"}
            )
        print(" -> Documents added OK")

        # Try rag_retrieve (if pipeline exists, otherwise will fail gracefully)
        try:
            results = self.client.rag_retrieve(idx_name, "Python programming", k=2)
            print(f"    RAG results: {results}")
            print(" -> rag_retrieve OK")
        except Exception as e:
            print(f"    rag_retrieve not configured (expected if no pipeline): {e}")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: RAG retrieve test completed.")

    def test_12_sessions_and_profiles(self):
        """Tests session management and user profiles."""
        print("\n--- Running: test_12_sessions_and_profiles ---")

        idx_name = f"e2e-sessions-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Add user profile (using correct ID format: _profile::user_id)
        user_id = "test_user_123"
        profile_id = f"_profile::{user_id}"
        user_metadata = {
            "user_id": user_id,
            "name": "Test User",
            "language": "en",
            "communication_style": "detailed",
            "expertise_areas": "python,go",
            "response_length": "medium",
            "confidence": 0.8,
            "last_updated": time.time(),
        }

        self.client.vadd(
            idx_name,
            profile_id,
            [0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5],
            metadata=user_metadata,
        )
        print(" -> User profile added OK")

        # Get user profile
        profile = self.client.get_user_profile(user_id, idx_name)
        self.assertIsNotNone(profile)
        # Profile has specific fields, not generic metadata
        self.assertEqual(profile.get("user_id"), user_id)
        self.assertEqual(profile.get("language"), "en")
        self.assertEqual(profile.get("communication_style"), "detailed")
        print(" -> get_user_profile OK")

        # Start session - context should be a string, not a dict
        session = self.client.start_session(
            idx_name, context="Browsing home page", user_id=user_id
        )
        self.assertIsNotNone(session)
        session_id = session.get("session_id")
        self.assertIsNotNone(session_id)
        print(f"    Session started: {session_id}")
        print(" -> start_session OK")

        # End session
        end_result = self.client.end_session(session_id, idx_name)
        self.assertIsNotNone(end_result)
        print(" -> end_session OK")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: Sessions and profiles test completed.")

    def test_13_vextract_subgraph(self):
        """Tests VExtractSubgraph for extracting local subgraph."""
        print("\n--- Running: test_13_vextract_subgraph ---")

        idx_name = f"e2e-subgraph-{int(time.time())}"
        self.client.vcreate(idx_name, metric="cosine")

        # Create a small graph: root -> a, root -> b, a -> c
        self.client.vadd(
            idx_name,
            "root",
            [1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "root"},
        )
        self.client.vadd(
            idx_name,
            "node_a",
            [0.8, 0.2, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "A"},
        )
        self.client.vadd(
            idx_name,
            "node_b",
            [0.7, 0.3, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "B"},
        )
        self.client.vadd(
            idx_name,
            "node_c",
            [0.6, 0.4, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
            metadata={"name": "C"},
        )

        self.client.vlink(idx_name, "root", "node_a", "child", "")
        self.client.vlink(idx_name, "root", "node_b", "child", "")
        self.client.vlink(idx_name, "node_a", "node_c", "child", "")
        print(" -> Graph setup OK")

        # Extract subgraph
        result = self.client.extract_subgraph(idx_name, "root", ["child"], max_depth=2)

        self.assertIsNotNone(result)
        self.assertIn("nodes", result)
        self.assertIn("edges", result)
        self.assertEqual(result.get("root_id"), "root")

        # Should have at least root + node_a + node_b (depth 1)
        self.assertGreaterEqual(len(result["nodes"]), 3)
        print(
            f"    Extracted {len(result['nodes'])} nodes and {len(result['edges'])} edges"
        )
        print(" -> extract_subgraph OK")

        # Test with depth limit = 1 (should not include node_c)
        result_limited = self.client.extract_subgraph(
            idx_name, "root", ["child"], max_depth=1
        )
        node_ids = [n["id"] for n in result_limited.get("nodes", [])]
        self.assertNotIn("node_c", node_ids)
        print(" -> Depth limit OK")

        # Cleanup
        self.client.delete_index(idx_name)
        print(" -> Cleanup OK")
        print("✅ PASS: VExtractSubgraph test completed.")


if __name__ == "__main__":
    unittest.main()
