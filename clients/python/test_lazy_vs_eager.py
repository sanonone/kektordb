import numpy as np
import time
import argparse
from tqdm import tqdm
from kektordb_client import KektorDBClient

DATASET_TXT_FILE = "glove.6B.100d.txt"
METRIC = "cosine"
K_SEARCH = 10
NUM_QUERIES = 500

# Parametri Confronto
EAGER_EF = 200   # Qualità alta subito (Lento inserimento)
LAZY_EF = 40     # Qualità bassa subito (Veloce inserimento)
TARGET_EF = 200  # Obiettivo del Refine

def load_vectors(filepath, size):
    print(f"Caricamento {size} vettori...")
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

def calc_recall(client, index_name, queries, gt_sets):
    total = 0.0
    for i, q in enumerate(queries):
        try:
            res = client.vsearch(index_name, q.tolist(), k=K_SEARCH, ef_search=100)
            total += len(set(res).intersection(gt_sets[i])) / K_SEARCH
        except: pass
    return total / len(queries)

def run_scenario(client, vectors, words, queries, gt_sets, mode):
    index_name = f"bench-{mode}-{int(time.time())}"
    
    # 1. Configurazione
    ef_cons = EAGER_EF if mode == "EAGER" else LAZY_EF
    print(f"\n--- SCENARIO {mode} (ef_cons={ef_cons}) ---")
    
    # Creazione
    client.vcreate(index_name, metric=METRIC, precision="float32", m=16, ef_construction=ef_cons)
    
    # 2. Inserimento (VImport Async con Wait)
    print("Avvio Inserimento...")
    start_time = time.time()
    
    batch_data = [{"id": words[i], "vector": vectors[i].tolist()} for i in range(len(vectors))]
    # Unico chunk grosso per massimizzare vimport
    task = client.vimport(index_name, batch_data, wait=True)
    
    ingest_time = time.time() - start_time
    print(f"Ingestione completata in: {ingest_time:.2f}s")
    
    # 3. Recall Iniziale
    recall_init = calc_recall(client, index_name, queries, gt_sets)
    print(f"Recall Iniziale: {recall_init:.4f}")
    
    total_time = ingest_time
    
    # 4. Fase Refine (Solo per LAZY)
    if mode == "LAZY":
        print("Avvio Refinement Background...")
        start_refine = time.time()
        
        # Configura Refine aggressivo
        client.vupdate_config(index_name, {
            "refine_enabled": True,
            "refine_ef_construction": TARGET_EF, # Portiamo la qualità a livello Eager
            "refine_batch_size": 2000,
            "refine_interval": "1ms" # Più veloce possibile per il test
        })
        
        # Eseguiamo cicli finché la recall non è simile a EAGER
        # (Qui simuliamo chiamando trigger manuale finché non finisce un giro completo)
        # Stima: len(vectors) / batch_size
        cycles = (len(vectors) // 2000) + 1
        
        for _ in tqdm(range(cycles), desc="Refining"):
             client.vtrigger_maintenance(index_name, "refine", wait=True)
             
        refine_time = time.time() - start_refine
        total_time += refine_time
        print(f"Refinement completato in: {refine_time:.2f}s")
        
        recall_final = calc_recall(client, index_name, queries, gt_sets)
        print(f"Recall Finale: {recall_final:.4f}")
        
    client.delete_index(index_name)
    return ingest_time, total_time

def main():
    # Usa un dataset medio (100k) per vedere le differenze
    vectors, words = load_vectors(DATASET_TXT_FILE, 100000)
    
    # GT
    print("Calcolo GT...")
    query_idxs = np.random.choice(len(vectors), NUM_QUERIES, replace=False)
    queries = vectors[query_idxs]
    vecs_norm = vectors / np.linalg.norm(vectors, axis=1, keepdims=True)
    q_norm = queries / np.linalg.norm(queries, axis=1, keepdims=True)
    gt = []
    for q in q_norm:
        sims = np.dot(vecs_norm, q)
        gt.append({words[i] for i in np.argsort(sims)[::-1][:K_SEARCH]})
        
    client = KektorDBClient()
    
    # Esegui i due scenari
    t_ingest_eager, t_total_eager = run_scenario(client, vectors, words, queries, gt, "EAGER")
    t_ingest_lazy, t_total_lazy = run_scenario(client, vectors, words, queries, gt, "LAZY")
    
    print("\n=== RISULTATO SFIDA ===")
    print(f"Time To Ready (Ingest):")
    print(f"  EAGER: {t_ingest_eager:.2f}s")
    print(f"  LAZY:  {t_ingest_lazy:.2f}s  (WINNER! {t_ingest_eager/t_ingest_lazy:.1f}x faster start)")
    
    print(f"\nTime To Quality (Ingest + Refine):")
    print(f"  EAGER: {t_total_eager:.2f}s")
    print(f"  LAZY:  {t_total_lazy:.2f}s")

if __name__ == "__main__":
    main()
