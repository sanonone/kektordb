# File: clients/python/benchmark.py
import numpy as np
import requests
import time
import os
from tqdm import tqdm
from kektordb_client import KektorDBClient
import zipfile
import urllib.request

# --- NUOVA CONFIGURAZIONE ---
DATASET_URL = "https://nlp.stanford.edu/data/glove.6B.zip"
DATASET_ZIP_FILE = "glove.6B.zip"
DATASET_TXT_FILE = "glove.6B.50d.txt" # Useremo i vettori a 50 dimensioni

INDEX_NAME = "glove-6B-50d-batch-benchmark"
METRIC = "cosine" # GloVe è tipicamente usato con la similarità del coseno
K_SEARCH = 10
NUM_QUERIES = 100
BATCH_SIZE = 256 # Dimensione del batch per l'inserimento

def download_and_extract_dataset():
    """Scarica e de-comprime il dataset se non è presente in locale."""
    if os.path.exists(DATASET_TXT_FILE):
        print(f"Dataset '{DATASET_TXT_FILE}' già presente.")
        return
        
    if not os.path.exists(DATASET_ZIP_FILE):
        print(f"Download del dataset da {DATASET_URL}...")
        try:
            urllib.request.urlretrieve(DATASET_URL, DATASET_ZIP_FILE)
            print("Download completato.")
        except Exception as e:
            print(f"\nERRORE: Impossibile scaricare il dataset. Causa: {e}")
            exit(1)
            
    print(f"Decompressione di '{DATASET_ZIP_FILE}'...")
    with zipfile.ZipFile(DATASET_ZIP_FILE, 'r') as zip_ref:
        zip_ref.extractall('.') # Estrai nella cartella corrente
    print("Decompressione completata.")

def load_vectors_from_txt(filepath):
    """Carica i vettori da un file di testo GloVe."""
    print(f"Caricamento dei vettori da '{filepath}'...")
    words = []
    vectors = []
    with open(filepath, 'r', encoding='utf-8') as f:
        for line in tqdm(f):
            parts = line.split()
            word = parts[0]
            vector = np.array(parts[1:], dtype=np.float32)
            words.append(word)
            vectors.append(vector)
    return np.array(vectors), words


def main():
    # 1. Prepara il dataset
    download_and_extract_dataset()
    vectors, words = load_vectors_from_txt(DATASET_TXT_FILE)
    num_vectors, dims = vectors.shape
    print(f"Dataset caricato: {num_vectors} vettori, {dims} dimensioni.")

    # 2. Prepara KektorDB
    client = KektorDBClient()
    print("\n--- Fase di Indicizzazione (BATCH) ---")
    try:
        client.vcreate(INDEX_NAME, metric=METRIC, precision="float32", ef_construction=200, m=16)
    except Exception as e:
        print(f"Indice '{INDEX_NAME}' probabilmente già esistente.")

    # 3. Inserisci i dati
    print(f"Inserimento di {num_vectors} vettori in batch da {BATCH_SIZE} in KektorDB...")
    start_time = time.time()

    # Crea una lista di oggetti da inviare
    all_vector_objects = []
    for i, vec in enumerate(vectors):
        all_vector_objects.append({
            "id": words[i],
            "vector": vec.tolist()
        })
        
    # Invia i dati in "chunks" (lotti)
    with tqdm(total=num_vectors) as pbar:
        for i in range(0, num_vectors, BATCH_SIZE):
            batch = all_vector_objects[i : i + BATCH_SIZE]
            client.vadd_batch(INDEX_NAME, batch)
            pbar.update(len(batch))

    end_time = time.time()
    indexing_duration = end_time - start_time
    print(f"Indicizzazione completata in {indexing_duration:.2f} secondi.")
    print(f"Velocità di inserimento: {num_vectors / indexing_duration:.2f} vettori/secondo.")

    # 4. Fase di Test
    print("\n--- Fase di Test: Recall e QPS ---")
    
    query_indices = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
    query_vectors = vectors[query_indices]

    total_recall = 0.0
    total_search_time = 0.0

    print(f"Esecuzione di {NUM_QUERIES} ricerche...")
    for i, query_vec in enumerate(tqdm(query_vectors)):
        # Calcola la verità assoluta con numpy
        # Normalizza per il calcolo corretto della similarità del coseno
        vectors_norm = vectors / np.linalg.norm(vectors, axis=1, keepdims=True)
        query_norm = query_vec / np.linalg.norm(query_vec)
        similarities = np.dot(vectors_norm, query_norm)
        
        true_neighbors_indices = np.argsort(similarities)[::-1][:K_SEARCH]
        true_neighbor_ids = {words[idx] for idx in true_neighbors_indices}
        
        # Cerca con KektorDB
        search_start = time.time()
        results = client.vsearch(INDEX_NAME, query_vec.tolist(), k=K_SEARCH, ef_search=100)
        search_end = time.time()
        total_search_time += (search_end - search_start)
        
        # Calcola la recall
        intersection = set(results).intersection(true_neighbor_ids)
        recall = len(intersection) / float(K_SEARCH)
        total_recall += recall

    average_recall = total_recall / NUM_QUERIES
    qps = NUM_QUERIES / total_search_time

    print("\n--- BENCHMARK RESULTS (GloVe-400k-50d) ---")
    print(f"Recall Media @{K_SEARCH}: {average_recall:.4f}")
    print(f"Performance di Ricerca (QPS): {qps:.2f} query/secondo")


if __name__ == "__main__":
    main()
