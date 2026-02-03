import unittest
import time
import numpy as np
from kektordb_client import KektorDBClient

HOST = "localhost"
PORT = 9091
INDEX_NAME = f"autolink-test-{int(time.time())}"

class TestAutoLinking(unittest.TestCase):
    client = None

    @classmethod
    def setUpClass(cls):
        print("--- Auto-Linking Test Suite ---")
        cls.client = KektorDBClient(host=HOST, port=PORT)
        
        # Define Auto-Link Rules
        # When a node has "parent_id" metadata, link it to the node with that ID via "child_of"
        auto_links = [
            {
                "metadata_field": "parent_id",
                "relation_type": "child_of",
                "create_node": True
            }
        ]

        print(f"Creating index {INDEX_NAME} with auto_links rules...")
        cls.client.vcreate(INDEX_NAME, metric="cosine", auto_links=auto_links)

    @classmethod
    def tearDownClass(cls):
        print(f"\nDeleting index {INDEX_NAME}...")
        try:
            cls.client.delete_index(INDEX_NAME)
        except Exception as e:
            print(f"Error cleaning up: {e}")

    def test_auto_linking_logic(self):
        print("\n--- Running: test_auto_linking_logic ---")
        
        # 1. Add Parent Node (P1)
        # Note: Ideally P1 should exist, but VLink is best-effort. 
        # Here we add it explicitly to ensure it's a valid vector node.
        p1_vector = np.random.rand(8).tolist()
        self.client.vadd(INDEX_NAME, "P1", p1_vector, {"type": "parent"})
        print(" -> Added Parent P1")

        # 2. Add Child Node (C1) with metadata pointing to P1
        c1_vector = np.random.rand(8).tolist()
        c1_meta = {
            "type": "child",
            "parent_id": "P1"  # This should trigger the auto-link
        }
        self.client.vadd(INDEX_NAME, "C1", c1_vector, c1_meta)
        print(" -> Added Child C1 with parent_id=P1")

        # Allow async processing if any (though VLink in VAdd is usually synchronous)
        time.sleep(0.5)

        # 3. Verify Forward Link (C1 -> child_of -> P1)
        links = self.client.vget_links("C1", "child_of")
        print(f" -> C1 links (child_of): {links}")
        self.assertIn("P1", links, "Auto-linking failed: C1 should be linked to P1 via 'child_of'")

        # 4. Verify Reverse Link/Incoming (P1 <- child_of <- C1)
        # Using the new get_incoming method
        incoming = self.client.get_incoming("P1", "child_of")
        print(f" -> P1 incoming (child_of): {incoming}")
        self.assertIn("C1", incoming, "Reverse link failed: P1 should have incoming 'child_of' from C1")

if __name__ == '__main__':
    unittest.main()
