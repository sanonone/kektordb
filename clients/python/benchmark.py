# File: benchmark.py
#
# Script di benchmarking unificato per KektorDB, Qdrant, e ChromaDB
# con supporto per dataset e dimensioni personalizzabili.
#
# Esempi di esecuzione:
#   # Test rapido su 10k vettori del dataset GloVe
#   python benchmark.py --dataset=glove-100d --size=10000
#
#   # Benchmark completo su 1 milione di vettori del dataset SIFT
#   python benchmark.py --dataset=sift-1m --size=1000000

import abc
import time
import os
import subprocess
import urllib.request
import zipfile
import tarfile
import argparse
import numpy as np
import pandas as pd
from tqdm import tqdm

# --- Import dei Client dei Database (con gestione errori) ---
from kektordb_client import KektorDBClient, APIError

try:
    from qdrant_client import QdrantClient, models as qdrant_models
    QDRANT_AVAILABLE = True
except ImportError:
    QDRANT_AVAILABLE = False
    print("ATTENZIONE: `qdrant-client` non trovato. Il benchmark per Qdrant sarà saltato. Esegui: pip install qdrant-client")

try:
    import chromadb
    from chromadb.config import Settings
    CHROMA_AVAILABLE = True
except ImportError:
    CHROMA_AVAILABLE = False
    print("ATTENZIONE: `chromadb` non trovato. Il benchmark per ChromaDB sarà saltato. Esegui: pip install chromadb")

# ==============================================================================
# === DEFINIZIONE DEI DATASET =================================================
# ==============================================================================

def fvecs_read(filename, c_contiguous=True):
    """Legge dati dal formato binario .fvecs."""
    with open(filename, "rb") as f:
        fv = np.fromfile(f, dtype=np.float32)
    if fv.size == 0: return np.zeros((0, 0))
    dim = fv.view(np.int32)[0]
    assert fv.size % (dim + 1) == 0
    fv = fv.reshape(-1, dim + 1)[:, 1:]
    if c_contiguous: fv = fv.copy()
    return fv

def get_dataset(name: str, size: int):
    print(f"\n--- Preparazione dataset: {name} ---")
    base_dir = "./datasets"
    os.makedirs(base_dir, exist_ok=True)
    
    if name == "glove-100d":
        url = "https://nlp.stanford.edu/data/glove.6B.zip"
        zip_path = os.path.join(base_dir, "glove.6B.zip")
        txt_path = os.path.join(base_dir, "glove.6B.100d.txt")
        
        if not os.path.exists(txt_path):
            if not os.path.exists(zip_path):
                print(f"Download {url}...")
                urllib.request.urlretrieve(url, zip_path)
            print("Estrazione...")
            with zipfile.ZipFile(zip_path, 'r') as zf:
                zf.extract("glove.6B.100d.txt", path=base_dir)
        
        print("Parsing vettori...")
        vectors = []
        with open(txt_path, 'r', encoding='utf-8') as f:
            for line in f:
                parts = line.split()
                try:
                    vec = np.array(parts[1:], dtype=np.float32)
                    if len(vec) == 100: vectors.append(vec)
                except: continue
        all_vectors = np.array(vectors)
        metric = "cosine"

    elif name == "sift-1m":
        url = "ftp://ftp.irisa.fr/local/texmex/corpus/sift.tar.gz"
        tar_path = os.path.join(base_dir, "sift.tar.gz")
        fvecs_path = os.path.join(base_dir, "sift/sift_base.fvecs")
        npy_path = os.path.join(base_dir, "sift_base.npy")
        
        if not os.path.exists(npy_path):
            if not os.path.exists(fvecs_path):
                if not os.path.exists(tar_path):
                    print(f"Download {url}...")
                    urllib.request.urlretrieve(url, tar_path)
                print("Estrazione...")
                with tarfile.open(tar_path, "r:gz") as tf:
                    tf.extractall(path=base_dir)
            print("Conversione npy...")
            vectors = fvecs_read(fvecs_path)
            np.save(npy_path, vectors)
        else:
            all_vectors = np.load(npy_path)
        metric = "euclidean"
    else:
        raise ValueError("Dataset sconosciuto")

    if 0 < size < len(all_vectors):
        return all_vectors[:size], metric
    return all_vectors, metric

# ==============================================================================
# === RUNNER ABSTRACTION & IMPLEMENTATIONS =====================================
# ==============================================================================

class DBRunner(abc.ABC):
    @abc.abstractmethod
    def name(self) -> str: pass
    def setup(self): pass
    @abc.abstractmethod
    def index_vectors(self, index_name: str, vectors: np.ndarray, metric: str, config: dict): pass
    @abc.abstractmethod
    def search(self, index_name: str, query_vectors: np.ndarray, k: int, config: dict) -> (float, list): pass
    @abc.abstractmethod
    def cleanup(self, index_name: str): pass
    def teardown(self): pass

