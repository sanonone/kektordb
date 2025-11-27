import numpy as np
import time
import os
from tqdm import tqdm
from kektordb_client import KektorDBClient
import tarfile
import urllib.request
import argparse

# --- Configurazione ---
# Nuovo URL e nomi di file per SIFT1M dal corpus texmex
DATASET_URL = "ftp://ftp.irisa.fr/local/texmex/corpus/sift.tar.gz"
DATASET_TAR_GZ_FILE = "sift.tar.gz"
# Il file che ci interessa all'interno dell'archivio
DATASET_BIN_FILE = "sift/sift_base.fvecs" 
# File .npy convertito che creeremo noi
DATASET_NPY_FILE = "sift_base.npy" 

METRIC = "euclidean"
PRECISION = "float32"
BATCH_SIZE = 10000
K_SEARCH = 10
NUM_QUERIES = 1000

def fvecs_read(filename, c_contiguous=True):
    """Legge dati dal formato .fvecs binario."""
    fv = np.fromfile(filename, dtype=np.float32)
    if fv.size == 0:
        return np.zeros((0, 0))
    dim = fv.view(np.int32)[0]
    assert fv.size % (dim + 1) == 0
    fv = fv.reshape(-1, dim + 1)
    if not all(fv.view(np.int32)[:, 0] == dim):
        raise IOError("File non in formato fvecs")
    fv = fv[:, 1:]
    if c_contiguous:
        fv = fv.copy()
    return fv

def download_and_convert_dataset():
    """Scarica, estrae e converte il dataset se non è presente in locale."""
    if os.path.exists(DATASET_NPY_FILE):
        print(f"Dataset '{DATASET_NPY_FILE}' già presente.")
        return
        
    if not os.path.exists(DATASET_TAR_GZ_FILE):
        print(f"Download del dataset da {DATASET_URL}...")
        try:
            # NOTA: urllib potrebbe fallire. Se fallisce, usare wget.
            urllib.request.urlretrieve(DATASET_URL, DATASET_TAR_GZ_FILE)
            print("Download completato.")
        except Exception as e:
            print(f"\nERRORE: Download automatico fallito. Causa: {e}")
            print("\n--- ISTRUZIONI MANUALI ---")
            print("1. Apri il terminale.")
            print(f"2. Esegui: wget {DATASET_URL}")
            print("3. Riesegui questo script.")
            print("--------------------------")
            exit(1)
            
    print(f"Estrazione di '{DATASET_TAR_GZ_FILE}'...")
    with tarfile.open(DATASET_TAR_GZ_FILE, "r:gz") as tar:
        tar.extractall()
    print("Estrazione completata.")
    
    print(f"Conversione di '{DATASET_BIN_FILE}' in formato .npy...")
    vectors = fvecs_read(DATASET_BIN_FILE)
    np.save(DATASET_NPY_FILE, vectors)
    print(f"Conversione completata. File salvato: '{DATASET_NPY_FILE}'")


def main(args):
    # 1. Prepara il dataset
    download_and_convert_dataset()
    print(f"Caricamento di '{DATASET_NPY_FILE}' in memoria...")
    all_vectors = np.load(DATASET_NPY_FILE)
    
    # Seleziona il subset
    if args.size <= 0 or args.size > len(all_vectors):
        args.size = len(all_vectors)
    vectors_to_index = all_vectors[:args.size]
    num_vectors, dims = vectors_to_index.shape
    print(f"--- Benchmark su {num_vectors} vettori, {dims} dimensioni ---")

    # 2. Prepara KektorDB
    INDEX_NAME = f"sift-{num_vectors}-euclidean-float32"
    client = KektorDBClient()

    try:
        print(f"\n--- Fase di Indicizzazione (BATCH, {METRIC}, {PRECISION}) ---")
        try:
            client.vcreate(INDEX_NAME, metric=METRIC, precision=PRECISION, ef_construction=200, m=16)
        except Exception as e:
            print(f"Indice '{INDEX_NAME}' probabilmente già esistente.")

        # 3. Inserisci i dati in batch
        print(f"Inserimento di {num_vectors} vettori in batch da {BATCH_SIZE}...")
        start_time = time.time()
        
        all_vector_objects = [{"id": f"sift_{i}", "vector": vec.tolist()} for i, vec in enumerate(vectors_to_index)]
            
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
        print(f"\n--- Fase di Test ({PRECISION}): Recall e QPS ---")
        
        query_indices = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
        query_vectors = vectors_to_index[query_indices]

        # --- A. PRE-CALCOLO GROUND TRUTH ---
        print(f"Calcolo Ground Truth per {NUM_QUERIES} query...")
        ground_truths = []
        
        for query_vec in tqdm(query_vectors, desc="Ground Truth"):
            distances = np.sum((vectors_to_index - query_vec)**2, axis=1)
            true_indices = np.argsort(distances)[:K_SEARCH]
            ground_truths.append({f"sift_{idx}" for idx in true_indices})

        # --- B. WARMUP ---
        print("Esecuzione Warmup...")
        for i in range(min(20, len(query_vectors))):
             client.vsearch(INDEX_NAME, query_vectors[i].tolist(), k=K_SEARCH, ef_search=100)

        # --- C. BENCHMARK PURO ---
        print(f"Esecuzione di {NUM_QUERIES} ricerche...")
        total_search_time = 0.0
        total_recall = 0.0

        for i, query_vec in enumerate(tqdm(query_vectors, desc="Search")):
            start_t = time.time()
            results = client.vsearch(INDEX_NAME, query_vec.tolist(), k=K_SEARCH, ef_search=100)
            end_t = time.time()
            
            total_search_time += (end_t - start_t)
            
            # Calcolo Recall
            intersection = set(results).intersection(ground_truths[i])
            total_recall += len(intersection) / float(K_SEARCH)

        average_recall = total_recall / NUM_QUERIES
        qps = NUM_QUERIES / total_search_time

        print(f"\n--- BENCHMARK RESULTS ({INDEX_NAME}) ---")
        print(f"Recall Media @{K_SEARCH}: {average_recall:.4f}")
        print(f"Performance di Ricerca (QPS): {qps:.2f} query/secondo")

    finally:
        # Pulizia
        print(f"\nPulizia dell'indice '{INDEX_NAME}'...")
        try:
            client.delete_index(INDEX_NAME)
            print("Indice eliminato.")
        except Exception as e:
            print(f"Impossibile eliminare l'indice: {e}")

        print(f"\nTest eseguito su dataset {DATASET_BIN_FILE}, con {num_vectors} vettori, metric {METRIC}, precision float32, m 16, ef construction 200, ef search 100, k search 10 ")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Benchmark script for KektorDB (SIFT, Euclidean, float32).")
    parser.add_argument("--size", type=int, default=100000, help="Number of vectors to use (default: 100000).")
    parsed_args = parser.parse_args()
    main(parsed_args) 
