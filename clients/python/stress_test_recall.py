# File: clients/python/stress_test_recall.py
import unittest
import time
import numpy as np
from kektordb_client import KektorDBClient

# --- PARAMETRI DELLO STRESS TEST ---
HOST = "localhost"
PORT = 9091
INDEX_NAME = f"stress-test-{int(time.time())}"
NUM_VECTORS = 10000  # Aumentato drasticamente
VECTOR_DIMS = 64    # Aumentato
K_SEARCH = 10       # Vicini da cercare

# Parametri HNSW "difficili"
M = 16               # Meno connessioni
EF_CONSTRUCTION = 200 # Meno candidati durante la costruzione

class TestHNSWQuality(unittest.TestCase):
    client = None
    vectors = None

    @classmethod
    def setUpClass(cls):
        print("--- HNSW Quality Stress Test ---")
        print(f"Parametri: {NUM_VECTORS} vettori, {VECTOR_DIMS} dimensioni, M={M}, efC={EF_CONSTRUCTION}")
        
        cls.client = KektorDBClient(host=HOST, port=PORT)
        
        # Genera dati di test
        print("Generating test data...")
        cls.vectors = np.random.rand(NUM_VECTORS, VECTOR_DIMS).astype(np.float32)

        # Crea e popola l'indice
        print(f"Creating and populating index '{INDEX_NAME}'...")
        start_time = time.time()
        try:
            cls.client.vcreate(INDEX_NAME, metric="euclidean", m=M, ef_construction=EF_CONSTRUCTION)
            for i, vec in enumerate(cls.vectors):
                cls.client.vadd(INDEX_NAME, f"vec_{i}", vec.tolist())
        except Exception as e:
            cls.fail(f"Setup failed during data ingestion: {e}")
        
        duration = time.time() - start_time
        print(f"Indexing completed in {duration:.2f} seconds.")

    @classmethod
    def tearDownClass(cls):
        """Pulisce l'indice alla fine del test."""
        print(f"Cleaning up index '{INDEX_NAME}'...")
        try:
            cls.client.delete_index(INDEX_NAME)
        except Exception as e:
            print(f"Could not clean up index: {e}")

    def test_recall_quality(self):
        """Misura la recall media su un campione di query."""
        print("\n--- Running: test_recall_quality ---")
        
        num_queries = 20 # Eseguiamo il test su 20 query casuali per avere una media
        total_recall = 0.0
        
        print(f"Performing {num_queries} random searches to calculate average recall...")
        for i in range(num_queries):
            query_idx = np.random.randint(0, NUM_VECTORS)
            query_vector = self.vectors[query_idx]

            # Calcola i veri vicini
            distances = np.sum((self.vectors - query_vector)**2, axis=1)
            true_neighbors_indices = np.argsort(distances)[:K_SEARCH]
            true_neighbor_ids = {f"vec_{i}" for i in true_neighbors_indices}
            
            # Cerca con KektorDB
            results = self.client.vsearch(INDEX_NAME, query_vector.tolist(), k=K_SEARCH)
            
            # Calcola la recall per questa query
            intersection = set(results).intersection(true_neighbor_ids)
            recall = len(intersection) / float(K_SEARCH)
            total_recall += recall

        average_recall = total_recall / num_queries
        
        print(f"\n--- Stress Test Results ---")
        print(f"Average Recall@{K_SEARCH} (over {num_queries} queries): {average_recall:.4f}")
        
        # Ci aspettiamo una recall alta, ma non perfetta, in queste condizioni difficili.
        self.assertGreaterEqual(average_recall, 0.95, f"Average recall {average_recall:.4f} is below the 95% threshold.")


if __name__ == '__main__':
    unittest.main()
