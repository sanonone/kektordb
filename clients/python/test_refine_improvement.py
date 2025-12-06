import numpy as np
import time
import os
import argparse
from tqdm import tqdm
from kektordb_client import KektorDBClient

DATASET_TXT_FILE = "glove.6B.100d.txt"
METRIC = "cosine"
K_SEARCH = 10
NUM_QUERIES = 1000

# Parametri "cattivi" per simulare un inserimento super-veloce ma impreciso
BAD_EF_CONSTRUCTION = 40 

# Parametri "buoni" per il refinement
TARGET_EF = 200
REFINE_BATCH = 20000

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
    return np.array(vectors), words

def calc_recall(client, index_name, queries, gt_sets, label):
    total_recall = 0.0
    print(f"--- Misurazione Recall ({label}) ---")
    for i, q in enumerate(queries):
        res = client.vsearch(index_name, q.tolist(), k=K_SEARCH, ef_search=100)
        intersection = set(res).intersection(gt_sets[i])
        total_recall += len(intersection) / K_SEARCH
    
    avg = total_recall / len(queries)
    print(f"[{label}] Recall: {avg:.4f}")
    return avg

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--size", type=int, default=200000, help="Dataset size (keep small for quick refine test)")
    args = parser.parse_args()

    vectors, words = load_vectors(DATASET_TXT_FILE, args.size)
    client = KektorDBClient()
    index_name = f"test-refine-{int(time.time())}"
    
    # 1. Creazione "Low Quality"
    print(f"Creazione indice LOW QUALITY (ef_construction={BAD_EF_CONSTRUCTION})...")
    client.vcreate(index_name, metric=METRIC, precision="float32", m=16, ef_construction=BAD_EF_CONSTRUCTION)
    
    batch_data = [{"id": words[i], "vector": vectors[i].tolist()} for i in range(len(vectors))]
    for i in range(0, len(batch_data), 5000):
        client.vadd_batch(index_name, batch_data[i:i+5000])

    # 2. Ground Truth
    print("Calcolo Ground Truth...")
    query_idxs = np.random.choice(len(vectors), NUM_QUERIES, replace=False)
    queries = vectors[query_idxs]
    vecs_norm = vectors / np.linalg.norm(vectors, axis=1, keepdims=True)
    queries_norm = queries / np.linalg.norm(queries, axis=1, keepdims=True)
    
    gt_sets = []
    for q in queries_norm:
        sims = np.dot(vecs_norm, q)
        gt_sets.append({words[i] for i in np.argsort(sims)[::-1][:K_SEARCH]})

    # 3. Recall Base
    base_recall = calc_recall(client, index_name, queries, gt_sets, "BASELINE (Low Quality)")

    # 4. Configurazione Refine
    print(f"\nConfigurazione Refine (ef={TARGET_EF}, batch={REFINE_BATCH})...")
    client.vupdate_config(index_name, {
        "refine_enabled": True,
        "refine_ef_construction": TARGET_EF,
        "refine_batch_size": REFINE_BATCH,
        "refine_interval": "1s"
    })

    # 5. Esecuzione Cicli Refine
    # Calcoliamo quante chiamate servono per coprire tutto il grafo
    # Poiché l'API è sincrona (attende la fine del batch), possiamo loopare.
    iterations = (args.size // REFINE_BATCH) + 2
    print(f"Avvio {iterations} cicli di Refine manuale (Simulazione background)...")
    
    start_refine = time.time()
    for _ in tqdm(range(iterations)):
        client.vtrigger_maintenance(index_name, "refine")
    
    print(f"Refinement completato in {time.time() - start_refine:.2f}s")

    # 6. Recall Finale
    final_recall = calc_recall(client, index_name, queries, gt_sets, "REFINED (High Quality)")
    
    improvement = final_recall - base_recall
    print(f"\nRisultato: Miglioramento Recall = +{improvement:.4f}")
    
    if improvement > 0.01:
        print("✅ SUCCESSO: Il Refinement ha migliorato il grafo.")
    else:
        print("⚠️ ATTENZIONE: Miglioramento trascurabile (il grafo era già buono o refine non efficace).")

    client.delete_index(index_name)

if __name__ == "__main__":
    main()
