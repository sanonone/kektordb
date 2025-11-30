import unittest
import argparse
import numpy as np
from kektordb_client import KektorDBClient, APIError

# --- CLI ARGUMENT PARSING ---
# Parse CLI args immediately at script start.
parser = argparse.ArgumentParser(description="Consistency test for KektorDB.")
parser.add_argument(
    '--mode',
    type=str,
    default='setup',
    choices=['setup', 'verify'],
    help="Execution mode: 'setup' to populate and verify, 'verify' to verify only."
)
# Use parse_known_args() to avoid conflicts with unittest args
args, _ = parser.parse_known_args()
mode = args.mode
# --- END PARSING ---

# --- Global Test Data Config ---
# Fixed names to ensure persistence across runs.
# NOTE: Delete kektordb.aof and .kdb before the first run.
HOST = "localhost"
PORT = 9091
INDEX_EUCLIDEAN_F32 = "consistency-euclidean-f32"
INDEX_EUCLIDEAN_F16 = "consistency-euclidean-f16"
INDEX_COSINE_F32 = "consistency-cosine-f32"
INDEX_COSINE_I8 = "consistency-cosine-i8"

# Consistent test data
VECTORS = {
    "v1": [0.1, 0.2, 0.3],
    "v2": [0.4, 0.5, 0.6],
    "v3": [0.7, 0.8, 0.9],
    "v_deleted": [9.9, 9.9, 9.9],
}
METADATA = {
    "v1": {"tag": "A", "val": 100},
    "v2": {"tag": "B", "val": 200},
    "v3": {"tag": "A", "val": 300},
}
KV_DATA = {
    "key1": "value1",
    "key2": "value2_updated",
}

class TestKektorDBConsistency(unittest.TestCase):
    """
    Test suite for KektorDB data consistency.
    Supports 'setup' and 'verify' modes.
    """
    client = KektorDBClient(host=HOST, port=PORT)
    
    # --- Verification Tests (Always executed) ---
    
    def test_verify_kv_store(self):
        """Verifies correctness of data in the KV store."""
        print("\n--- VERIFY: Key-Value Store ---")
        self.assertEqual(self.client.get("key1"), KV_DATA["key1"])
        self.assertEqual(self.client.get("key2"), KV_DATA["key2"])
        
        with self.assertRaises(APIError):
            self.client.get("key_deleted")
        print("✅ KV data correct.")

    def test_verify_indexes(self):
        """Verifies index configurations."""
        print("\n--- VERIFY: Index Config ---")
        indexes = {idx['name']: idx for idx in self.client.list_indexes()}
        self.assertIn(INDEX_EUCLIDEAN_F16, indexes)
        self.assertIn(INDEX_COSINE_I8, indexes)
        
        # Check specific index config
        info_f16 = indexes[INDEX_EUCLIDEAN_F16]
        self.assertEqual(info_f16['metric'], 'euclidean')
        self.assertEqual(info_f16['precision'], 'float16')
        self.assertEqual(info_f16['vector_count'], 3) # v1, v2, v3
        
        info_i8 = indexes[INDEX_COSINE_I8]
        self.assertEqual(info_i8['metric'], 'cosine')
        self.assertEqual(info_i8['precision'], 'int8')
        self.assertEqual(info_i8['vector_count'], 3)
        print("✅ Index config correct.")

    def test_verify_vector_data_and_search(self):
        """Verifies vector data and search accuracy."""
        print("\n--- VERIFY: Vector Data & Search ---")
        
        # Verify single retrieval (vget)
        retrieved = self.client.vget(INDEX_COSINE_I8, "v1")
        self.assertEqual(retrieved['metadata']['tag'], 'A')
        print(" -> vget OK.")
        
        # Verify batch retrieval (vget_many)
        batch = self.client.vget_many(INDEX_COSINE_I8, ["v3", "v1"])
        self.assertEqual(len(batch), 2)
        print(" -> vget_many OK.")

        # Verify deleted vector is gone
        with self.assertRaises(APIError):
            self.client.vget(INDEX_COSINE_I8, "v_deleted")
        print(" -> Deleted vector not found (correct).")

        # Verify filtered search
        results = self.client.vsearch(
            INDEX_COSINE_I8,
            query_vector=[0.1, 0.2, 0.3],
            k=5,
            filter_str="tag=A"
        )
        # Expecting v1 and v3, ordered by proximity. v1 should be first.
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0], "v1")
        self.assertIn("v3", results)
        print(" -> Filtered vsearch OK.")


