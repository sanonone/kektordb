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
        self.assertGreaterEqual(recall, 0.9, "Recall should be at least 90%.")

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


if __name__ == "__main__":
    unittest.main()
