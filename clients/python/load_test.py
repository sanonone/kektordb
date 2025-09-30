# File: clients/python/load_test.py
import random
import time
import threading
from kektordb_client import KektorDBClient
import numpy as np

# --- Configurazione ---
HOST = "localhost"
PORT = 9091
INDEX_NAME = "pprof_test_idx"
VECTOR_DIMS = 128
NUM_VECTORS_TO_ADD = 5000
NUM_SEARCH_THREADS = 4 # Simula 4 client che cercano contemporaneamente
SEARCHES_PER_THREAD = 1000

def normalize_vectors(data):
    norms = np.linalg.norm(data, axis=1, keepdims=True)
    norms[norms == 0] = 1
    return data / norms

def search_worker(client, vectors):
    """Una funzione che esegue ricerche continue."""
    print(f"Thread di ricerca avviato...")
    for i in range(SEARCHES_PER_THREAD):
        # Scegli un vettore di query casuale
        query_vec = random.choice(vectors).tolist()
        try:
            client.vsearch(INDEX_NAME, k=10, query_vector=query_vec)
        except Exception as e:
            print(f"Errore durante la ricerca: {e}")
        if (i+1) % 100 == 0:
            print(f"  > Thread ha completato {i+1} ricerche")
    print(f"Thread di ricerca terminato.")


def main():
    client = KektorDBClient(host=HOST, port=PORT)
    
    # 1. Prepara l'indice
    try:
        client.vcreate(INDEX_NAME, metric="cosine")
        print(f"Indice '{INDEX_NAME}' creato.")
    except Exception:
        print(f"Indice '{INDEX_NAME}' probabilmente già esistente.")

    # 2. Inserisci i dati iniziali
    print(f"Inserimento di {NUM_VECTORS_TO_ADD} vettori...")
    vectors = normalize_vectors(np.random.rand(NUM_VECTORS_TO_ADD, VECTOR_DIMS).astype(np.float32))
    for i, vec in enumerate(vectors):
        client.vadd(INDEX_NAME, f"vec_{i}", vec.tolist())
    print("Inserimento completato.")

    # 3. Avvia i thread di ricerca per generare carico
    print(f"\nAvvio di {NUM_SEARCH_THREADS} thread di ricerca per generare carico sulla CPU...")
    threads = []
    for _ in range(NUM_SEARCH_THREADS):
        thread = threading.Thread(target=search_worker, args=(client, vectors))
        threads.append(thread)
        thread.start()

    # Tieni il programma principale in esecuzione mentre i thread lavorano
    for thread in threads:
        thread.join()
        
    print("\nTest di carico completato.")

if __name__ == "__main__":
    main()