@unittest.skipIf(mode != 'setup', "Running in 'verify-only' mode")
class TestKektorDBSetup(unittest.TestCase):
    """
    Test class for initial data setup.
    Runs only in 'setup' mode.
    """
    client = KektorDBClient(host=HOST, port=PORT)

    def test_full_setup(self):
        """Executes full creation and population process."""
        print("\n--- SETUP: Starting DB population ---")
        
        # --- KV ---
        print("Populating KV store...")
        self.client.set("key1", "value1")
        self.client.set("key2", "value2_old") # Will be overwritten
        self.client.set("key_deleted", "temp")
        self.client.set("key2", KV_DATA["key2"]) # Overwrite
        self.client.delete("key_deleted")
        
        # --- Vector Indexes (created as float32) ---
        print("Creating float32 indexes...")
        self.client.vcreate(INDEX_EUCLIDEAN_F32, metric="euclidean")
        self.client.vcreate(INDEX_EUCLIDEAN_F16, metric="euclidean")
        self.client.vcreate(INDEX_COSINE_F32, metric="cosine")
        self.client.vcreate(INDEX_COSINE_I8, metric="cosine")
        
        # --- Vector Population ---
        print("Populating indexes with vectors...")
        for index in [INDEX_EUCLIDEAN_F32, INDEX_EUCLIDEAN_F16, INDEX_COSINE_F32, INDEX_COSINE_I8]:
            for vid, vec in VECTORS.items():
                meta = METADATA.get(vid)
                self.client.vadd(index, vid, vec, meta)
                
        # --- Soft Delete ---
        print("Executing soft delete...")
        for index in [INDEX_EUCLIDEAN_F32, INDEX_EUCLIDEAN_F16, INDEX_COSINE_F32, INDEX_COSINE_I8]:
            self.client.vdelete(index, "v_deleted")
            
        # --- Compression ---
        print("Compressing indexes...")
        task_f16 = self.client.vcompress(INDEX_EUCLIDEAN_F16, precision="float16", wait=True)
        task_i8 = self.client.vcompress(INDEX_COSINE_I8, precision="int8", wait=True)
        
        print(f"Waiting for compression task {task_f16.id}...")
        task_f16.wait()
        self.assertEqual(task_f16.status, "completed")

        print(f"Waiting for compression task {task_i8.id}...")
        task_i8.wait()
        self.assertEqual(task_i8.status, "completed")
        
        # --- Snapshot & AOF Rewrite ---
        print("Executing SAVE (Snapshot)...")
        self.client.aof_rewrite()
        self.client.save() # Ensure this method exists in client!
        
        print("--- SETUP COMPLETE ---")


if __name__ == '__main__':

    # Create test suite
    suite = unittest.TestSuite()
    if mode == 'setup':
        print("Mode: SETUP (populate and verify)")
        suite.addTest(unittest.makeSuite(TestKektorDBSetup))
    else:
        print("Mode: VERIFY (verify existing data only)")
    
    suite.addTest(unittest.makeSuite(TestKektorDBConsistency))
    
    # Run tests
    import sys
    runner = unittest.TextTestRunner()
    result = runner.run(suite)

    # Exit with error code if tests fail (for CI)
    if not result.wasSuccessful():
        exit(1)
