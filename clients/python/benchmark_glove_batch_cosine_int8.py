import numpy as np
import time
import os
from tqdm import tqdm
from kektordb_client import KektorDBClient
import zipfile
import urllib.request
import argparse 
import time

# --- Configurazione ---
DATASET_URL = "https://nlp.stanford.edu/data/glove.6B.zip"
DATASET_ZIP_FILE = "glove.6B.zip"
DATASET_TXT_FILE = "glove.6B.100d.txt"

METRIC = "cosine"
PRECISION = "int8"
K_SEARCH = 10
NUM_QUERIES = 100
BATCH_SIZE = 256

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


def main(args):
    """La funzione principale ora accetta gli argomenti parsati."""
    
    INDEX_NAME = f"glove-benchmark-{args.size}-{int(time.time())}"

    # 1. Prepara il dataset
    download_and_extract_dataset()
    all_vectors, all_words = load_vectors_from_txt(DATASET_TXT_FILE)
    
    # Se la dimensione non è specificata, usa l'intero dataset
    if args.size <= 0:
        args.size = len(all_vectors)
        
    # Seleziona il subset del dataset
    vectors_to_index = all_vectors[:args.size]
    words_to_index = all_words[:args.size]
    num_vectors, dims = vectors_to_index.shape
    print(f"--- Benchmark su {num_vectors} vettori, {dims} dimensioni ---")

    # 2. Prepara KektorDB
    client = KektorDBClient()
    
    try:
        print(f"\n--- Fase di Indicizzazione (BATCH, {METRIC}, float32) ---")
        client.vcreate(INDEX_NAME, metric=METRIC, precision="float32", ef_construction=200, m=16)

        # 3. Inserisci i dati
        print(f"Inserimento di {num_vectors} vettori...")
        start_time = time.time()
        all_vector_objects = [
            {"id": words_to_index[i], "vector": vec.tolist()} for i, vec in enumerate(vectors_to_index)
        ]
        with tqdm(total=num_vectors) as pbar:
            for i in range(0, num_vectors, BATCH_SIZE):
                batch = all_vector_objects[i : i + BATCH_SIZE]
                client.vadd_batch(INDEX_NAME, batch)
                pbar.update(len(batch))
        end_time = time.time()
        print(f"Indicizzazione completata in {end_time - start_time:.2f}s.")

        # --- COMPRESSIONE ---
        '''
        print(f"\nCompressione dell'indice a precisione '{PRECISION}'...")
        # Aumenta il timeout per la compressione
        original_timeout = client.timeout
        client.timeout = 3600 # 1 ora
        try:
            client.vcompress(INDEX_NAME, precision=PRECISION)
        finally:
            client.timeout = original_timeout # Ripristina sempre
        print("Compressione completata.")
        '''

        print(f"\nAvvio compressione dell'indice a precisione '{PRECISION}'...")
        start_time_compress = time.time()
        try:
            # La chiamata sembra la stessa, ma ora sotto il cofano fa il polling.
            # Stampa i messaggi di "attesa".
            task = client.vcompress(INDEX_NAME, precision=PRECISION)
            if task.status == 'failed':
                raise APIError(task.error)
            
            duration_compress = time.time() - start_time_compress
            print(f"Compressione completata in {duration_compress:.2f} secondi.")
        except Exception as e:
            print(f"Fallimento della compressione: {e}")
            exit(1)

       # --- NUOVO: TEST DI QUALITÀ DELLA QUANTIZZAZIONE ---
        print("\nVerifica della qualità della quantizzazione...")
        check_indices = np.random.choice(num_vectors, 5, replace=False)

        for i in check_indices:
            # --- CORREZIONE QUI ---
            # Vecchio: original_vec = num_vectors[i]
            # Nuovo: Usa la matrice numpy corretta
            original_vec = vectors_to_index[i]
            
            # Anche qui, usa la lista di parole corretta
            retrieved_data = client.vget(INDEX_NAME, words_to_index[i])
            # --- FINE CORREZIONE ---
            
            dequantized_vec = np.array(retrieved_data['vector'], dtype=np.float32)

            # Calcola la similarità del coseno tra l'originale e il de-quantizzato
            original_norm = original_vec / np.linalg.norm(original_vec)
            dequantized_norm = dequantized_vec / np.linalg.norm(dequantized_vec)
            similarity = np.dot(original_norm, dequantized_norm)

            print(f"  - Vettore '{words_to_index[i]}': Similarità tra originale e de-quantizzato = {similarity:.4f}")

            if similarity < 0.9:
                print(f"    ATTENZIONE: Bassa similarità. La quantizzazione sta perdendo troppa informazione.")
    # --- FINE TEST --- 

        # 4. Fase di Test
        print(f"\n--- Fase di Test (su indice compresso {PRECISION}): Recall e QPS ---")
        query_indices = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
        query_vectors = vectors_to_index[query_indices]
        
        total_recall = 0.0
        total_search_time = 0.0

        print(f"Esecuzione di {NUM_QUERIES} ricerche...")
        for i, query_vec in enumerate(tqdm(query_vectors)):
            # Calcola la verità assoluta con numpy
            vectors_norm = vectors_to_index / np.linalg.norm(vectors_to_index, axis=1, keepdims=True)
            query_norm = query_vec / np.linalg.norm(query_vec)
            similarities = np.dot(vectors_norm, query_norm)
            
            true_neighbors_indices = np.argsort(similarities)[::-1][:K_SEARCH]
            true_neighbor_ids = {words_to_index[idx] for idx in true_neighbors_indices}
            
            # Cerca con KektorDB
            search_start = time.time()
            results = client.vsearch(INDEX_NAME, query_vec.tolist(), k=K_SEARCH, ef_search=200)
            search_end = time.time()
            total_search_time += (search_end - search_start)
            
            intersection = set(results).intersection(true_neighbor_ids)
            recall = len(intersection) / float(K_SEARCH)
            total_recall += recall

        average_recall = total_recall / NUM_QUERIES
        qps = NUM_QUERIES / total_search_time

        print(f"\n--- BENCHMARK RESULTS ({num_vectors} vettori) ---")
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

        print(f"\nTest eseguito su dataset {DATASET_TXT_FILE}, con {num_vectors} vettori, metric {METRIC}, precision float32, m 16, ef construction 200, ef search 200, k search 10 ")


if __name__ == "__main__":
    # --- NUOVO: Parsing degli Argomenti da Riga di Comando ---
    parser = argparse.ArgumentParser(description="Benchmark script for KektorDB.")
    parser.add_argument(
        "--size",
        type=int,
        default=0, # Default a 0 significa "usa tutto il dataset"
        help="Number of vectors from the dataset to use for the benchmark."
    )
    parsed_args = parser.parse_args()
    
    main(parsed_args)
