import numpy as np
import time
import os
import argparse
import random
from tqdm import tqdm
from kektordb_client import KektorDBClient

DATASET_TXT_FILE = "glove.6B.100d.txt"
METRIC = "cosine"
K_SEARCH = 10
NUM_QUERIES = 500

def load_vectors(filepath, size):
    print(f"Caricamento vettori da {filepath}...")
    vectors, words = [], []
    with open(filepath, 'r', encoding='utf-8') as f:
        for i, line in enumerate(f):
            if size > 0 and i >= size: break
            parts = line.split()
            try:
                vec = np.array(parts[1:], dtype=np.float32)
                if len(vec) == 100:
                    words.append(parts[0])
                    vectors.append(vec)
            except: continue
    return np.array(vectors), np.array(words) # Usiamo np array per indexing veloce

def compute_ground_truth(all_vectors, all_words, query_vectors, active_mask):
    """
    Calcola la Ground Truth considerando SOLO i vettori attivi (mask=True).
    """
    print("Calcolo Ground Truth (Rigorous)...")
    gt_sets = []
    
    # Normalizzazione (Cosine)
    # Nota: facciamo copie per non toccare l'originale se serve
    vecs_active = all_vectors
    vecs_norm = vecs_active / np.linalg.norm(vecs_active, axis=1, keepdims=True)
    queries_norm = query_vectors / np.linalg.norm(query_vectors, axis=1, keepdims=True)
    
    for q in tqdm(queries_norm, desc="GT Calculation"):
        sims = np.dot(vecs_norm, q)
        
        # TRUCCO: Impostiamo la similarità dei nodi cancellati a -infinito
        # così numpy non li sceglierà mai come Top K.
        sims[~active_mask] = -np.inf
        
        top_k_indices = np.argsort(sims)[::-1][:K_SEARCH]
        gt_sets.append(set(all_words[top_k_indices]))
        
    return gt_sets

def calc_recall(client, index_name, queries, ground_truth_sets, label):
    total_recall = 0.0
    print(f"--- Misurazione Recall ({label}) ---")
    for i, q in enumerate(tqdm(queries)):
        try:
            results = client.vsearch(index_name, q.tolist(), k=K_SEARCH, ef_search=100)
            intersection = set(results).intersection(ground_truth_sets[i])
            total_recall += len(intersection) / K_SEARCH
        except Exception as e:
            print(f"Error searching: {e}")
            continue
    
    avg_recall = total_recall / len(queries)
    print(f"[{label}] Recall Reale: {avg_recall:.4f}")
    return avg_recall

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--size", type=int, default=50000, help="Dimensione dataset")
    parser.add_argument("--delete_pct", type=float, default=0.2, help="Percentuale cancellazione")
    args = parser.parse_args()

    # 1. Setup Dati
    vectors, words = load_vectors(DATASET_TXT_FILE, args.size)
    num_vectors = len(vectors)
    
    # Maschera booleana: True = Vivo, False = Cancellato
    active_mask = np.ones(num_vectors, dtype=bool)
    
    client = KektorDBClient()
    index_name = f"test-vac-rigorous-{int(time.time())}"
    
    # 2. Creazione & Insert
    print(f"Creazione indice {index_name}...")
    client.vcreate(index_name, metric=METRIC, precision="float32", m=16, ef_construction=200)
    
    batch_data = [{"id": words[i], "vector": vectors[i].tolist()} for i in range(num_vectors)]
    for i in range(0, num_vectors, 5000):
        client.vadd_batch(index_name, batch_data[i:i+5000])

    # 3. GT Iniziale (Tutti attivi)
    query_idxs = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
    queries = vectors[query_idxs]
    
    gt_initial = compute_ground_truth(vectors, words, queries, active_mask)
    calc_recall(client, index_name, queries, gt_initial, "PRE-DELETE")

    # 4. Cancellazione
    num_delete = int(num_vectors * args.delete_pct)
    # Scegliamo indici casuali da cancellare
    delete_indices = np.random.choice(num_vectors, num_delete, replace=False)
    
    print(f"\nCancellazione di {num_delete} nodi...")
    # Aggiorna la maschera (importante per la GT successiva)
    active_mask[delete_indices] = False
    
    # Esegui cancellazione su DB
    ids_to_delete = words[delete_indices]
    for i in tqdm(range(0, len(ids_to_delete), 1000)):
        batch = ids_to_delete[i:i+1000]
        for doc_id in batch:
            client.vdelete(index_name, doc_id)

    # 5. Vacuum
    print("\nAvvio VACUUM (Graph Healing)...")
    client.vupdate_config(index_name, {"vacuum_interval": "1s", "delete_threshold": 0.01})
    
    start_vac = time.time()
    client.vtrigger_maintenance(index_name, "vacuum")
    print(f"Vacuum completato in {time.time() - start_vac:.2f}s")
    
    # 6. RICALCOLO GT (RIGOROSO)
    # Calcoliamo quali SONO i veri vicini ADESSO, ignorando quelli cancellati.
    # Se il grafo è sano, KektorDB dovrebbe trovare QUESTI nuovi vicini.
    print("\nRicalcolo Ground Truth sui nodi sopravvissuti...")
    gt_post = compute_ground_truth(vectors, words, queries, active_mask)
    
    # 7. Recall Finale
    recall_final = calc_recall(client, index_name, queries, gt_post, "POST-VACUUM (Rigorous)")
    
    print(f"\nRisultato Finale: Il grafo mantiene il {recall_final*100:.2f}% della qualità teorica massima.")

    client.delete_index(index_name)

if __name__ == "__main__":
    main()