class KektorDBRunner(DBRunner):
    def __init__(self, host="localhost", port=9091):
        self.client = KektorDBClient(host, port)
    def name(self): return "KektorDB"
    def index_vectors(self, index_name, vectors, metric, config):
        num_vectors = len(vectors)
        precision = config.get("precision", "float32")
        try: self.client.delete_index(index_name)
        except APIError: pass

        self.client.vcreate(index_name, metric=metric, precision="float32", 
                            m=config.get("m", 16), ef_construction=config.get("ef_construction", 200))
        
        start_time = time.time()
        all_objs = [{"id": str(i), "vector": v.tolist()} for i, v in enumerate(vectors)]
        
        BATCH_SIZE = 5120
        for i in tqdm(range(0, num_vectors, BATCH_SIZE), desc=f"Indicizzazione {self.name()}"):
            self.client.vadd_batch(index_name, all_objs[i : i + BATCH_SIZE])
        
        indexing_duration = time.time() - start_time
        
        if precision != "float32":
            print(f"[{self.name()}] Compressione a {precision}...")
            compress_start = time.time()
            task = self.client.vcompress(index_name, precision)
            if task.status == 'failed': raise Exception(f"Compressione fallita: {task.error}")
            indexing_duration += (time.time() - compress_start)

        return indexing_duration
    def search(self, index_name, query_vectors, k, config):
        total_time, all_results = 0, []
        for query in tqdm(query_vectors, desc=f"Ricerca {self.name()}"):
            start_t = time.time()
            res = self.client.vsearch(index_name, query.tolist(), k=k, ef_search=config.get("ef_search", 100))
            total_time += (time.time() - start_t)
            all_results.append([int(r) for r in res])
        return len(query_vectors) / total_time, all_results
    def cleanup(self, index_name): self.client.delete_index(index_name)

class QdrantRunner(DBRunner):
    def __init__(self, host="localhost", port=6333):
        self.container_name, self.host, self.port = "qdrant-benchmark", host, port
        self.client = None
    def name(self): return "Qdrant"
    def setup(self):
        print(f"[{self.name()}] Avvio container Docker...")
        subprocess.run(["docker", "rm", "-f", self.container_name], capture_output=True)
        subprocess.run(["docker", "run", "-d", "--name", self.container_name, "-p", f"{self.port}:6333", "qdrant/qdrant"], check=True)
        print(f"[{self.name()}] Attesa di 10s per l'avvio del server...")
        time.sleep(5)
        self.client = QdrantClient(host=self.host, port=self.port, timeout=60)
    def index_vectors(self, index_name, vectors, metric, config):
        num_vectors, dims = vectors.shape
        q_metric = qdrant_models.Distance.COSINE if metric == "cosine" else qdrant_models.Distance.EUCLID
        
        # Prima controlla se la collezione esiste, poi la cancella e la ricrea.
        try:
            if self.client.has_collection(collection_name=index_name):
                self.client.delete_collection(collection_name=index_name)
        except Exception: # Ignora errori se non esiste
            pass

        self.client.create_collection(
            collection_name=index_name,
            vectors_config=qdrant_models.VectorParams(size=dims, distance=q_metric),
            hnsw_config=qdrant_models.HnswConfigDiff(m=config.get("m", 16), ef_construct=config.get("ef_construction", 200)),
        )
        
        start_time = time.time()
        print(f"[{self.name()}] Inserimento di {num_vectors} vettori (l'output del progresso è gestito dal client)...")
        self.client.upload_points(
            collection_name=index_name, 
            points=[
                qdrant_models.PointStruct(id=i, vector=v.tolist()) 
                for i, v in enumerate(vectors)
            ],
            wait=True, # Aspetta che l'indicizzazione sia completa
            parallel=os.cpu_count()
        )
        return time.time() - start_time
    def search(self, index_name, query_vectors, k, config):
        total_time, all_results = 0, []
        for query in tqdm(query_vectors, desc=f"Ricerca {self.name()}"):
            start_t = time.time()
            res = self.client.query_points(collection_name=index_name, query=query.tolist(), limit=k, 
                                           search_params=qdrant_models.SearchParams(hnsw_ef=config.get("ef_search", 100)))
            total_time += (time.time() - start_t)
            all_results.append([p.id for p in res.points])
        return len(query_vectors) / total_time, all_results
    def cleanup(self, index_name): self.client.delete_collection(collection_name=index_name)
    def teardown(self):
        print(f"[{self.name()}] Arresto container Docker...")
        subprocess.run(["docker", "rm", "-f", self.container_name], check=True, capture_output=True)

