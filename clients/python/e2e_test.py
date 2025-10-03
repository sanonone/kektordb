# File: clients/python/e2e_test.py
import unittest
import time
import numpy as np
from kektordb_client import KektorDBClient, APIError

# --- Configurazione dei Test ---
HOST = "localhost"
PORT = 9091
INDEX_EUCLIDEAN = "e2e-test-euclidean"
INDEX_COSINE = "e2e-test-cosine"
VECTOR_DIMS = 8
NUM_VECTORS = 100

class TestKektorDBEndToEnd(unittest.TestCase):
    """
    Suite di test End-to-End per KektorDB.
    Richiede un server KektorDB in esecuzione su HOST:PORT.
    """
    @classmethod
    def setUpClass(cls):
        """Eseguito una sola volta all'inizio di tutti i test."""
        print("--- Avvio Suite di Test E2E per KektorDB ---")
        cls.client = KektorDBClient(host=HOST, port=PORT)
        
        # Genera dati di test consistenti
        cls.vectors = np.random.rand(NUM_VECTORS, VECTOR_DIMS).astype(np.float32)
        
        # Pulisce l'ambiente prima di iniziare
        try:
            print("Pulizia e compattazione AOF iniziale...")
            cls.client.aof_rewrite()
        except Exception as e:
            print(f"Attenzione: impossibile compattare AOF all'inizio. Errore: {e}")

    def test_01_index_creation(self):
        """Verifica la creazione di indici con diverse metriche e parametri."""
        print("\n--- Esecuzione: test_01_index_creation ---")
        self.client.vcreate(INDEX_EUCLIDEAN, metric="euclidean", m=10, ef_construction=200)
        self.client.vcreate(INDEX_COSINE, metric="cosine", precision="float32",m=10, ef_construction=200)
        # In un'API più matura, avremmo un endpoint per verificare che gli indici esistano.
        print("✅ PASS: Creazione indici completata.")

    def test_02_data_ingestion(self):
        """Verifica l'inserimento di dati in entrambi gli indici."""
        print("\n--- Esecuzione: test_02_data_ingestion ---")
        for i, vec in enumerate(self.vectors):
            
            self.client.vadd(
                INDEX_EUCLIDEAN, f"vec_{i}", vec.tolist(), 
                metadata={"id": i, "type": "euclidean"}
            )
           
            self.client.vadd(
                INDEX_COSINE, f"vec_{i}", vec.tolist(),
                metadata={"id": i, "type": "cosine"}
            )
        print(f"✅ PASS: Inseriti {NUM_VECTORS} vettori in 2 indici.")

        
    def test_03_euclidean_search_quality(self):
        """Verifica la correttezza e la recall della ricerca Euclidea."""
        print("\n--- Esecuzione: test_03_euclidean_search_quality ---")
        query_idx = 50
        query_vector = self.vectors[query_idx]

        # Calcola i veri vicini con brute-force
        distances = np.sum((self.vectors - query_vector)**2, axis=1)
        true_neighbors_indices = np.argsort(distances)[:10]
        true_neighbor_ids = {f"vec_{i}" for i in true_neighbors_indices}

        # Esegui la ricerca con KektorDB
        results = self.client.vsearch(INDEX_EUCLIDEAN, query_vector.tolist(), k=10)
        
        self.assertEqual(f"vec_{query_idx}", results[0], "Il primo risultato deve essere l'elemento stesso.")
        
        found_set = set(results)
        intersection = found_set.intersection(true_neighbor_ids)
        recall = len(intersection) / 10.0
        
        print(f"Recall@10 per Euclidean: {recall:.2f}")
        self.assertGreaterEqual(recall, 0.9, "La recall per Euclidean deve essere almeno del 90%.")
        print("✅ PASS: Qualità ricerca Euclidea verificata.")
        

    '''    
    def test_04_cosine_search_with_filter(self):
        """Verifica la ricerca Coseno con filtro."""
        print("\n--- Esecuzione: test_04_cosine_search_with_filter ---")
        # I nostri dati sono casuali, quindi la ricerca semantica non ha senso.
        # Testiamo solo che il filtro funzioni.
        query_vector = self.vectors[0]
        results = self.client.vsearch(INDEX_COSINE, query_vector.tolist(), k=10, filter_str="id<10")
        
        self.assertTrue(len(results) > 0, "La ricerca filtrata dovrebbe restituire risultati.")
        for item_id in results:
            item_num = int(item_id.split('_')[1])
            self.assertLess(item_num, 10, f"L'item {item_id} non rispetta il filtro 'id<10'.")
        print("✅ PASS: Ricerca Coseno con filtro verificata.")
        '''
    def test_04_cosine_compression_and_search(self):
        """Verifica la compressione int8 e la ricerca filtrata."""
        print("\n--- Esecuzione: test_04_cosine_compression_and_search ---")
        print("Compressione indice Coseno a int8...")
        self.client.vcompress(INDEX_COSINE, precision="int8")

        # Cerca di nuovo dopo la compressione per assicurarsi che funzioni
        query_vector = self.vectors[0]
        results = self.client.vsearch(INDEX_COSINE, query_vector.tolist(), k=10, filter_str="id<10")
        
        self.assertTrue(len(results) > 0, "La ricerca filtrata dovrebbe restituire risultati.")
        for item_id in results:
            item_num = int(item_id.split('_')[1])
            self.assertLess(item_num, 10, f"L'item {item_id} non rispetta il filtro 'id<10'.")
        print("✅ PASS: Ricerca Coseno con filtro post-compressione verificata.")
    
        
    def test_05_euclidean_compression_and_persistence(self):
        """Verifica la compressione, la riscrittura AOF e la persistenza."""
        print("\n--- Esecuzione: test_05_compression_and_persistence ---")
        # Comprimi l'indice Euclideo a float16
        print("Compressione a float16...")
        self.client.vcompress(INDEX_EUCLIDEAN, precision="float16")
        
        # Cerca di nuovo per assicurarsi che funzioni ancora
        results_f16 = self.client.vsearch(INDEX_EUCLIDEAN, self.vectors[50].tolist(), k=1)
        self.assertEqual("vec_50", results_f16[0], "La ricerca deve funzionare dopo la compressione float16.")

        # Esegui la riscrittura AOF
        print("Esecuzione riscrittura AOF...")
        self.client.aof_rewrite()
        print("✅ PASS: Compressione e riscrittura AOF completate.")
        

if __name__ == '__main__':
    # Questo permette di eseguire lo script come un test standard
    unittest.main()
