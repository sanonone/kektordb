# File: clients/python/benchmark_glove_batch.py
import numpy as np
import time
import os
from tqdm import tqdm
from kektordb_client import KektorDBClient, APIError
import zipfile
import urllib.request
import argparse

# --- Configurazione ---
DATASET_URL = "https://nlp.stanford.edu/data/glove.6B.zip"
DATASET_ZIP_FILE = "glove.6B.zip"
DATASET_TXT_FILE = "glove.6B.100d.txt" # Useremo i vettori a 50 dimensioni

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
            with urllib.request.urlopen(DATASET_URL) as response, open(DATASET_ZIP_FILE, 'wb') as out_file:
                total_length = response.info().get('Content-Length')
                if total_length:
                    total_length = int(total_length)
                with tqdm.wrapattr(out_file, "write", total=total_length, desc=DATASET_ZIP_FILE) as f:
                    while True:
                        chunk = response.read(8192)
                        if not chunk:
                            break
                        f.write(chunk)
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
            try:
                vector = np.array(parts[1:], dtype=np.float32)
                # Assicurati che le dimensioni siano corrette (ignora righe malformate)
                if len(vector) == 100:
                    words.append(word)
                    vectors.append(vector)
            except ValueError:
                # Ignora righe che non possono essere convertite in float
                print(f"Attenzione: riga malformata saltata per la parola '{word}'")
                continue
    return np.array(vectors), words


def main(args):
    """La funzione principale ora accetta gli argomenti parsati."""

    # 1. Prepara il dataset
    download_and_extract_dataset()
    all_vectors, all_words = load_vectors_from_txt(DATASET_TXT_FILE)
    
    # Se la dimensione non è specificata (o è 0), usa l'intero dataset
    if args.size <= 0 or args.size > len(all_vectors):
        args.size = len(all_vectors)

    # Seleziona il subset del dataset
    vectors_to_index = all_vectors[:args.size]
    words_to_index = all_words[:args.size]
    num_vectors, dims = vectors_to_index.shape
    print(f"--- Benchmark su {num_vectors} vettori, {dims} dimensioni ---")

    INDEX_NAME = f"glove-{num_vectors}-{METRIC}-batch"
    client = KektorDBClient()

    try:
        
        # 2. Prepara KektorDB
        print(f"\n--- Fase di Indicizzazione (BATCH, {METRIC}, float32) ---")
        try:
            client.vcreate(INDEX_NAME, metric=METRIC, precision="float32", ef_construction=200, m=16)
        except APIError as e:
            if "already exists" in str(e):
                print(f"Indice '{INDEX_NAME}' probabilmente già esistente.")
            else:
                raise e

        # 3. Inserisci i dati in batch
        print(f"Inserimento di {num_vectors} vettori...")
        start_time = time.time()
        
        all_vector_objects = [
            {"id": words_to_index[i], "vector": vec.tolist()} for i, vec in enumerate(vectors_to_index)
        ]
        
        with tqdm(total=num_vectors, desc="Indicizzazione") as pbar:
            for i in range(0, num_vectors, BATCH_SIZE):
                batch = all_vector_objects[i : i + BATCH_SIZE]
                client.vadd_batch(INDEX_NAME, batch)
                pbar.update(len(batch))

        end_time = time.time()
        indexing_duration = end_time-start_time
        print(f"Indicizzazione completata in {indexing_duration:.2f} secondi.")
        print(f"Velocità di inserimento: {num_vectors / indexing_duration:.2f} vettori/secondo.")
        

        # 4. Fase di Test
        print(f"\n--- Fase di Test: Recall e QPS ---")
        
        query_indices = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
        query_vectors = vectors_to_index[query_indices]

        total_recall = 0.0
        total_search_time = 0.0

        print(f"Esecuzione di {NUM_QUERIES} ricerche...")
        for i, query_vec in enumerate(tqdm(query_vectors, desc="Ricerca")):
            # Calcola la verità assoluta con numpy
            vectors_norm = vectors_to_index / np.linalg.norm(vectors_to_index, axis=1, keepdims=True)
            query_norm = query_vec / np.linalg.norm(query_vec)
            similarities = np.dot(vectors_norm, query_norm)
            
            true_neighbors_indices = np.argsort(similarities)[::-1][:K_SEARCH]
            true_neighbor_ids = {words_to_index[idx] for idx in true_neighbors_indices}
            
            # Cerca con KektorDB
            search_start = time.time()
            results = client.vsearch(INDEX_NAME, query_vec.tolist(), k=K_SEARCH, ef_search=100)
            search_end = time.time()
            total_search_time += (search_end - search_start)
            
            intersection = set(results).intersection(true_neighbor_ids)
            recall = len(intersection) / float(K_SEARCH)
            total_recall += recall

        average_recall = total_recall / NUM_QUERIES
        qps = NUM_QUERIES / total_search_time

        print(f"\n--- BENCHMARK RESULTS (GloVe-{num_vectors}-) ---")
        print(f"Recall Media @{K_SEARCH}: {average_recall:.4f}")
        print(f"Performance di Ricerca (QPS): {qps:.2f} query/secondo")
    
    finally:
        # Pulizia: assicurati che l'indice venga sempre eliminato
        print(f"\nPulizia dell'indice '{INDEX_NAME}'...")
        try:
            client.delete_index(INDEX_NAME)
            print("Indice eliminato con successo.")
        except Exception as e:
            print(f"Impossibile eliminare l'indice: {e}")
        print(f"\nTest eseguito su dataset {DATASET_TXT_FILE}, con {num_vectors} vettori, metric {METRIC}, precision float32, m 16, ef construction 200, ef search 100, k search 10 ")


if __name__ == "__main__":
    # Parsing degli Argomenti da Riga di Comando
    parser = argparse.ArgumentParser(description="Benchmark script for KektorDB (GloVe, Cosine, float32).")
    parser.add_argument(
        "--size",
        type=int,
        default=0, # Default a 0 significa "usa tutto il dataset"
        help="Number of vectors from the dataset to use for the benchmark (default: all 400k)."
    )
    parsed_args = parser.parse_args()
    
    main(parsed_args)
