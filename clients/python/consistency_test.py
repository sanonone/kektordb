# File: clients/python/consistency_test.py
import unittest
import argparse
import numpy as np
from kektordb_client import KektorDBClient, APIError

# --- NUOVA POSIZIONE DEL PARSING ---
# Parsa gli argomenti della riga di comando subito, all'inizio dello script.
parser = argparse.ArgumentParser(description="Test di consistenza per KektorDB.")
parser.add_argument(
    '--mode',
    type=str,
    default='setup',
    choices=['setup', 'verify'],
    help="Modalità di esecuzione: 'setup' per popolare e verificare, 'verify' per verificare soltanto."
)
# Usiamo parse_known_args() per evitare conflitti con gli argomenti di unittest
args, _ = parser.parse_known_args()
mode = args.mode
# --- FINE NUOVA POSIZIONE ---

# --- Configurazione Globale dei Dati di Test ---
# Usiamo nomi fissi per poterli ritrovare tra le esecuzioni
# NOTA: Assicurati di cancellare kektordb.aof e .kdb prima della prima esecuzione.
HOST = "localhost"
PORT = 9091
INDEX_EUCLIDEAN_F32 = "consistency-euclidean-f32"
INDEX_EUCLIDEAN_F16 = "consistency-euclidean-f16"
INDEX_COSINE_F32 = "consistency-cosine-f32"
INDEX_COSINE_I8 = "consistency-cosine-i8"

