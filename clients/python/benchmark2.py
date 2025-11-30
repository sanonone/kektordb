# File: benchmark.py
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

# --- Import Client ---
# Assicurati che kektordb_client.py sia nella stessa cartella o nel PYTHONPATH
from kektordb_client import KektorDBClient, APIError

try:
    from qdrant_client import QdrantClient, models as qdrant_models
    QDRANT_AVAILABLE = True
except ImportError:
    QDRANT_AVAILABLE = False
    print("WARN: `qdrant-client` mancante. Qdrant saltato.")

try:
    import chromadb
    from chromadb.config import Settings
    CHROMA_AVAILABLE = True
except ImportError:
    CHROMA_AVAILABLE = False
    print("WARN: `chromadb` mancante. ChromaDB saltato.")

# ==============================================================================
# === DATASET UTILS ===========================================================
# ==============================================================================

def fvecs_read(filename, c_contiguous=True):
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
    
    # Mappatura nome dataset -> file txt
    glove_files = {
        "glove-100d": "glove.6B.100d.txt",
        "glove-200d": "glove.6B.200d.txt",
        "glove-300d": "glove.6B.300d.txt"
    }

    if name in glove_files:
        txt_filename = glove_files[name]
        url = "https://nlp.stanford.edu/data/glove.6B.zip"
        zip_path = os.path.join(base_dir, "glove.6B.zip")
        txt_path = os.path.join(base_dir, txt_filename)
        
        if not os.path.exists(txt_path):
            if not os.path.exists(zip_path):
                # Se lo zip non c'è in datasets, controlliamo nella root (il tuo caso)
                if os.path.exists("glove.6B.zip"):
                    print("Trovato zip nella root, lo sposto...")
                    os.rename("glove.6B.zip", zip_path)
                else:
                    print(f"Download {url}...")
                    urllib.request.urlretrieve(url, zip_path)
            
            print(f"Estrazione {txt_filename}...")
            with zipfile.ZipFile(zip_path, 'r') as zf:
                zf.extract(txt_filename, path=base_dir)
        
        print("Parsing vettori...")
        vectors = []
        
        # Determina la dimensione attesa dal nome (es. 100d -> 100)
        expected_dim = int(name.split('-')[1][:-1])
        
        with open(txt_path, 'r', encoding='utf-8') as f:
            for line in f:
                parts = line.split()
                try:
                    # GloVe a volte ha righe sporche, il try-except è fondamentale
                    vec = np.array(parts[1:], dtype=np.float32)
                    if len(vec) == expected_dim: 
                        vectors.append(vec)
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
# === RUNNERS =================================================================
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
        try: self.client.delete_index(index_name)
        except: pass
        
        # Create
        self.client.vcreate(index_name, metric=metric, precision="float32",
                            m=config.get("m", 16), ef_construction=config.get("ef_construction", 200))
        
        start = time.time()
        all_objs = [{"id": str(i), "vector": v.tolist()} for i, v in enumerate(vectors)]
        BATCH_SIZE = 10000 # Batch size ragionevole per HTTP JSON
        
        for i in tqdm(range(0, len(vectors), BATCH_SIZE), desc="Index KektorDB"):
            # self.client.vadd_batch(index_name, all_objs[i : i + BATCH_SIZE])
            chunk = all_objs[i : i + BATCH_SIZE]
            self.client.vadd_batch(index_name, chunk)
            
        indexing_time = time.time() - start
        
        precision = config.get("precision", "float32")
        if precision != "float32":
            print(f"Comprimo a {precision}...")
            s = time.time()
            t = self.client.vcompress(index_name, precision)
            if t.status == "failed": raise Exception(t.error)
            indexing_time += (time.time() - s)
            
        return indexing_time

    def search(self, index_name, query_vectors, k, config):
        ef_search = config.get("ef_search", 100)
        # Warmup (eseguiamo 10 query a vuoto)
        for q in query_vectors[:10]:
            self.client.vsearch(index_name, q.tolist(), k=k, ef_search=ef_search)
            
        total_time, results = 0, []
        for q in tqdm(query_vectors, desc="Search KektorDB"):
            s = time.time()
            # Nota: assicurati che il client non faccia parsing pesante nel tempo misurato se possibile
            res = self.client.vsearch(index_name, q.tolist(), k=k, ef_search=ef_search)
            total_time += (time.time() - s)
            results.append([int(r) for r in res])
        return len(query_vectors) / total_time, results

    def cleanup(self, index_name):
        try: self.client.delete_index(index_name)
        except: pass

