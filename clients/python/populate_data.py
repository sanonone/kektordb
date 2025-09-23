# File: clients/python/populate_data.py

import numpy as np
from kektordb_client import KektorDBClient, APIError
import time

# --- Configurazione ---
HOST = "localhost"
PORT = 9091
INDEX_NAME = "documenti-quant"
NUM_VECTORS = 1000
VECTOR_DIMS = 128 # Una dimensione comune per gli embeddings di testo

def normalize_vectors(data):
    """Normalizza una matrice di vettori a lunghezza unitaria (L2 norm)."""
    norms = np.linalg.norm(data, axis=1, keepdims=True)
    # Evita divisione per zero se un vettore è nullo
    norms[norms == 0] = 1
    return data / norms

def main():
    """Script per popolare KektorDB con dati di test."""
    
    print(f"Tentativo di connessione a KektorDB su {HOST}:{PORT}...")
    client = KektorDBClient(host=HOST, port=PORT)
    
    # 1. Crea l'indice (o ignoralo se esiste già)
    try:
        print(f"Creazione dell'indice '{INDEX_NAME}' con metrica 'cosine'...")
        client.vcreate(INDEX_NAME, metric="cosine")
        print("Indice creato con successo.")
    except APIError as e:
        if "già esistente" in e.args[0]:
            print("Indice già esistente, procedo.")
        else:
            raise e

    # 2. Genera vettori casuali e normalizzali
    print(f"Generazione di {NUM_VECTORS} vettori casuali a {VECTOR_DIMS} dimensioni...")
    # Crea dati casuali in un range [-1, 1]
    random_vectors = np.random.uniform(low=-1.0, high=1.0, size=(NUM_VECTORS, VECTOR_DIMS)).astype(np.float32)
    
    # Normalizza i vettori a lunghezza 1, fondamentale per la similarità del coseno
    normalized_vectors = normalize_vectors(random_vectors)
    print("Vettori normalizzati.")

    # 3. Inserisci i vettori in KektorDB
    print(f"Inserimento di {NUM_VECTORS} vettori in corso...")
    start_time = time.time()
    
    for i, vec in enumerate(normalized_vectors):
        item_id = f"doc_{i}"
        metadata = {
            "source": "bulk_script",
            "doc_num": i
        }
        
        # Converti il vettore numpy in una lista Python prima di inviarlo
        client.vadd(INDEX_NAME, item_id, vec.tolist(), metadata)
        
        # Stampa un progresso ogni 100 vettori
        if (i + 1) % 100 == 0:
            print(f"  ... {i + 1}/{NUM_VECTORS} vettori inseriti.")
            
    end_time = time.time()
    duration = end_time - start_time
    
    print("\n--- Riepilogo ---")
    print(f"✅ Inserimento completato.")
    print(f"   - Vettori totali: {NUM_VECTORS}")
    print(f"   - Tempo totale: {duration:.2f} secondi")
    print(f"   - Vettori al secondo (circa): {NUM_VECTORS / duration:.2f}")


if __name__ == "__main__":
    # Per eseguire lo script, potrebbe essere necessario installare numpy:
    # pip install numpy
    main()