# Dati di test consistenti
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
    Suite di test per la consistenza dei dati di KektorDB,
    con modalità 'setup' e 'verify'.
    """
    client = KektorDBClient(host=HOST, port=PORT)
    
    # --- Test di Verifica (sempre eseguiti) ---
    
    def test_verify_kv_store(self):
        """Verifica che i dati nel KV store siano corretti."""
        print("\n--- VERIFICA: Key-Value Store ---")
        self.assertEqual(self.client.get("key1"), KV_DATA["key1"])
        self.assertEqual(self.client.get("key2"), KV_DATA["key2"])
        
        with self.assertRaises(APIError):
            self.client.get("key_deleted")
        print("✅ Dati KV corretti.")

    def test_verify_indexes(self):
        """Verifica la configurazione degli indici."""
        print("\n--- VERIFICA: Configurazione Indici ---")
        indexes = {idx['name']: idx for idx in self.client.list_indexes()}
        self.assertIn(INDEX_EUCLIDEAN_F16, indexes)
        self.assertIn(INDEX_COSINE_I8, indexes)
        
        # Controlla la configurazione di un indice
        info_f16 = indexes[INDEX_EUCLIDEAN_F16]
        self.assertEqual(info_f16['metric'], 'euclidean')
        self.assertEqual(info_f16['precision'], 'float16')
        self.assertEqual(info_f16['vector_count'], 3) # v1, v2, v3
        
        info_i8 = indexes[INDEX_COSINE_I8]
        self.assertEqual(info_i8['metric'], 'cosine')
        self.assertEqual(info_i8['precision'], 'int8')
        self.assertEqual(info_i8['vector_count'], 3)
        print("✅ Configurazione indici corretta.")

    def test_verify_vector_data_and_search(self):
        """Verifica i dati dei vettori e la correttezza della ricerca."""
        print("\n--- VERIFICA: Dati Vettoriali e Ricerca ---")
        
        # Verifica recupero singolo (vget)
        retrieved = self.client.vget(INDEX_COSINE_I8, "v1")
        self.assertEqual(retrieved['metadata']['tag'], 'A')
        print(" -> vget OK.")
        
        # Verifica recupero batch (vget_many)
        batch = self.client.vget_many(INDEX_COSINE_I8, ["v3", "v1"])
        self.assertEqual(len(batch), 2)
        print(" -> vget_many OK.")

        # Verifica che il vettore eliminato non sia recuperabile
        with self.assertRaises(APIError):
            self.client.vget(INDEX_COSINE_I8, "v_deleted")
        print(" -> Vettore eliminato non trovato (corretto).")

        # Verifica ricerca filtrata
        results = self.client.vsearch(
            INDEX_COSINE_I8,
            query_vector=[0.1, 0.2, 0.3],
            k=5,
            filter_str="tag=A"
        )
        # Ci aspettiamo v1 e v3, ordinati per vicinanza. v1 dovrebbe essere il primo.
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0], "v1")
        self.assertIn("v3", results)
        print(" -> vsearch con filtro OK.")


@unittest.skipIf(mode != 'setup', "Esecuzione in modalità 'verify-only'")
class TestKektorDBSetup(unittest.TestCase):
    """
    Classe di test per il setup iniziale dei dati.
    Viene eseguita solo in modalità 'setup'.
    """
    client = KektorDBClient(host=HOST, port=PORT)

    def test_full_setup(self):
        """Esegue l'intero processo di creazione e popolamento."""
        print("\n--- SETUP: Inizio popolamento del database ---")
        
        # --- KV ---
        print("Popolamento KV store...")
        self.client.set("key1", "value1")
        self.client.set("key2", "value2_old") # Sarà sovrascritto
        self.client.set("key_deleted", "temp")
        self.client.set("key2", KV_DATA["key2"]) # Sovrascrittura
        self.client.delete("key_deleted")
        
        # --- Indici Vettoriali (creati in float32) ---
        print("Creazione indici float32...")
        self.client.vcreate(INDEX_EUCLIDEAN_F32, metric="euclidean")
        self.client.vcreate(INDEX_EUCLIDEAN_F16, metric="euclidean")
        self.client.vcreate(INDEX_COSINE_F32, metric="cosine")
        self.client.vcreate(INDEX_COSINE_I8, metric="cosine")
        
        # --- Popolamento Vettori ---
        print("Popolamento indici con vettori...")
        for index in [INDEX_EUCLIDEAN_F32, INDEX_EUCLIDEAN_F16, INDEX_COSINE_F32, INDEX_COSINE_I8]:
            for vid, vec in VECTORS.items():
                meta = METADATA.get(vid)
                self.client.vadd(index, vid, vec, meta)
                
        # --- Eliminazione (soft delete) ---
        print("Esecuzione soft delete...")
        for index in [INDEX_EUCLIDEAN_F32, INDEX_EUCLIDEAN_F16, INDEX_COSINE_F32, INDEX_COSINE_I8]:
            self.client.vdelete(index, "v_deleted")
            
        # --- Compressione ---
        print("Compressione indici...")
        task_f16 = self.client.vcompress(INDEX_EUCLIDEAN_F16, precision="float16", wait=True)
        task_i8 = self.client.vcompress(INDEX_COSINE_I8, precision="int8", wait=True)
        
        print(f"Attesa completamento task di compressione {task_f16.id}...")
        task_f16.wait()
        self.assertEqual(task_f16.status, "completed")

        print(f"Attesa completamento task di compressione {task_i8.id}...")
        task_i8.wait()
        self.assertEqual(task_i8.status, "completed")
        
        # --- Snapshot e Compattazione AOF ---
        print("Esecuzione SAVE (Snapshot)...")
        self.client.aof_rewrite()
        self.client.save() # Dobbiamo aggiungere questo metodo al client!
        
        print("--- SETUP COMPLETATO ---")


if __name__ == '__main__':

    # Crea la suite di test
    suite = unittest.TestSuite()
    if mode == 'setup':
        print("Modalità: SETUP (esegue popolamento e verifica)")
        suite.addTest(unittest.makeSuite(TestKektorDBSetup))
    else:
        print("Modalità: VERIFY (esegue solo la verifica dei dati esistenti)")
    
    suite.addTest(unittest.makeSuite(TestKektorDBConsistency))
    
    # Esegui i test
    import sys
    runner = unittest.TextTestRunner()
    result = runner.run(suite)

    # Esce con un codice di errore se i test falliscono (utile per CI)
    if not result.wasSuccessful():
        exit(1)