class QdrantRunner(DBRunner):
    def __init__(self, host="localhost", port=6333):
        self.container = "qdrant-bench"
        self.host = host
        self.port = port
        self.client = None
        
    def name(self): return "Qdrant"
    
    def setup(self):
        # Usa --network host su Linux per performance reali
        subprocess.run(["docker", "rm", "-f", self.container], capture_output=True)
        # Mappiamo comunque la porta per sicurezza se non si usa host network, 
        # ma raccomandiamo --network host
        cmd = ["docker", "run", "-d", "--name", self.container, "--network", "host", "qdrant/qdrant"]
        subprocess.run(cmd, check=True)
        print("Attesa avvio Qdrant...")
        time.sleep(5)
        self.client = QdrantClient(host=self.host, port=self.port)

    def index_vectors(self, index_name, vectors, metric, config):
        dim = vectors.shape[1]
        dist = qdrant_models.Distance.COSINE if metric == "cosine" else qdrant_models.Distance.EUCLID
        
        self.client.recreate_collection(
            collection_name=index_name,
            vectors_config=qdrant_models.VectorParams(size=dim, distance=dist),
            hnsw_config=qdrant_models.HnswConfigDiff(
                m=config.get("m", 16),
                ef_construct=config.get("ef_construction", 200)
            ),
            # Disabilita ottimizzatori durante il caricamento per velocità, poi riabilita? 
            # No, lasciamo default per simulare uso reale, ma aspettiamo alla fine.
        )
        
        start = time.time()
        self.client.upload_points(
            collection_name=index_name,
            points=[qdrant_models.PointStruct(id=i, vector=v.tolist()) for i, v in enumerate(vectors)],
            wait=True, # Aspetta l'ACK di scrittura
            parallel=os.cpu_count()
        )
        
        # FIX CRITICO: Attesa esplicita indicizzazione HNSW
        print("Attesa indicizzazione background Qdrant...")
        while True:
            info = self.client.get_collection(index_name)
            if info.status == qdrant_models.CollectionStatus.GREEN:
                break
            time.sleep(3)
            
        return time.time() - start

    def search(self, index_name, query_vectors, k, config):
        ef_search = config.get("ef_search", 100)
        search_params = qdrant_models.SearchParams(hnsw_ef=ef_search)
        
        # Warmup
        for q in query_vectors[:10]:
            self.client.query_points(index_name, query=q.tolist(), limit=k, search_params=search_params)
            
        total_time, results = 0, []
        for q in tqdm(query_vectors, desc="Search Qdrant"):
            s = time.time()
            res = self.client.query_points(index_name, query=q.tolist(), limit=k, search_params=search_params)
            total_time += (time.time() - s)
            results.append([p.id for p in res.points])
        return len(query_vectors) / total_time, results

    def cleanup(self, index_name):
        self.client.delete_collection(index_name)
    
    def teardown(self):
        subprocess.run(["docker", "rm", "-f", self.container], capture_output=True)

class ChromaDBRunner(DBRunner):
    def __init__(self, host="localhost", port=8000):
        self.container = "chroma-bench"
        self.host = host
        self.port = port
        self.client = None
        
    def name(self): return "ChromaDB"
    
    def setup(self):
        subprocess.run(["docker", "rm", "-f", self.container], capture_output=True)
        cmd = [
            "docker", "run", "-d", "--name", self.container, "--network", "host",
            "-e", "ALLOW_RESET=TRUE", "-e", "ANONYMIZED_TELEMETRY=False",
            "chromadb/chroma"
        ]
        subprocess.run(cmd, check=True)
        print("Attesa avvio Chroma...")
        time.sleep(10)
        self.client = chromadb.HttpClient(host=self.host, port=self.port, settings=Settings(allow_reset=True))

    def index_vectors(self, index_name, vectors, metric, config):
        try: self.client.delete_collection(index_name)
        except: pass
        
        dist = "l2" if metric == "euclidean" else metric # Chroma usa 'cosine' o 'l2' o 'ip'
        
        # Parametri HNSW espliciti nei metadati
        metadata = {
            "hnsw:space": dist,
            "hnsw:construction_ef": config.get("ef_construction", 200),
            "hnsw:M": config.get("m", 16),
            "hnsw:search_ef": config.get("ef_search", 100) # Default search param
        }
        
        col = self.client.create_collection(name=index_name, metadata=metadata)
        
        start = time.time()
        BATCH_SIZE = 5120 # Chroma gestisce bene batch grandi
        ids_all = [str(i) for i in range(len(vectors))]
        vecs_all = vectors.tolist()
        
        for i in tqdm(range(0, len(vectors), BATCH_SIZE), desc="Index Chroma"):
            end = i + BATCH_SIZE
            col.add(ids=ids_all[i:end], embeddings=vecs_all[i:end])
            
        # Chroma non ha un'API di stato esplicita come Qdrant, ma è sincrono nell'ingestione
        # (anche se hnswlib lavora in background su thread, di solito il return di add è affidabile)
        return time.time() - start

    def search(self, index_name, query_vectors, k, config):
        col = self.client.get_collection(index_name)
        # Nota: Chroma non permette di cambiare ef_search per query facilmente via client standard
        # senza modificare la collezione. Per questo benchmark assumiamo usi il default settato in creazione.
        
        # Warmup
        for q in query_vectors[:10]:
            col.query(query_embeddings=[q.tolist()], n_results=k)
            
        total_time, results = 0, []
        for q in tqdm(query_vectors, desc="Search Chroma"):
            s = time.time()
            res = col.query(query_embeddings=[q.tolist()], n_results=k)
            total_time += (time.time() - s)
            results.append([int(i) for i in res['ids'][0]])
        return len(query_vectors) / total_time, results

    def cleanup(self, index_name):
        try: self.client.delete_collection(index_name)
        except: pass
    
    def teardown(self):
        subprocess.run(["docker", "rm", "-f", self.container], capture_output=True)