class ChromaDBRunner(DBRunner):
    def __init__(self, host="localhost", port=8000):
        self.container_name, self.host, self.port = "chroma-benchmark", host, port
        self.client = None
    
    def name(self): 
        return "ChromaDB"
    
    def setup(self):
        print(f"[{self.name()}] Avvio container Docker...")
        subprocess.run(["docker", "rm", "-f", self.container_name], capture_output=True)
        docker_command = [
            "docker", "run", "-d", "--name", self.container_name,
            "-p", f"{self.port}:8000",
            "-e", "ALLOW_RESET=TRUE", 
            "-e", "ANONYMIZED_TELEMETRY=False",
            "chromadb/chroma"
        ]
        subprocess.run(docker_command, check=True)
        print(f"[{self.name()}] Attesa di 15s per l'avvio del server...")
        time.sleep(5)  # Aumentato da 10 a 15 secondi
        
        # Inizializza il client e verifica la connessione
        self.client = chromadb.HttpClient(
            host=self.host, 
            port=self.port, 
            settings=Settings(allow_reset=True)
        )
        
        # Test di connessione
        max_retries = 5
        for i in range(max_retries):
            try:
                self.client.heartbeat()
                print(f"[{self.name()}] Connessione stabilita con successo!")
                break
            except Exception as e:
                if i < max_retries - 1:
                    print(f"[{self.name()}] Tentativo {i+1}/{max_retries} fallito, riprovo...")
                    time.sleep(3)
                else:
                    raise Exception(f"Impossibile connettersi a ChromaDB dopo {max_retries} tentativi: {e}")
    
    def index_vectors(self, index_name, vectors, metric, config):
        num_vectors = len(vectors)
        chroma_metric = "l2" if metric == "euclidean" else metric
        
        # Pulizia preventiva
        try:
            existing_collections = [c.name for c in self.client.list_collections()]
            if index_name in existing_collections:
                self.client.delete_collection(name=index_name)
                time.sleep(1)  # Aspetta che la cancellazione sia completa
        except Exception as e:
            print(f"[{self.name()}] Avviso durante pulizia: {e}")
        
        # Crea la collection con metadati semplificati
        try:
            collection = self.client.create_collection(
                name=index_name,
                metadata={"hnsw:space": chroma_metric}
            )
            print(f"[{self.name()}] Collection '{index_name}' creata con successo")
        except Exception as e:
            raise Exception(f"Errore durante la creazione della collection: {e}")
        
        # Verifica che la collection esista
        time.sleep(2)
        try:
            collection = self.client.get_collection(name=index_name)
        except Exception as e:
            raise Exception(f"La collection non è disponibile dopo la creazione: {e}")
        
        start_time = time.time()
        BATCH_SIZE = 5120
        
        for i in tqdm(range(0, num_vectors, BATCH_SIZE), desc=f"Indicizzazione {self.name()}"):
            end = min(i + BATCH_SIZE, num_vectors)
            try:
                collection.add(
                    ids=[str(j) for j in range(i, end)],
                    embeddings=vectors[i:end].tolist()
                )
            except Exception as e:
                raise Exception(f"Errore durante l'aggiunta dei vettori (batch {i}-{end}): {e}")
        
        return time.time() - start_time
    
    def search(self, index_name, query_vectors, k, config):
        try:
            collection = self.client.get_collection(name=index_name)
        except Exception as e:
            raise Exception(f"Impossibile recuperare la collection '{index_name}': {e}")
        
        total_time, all_results = 0, []
        for query in tqdm(query_vectors, desc=f"Ricerca {self.name()}"):
            start_t = time.time()
            try:
                res = collection.query(
                    query_embeddings=[query.tolist()], 
                    n_results=k
                )
                total_time += (time.time() - start_t)
                all_results.append([int(id) for id in res['ids'][0]])
            except Exception as e:
                print(f"[{self.name()}] Errore durante la ricerca: {e}")
                raise
        
        return len(query_vectors) / total_time, all_results
    
    def cleanup(self, index_name):
        try:
            existing_collections = [c.name for c in self.client.list_collections()]
            if index_name in existing_collections:
                self.client.delete_collection(name=index_name)
                print(f"[{self.name()}] Collection '{index_name}' eliminata")
        except Exception as e:
            print(f"[{self.name()}] Avviso durante cleanup: {e}")
    
    def teardown(self):
        print(f"[{self.name()}] Arresto container Docker...")
        subprocess.run(["docker", "rm", "-f", self.container_name], check=True, capture_output=True)# ==============================================================================

        
# === ORCHESTRATORE DEL BENCHMARK ===============================================
# ==============================================================================

def run_benchmark_cycle(runner: DBRunner, dataset: np.ndarray, metric: str, config: dict):
    num_vectors = len(dataset)
    index_name = f"bench-{runner.name().lower().replace(' ', '')}-{num_vectors}-{int(time.time())}"
    print(f"\n{'='*25} ESECUZIONE: {runner.name()} | {config} {'='*25}")
    try:
        runner.setup()
        indexing_time = runner.index_vectors(index_name, dataset, metric, config)
        
        NUM_QUERIES, K_SEARCH = 1000, 10
        query_indices = np.random.choice(num_vectors, NUM_QUERIES, replace=False)
        query_vectors = dataset[query_indices]
        
        print(f"Calcolo ground truth per {NUM_QUERIES} query...")
        ground_truth = []
        for query in tqdm(query_vectors, desc="Ground Truth"):
            if metric == "cosine":
                dataset_norm = dataset / np.linalg.norm(dataset, axis=1, keepdims=True)
                query_norm = query / np.linalg.norm(query)
                similarities = np.dot(dataset_norm, query_norm)
                true_indices = np.argsort(similarities)[::-1][:K_SEARCH]
            else:
                distances = np.sum((dataset - query)**2, axis=1)
                true_indices = np.argsort(distances)[:K_SEARCH]
            ground_truth.append(set(true_indices))
            
        qps, results_ids = runner.search(index_name, query_vectors, K_SEARCH, config)
        
        total_recall = sum(len(set(results_ids[i]).intersection(ground_truth[i])) / K_SEARCH for i in range(NUM_QUERIES))
        avg_recall = total_recall / NUM_QUERIES

        return {
            "Database": runner.name(), "Config": f"M={config.get('m',16)}, efC={config.get('ef_construction',200)}, efS={config.get('ef_search',100)}",
            "Precision": config.get("precision", "float32"), "Recall@10": f"{avg_recall:.4f}", "QPS": f"{qps:.2f}",
            "Index Time (s)": f"{indexing_time:.2f}"
        }
    except Exception as e:
        print(f"\n!!!!!! ERRORE durante il benchmark per {runner.name()} !!!!!!")
        print(e)
        return None
    finally:
        try: 
            print(f"[{runner.name()}] Esecuzione pulizia...")
            runner.cleanup(index_name)
        except Exception as e: print(f"[{runner.name()}] Errore durante cleanup: {e}")
        runner.teardown()

def main(args):
    dataset, metric = get_dataset(args.dataset, args.size)
    
    runners = [KektorDBRunner()]
    if QDRANT_AVAILABLE: runners.append(QdrantRunner())
    if CHROMA_AVAILABLE: runners.append(ChromaDBRunner())
    
    configurations = [
        {"m": 16, "ef_construction": 200, "ef_search": 100, "precision": "float32"},
        {"m": 16, "ef_construction": 200, "ef_search": 200, "precision": "float32"},
        {"m": 32, "ef_construction": 400, "ef_search": 150, "precision": "float32"},
         # Per confrontare più equamente con il default di Chroma
        {"m": 16, "ef_construction": 200, "ef_search": 20, "precision": "float32"},
        {"m": 16, "ef_construction": 200, "ef_search": 40, "precision": "float32"},

        # {"m": 16, "ef_construction": 200, "ef_search": 200, "precision": "int8"}, # Solo per KektorDB
    ]
    
    all_results = []
    for config in configurations:
        for runner in runners:
            if config.get("precision") != "float32" and runner.name() != "KektorDB":
                print(f"\n--- Salto {runner.name()} per precisione {config.get('precision')} (non supportata) ---")
                continue
            
            result = run_benchmark_cycle(runner, dataset, metric, config)
            if result:
                all_results.append(result)
            print("-" * 80)
    
    if not all_results:
        print("\nNessun risultato di benchmark da mostrare.")
        return

    df = pd.DataFrame(all_results)
    print("\n\n" + "="*30 + f" RISULTATI FINALI ({args.dataset}, {len(dataset)} vettori) " + "="*30)
    print(df.to_markdown(index=False))

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Benchmark unificato per database vettoriali.")
    parser.add_argument(
        "--dataset", type=str, required=True, choices=["glove-100d", "glove-300d", "sift-1m"],
        help="Nome del dataset da usare (es. 'glove-100d')."
    )
    parser.add_argument(
        "--size", type=int, default=0,
        help="Numero di vettori da usare dal dataset. 0 per usare l'intero dataset (default: 0)."
    )
    parsed_args = parser.parse_args()
    main(parsed_args)