# ==============================================================================
# === MAIN LOGIC ==============================================================
# ==============================================================================

def run_test(runner, dataset, metric, config):
    print(f"\n>>> RUN: {runner.name()} | {config}")
    try:
        runner.setup()
        idx_time = runner.index_vectors("bench_idx", dataset, metric, config)
        
        # Ground Truth Calculation (Solo 500 query per velocità, ma accurate)
        NUM_Q = 1000
        idxs = np.random.choice(len(dataset), NUM_Q, replace=False)
        queries = dataset[idxs]
        
        print("Calcolo Ground Truth...")
        gt = []
        for q in queries:
            if metric == "cosine":
                # Assumiamo dataset normalizzato se cosine, o facciamo dot product raw
                # Per semplicità usiamo dot product
                sims = np.dot(dataset, q)
                gt.append(set(np.argsort(sims)[::-1][:10]))
            else:
                dists = np.sum((dataset - q)**2, axis=1)
                gt.append(set(np.argsort(dists)[:10]))
        
        qps, res_ids = runner.search("bench_idx", queries, 10, config)
        
        # Calc Recall
        recalls = []
        for i, r_set in enumerate(res_ids):
            recalls.append(len(set(r_set).intersection(gt[i])) / 10.0)
        avg_recall = np.mean(recalls)
        
        return {
            "DB": runner.name(),
            "M": config["m"], "efC": config["ef_construction"], "efS": config["ef_search"],
            "Prec": config["precision"],
            "Recall": f"{avg_recall:.4f}",
            "QPS": f"{qps:.0f}",
            "Index(s)": f"{idx_time:.1f}"
        }
    except Exception as e:
        print(f"ERROR: {e}")
        import traceback
        traceback.print_exc()
        return None
    finally:
        runner.cleanup("bench_idx")
        runner.teardown()

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--dataset", default="glove-100d", 
                        choices=["glove-100d", "glove-200d", "glove-300d", "sift-1m"])
    parser.add_argument("--size", type=int, default=50000)
    args = parser.parse_args()
    
    data, metric = get_dataset(args.dataset, args.size)
    # Normalizzazione per Cosine (KektorDB normalizza internamente, ma per coerenza ground truth)
    if metric == "cosine":
        print("Normalizzazione dataset per Cosine...")
        norms = np.linalg.norm(data, axis=1, keepdims=True)
        norms[norms==0] = 1
        data = data / norms

    configs = [
        {"m": 16, "ef_construction": 200, "ef_search": 100, "precision": "float32"}, # balanced
        #{"m": 32, "ef_construction": 400, "ef_search": 150, "precision": "float32"},
        #{"m": 32, "ef_construction": 400, "ef_search": 200, "precision": "float32"}, # quality
        #{"m": 12, "ef_construction": 150, "ef_search": 50, "precision": "float32"}, # speed
        #{"m": 8, "ef_construction": 100, "ef_search": 100, "precision": "float32"}, # less memory
        #{"m": 16, "ef_construction": 200, "ef_search": 20, "precision": "float32"},
        #{"m": 16, "ef_construction": 200, "ef_search": 40, "precision": "float32"},
    ]
    
    runners = [KektorDBRunner()]
    if QDRANT_AVAILABLE: runners.append(QdrantRunner())
    if CHROMA_AVAILABLE: runners.append(ChromaDBRunner())
    
    results = []
    for c in configs:
        for r in runners:
            res = run_test(r, data, metric, c)
            if res: results.append(res)
            
    df = pd.DataFrame(results)
    print("\n\n" + df.to_markdown(index=False))
