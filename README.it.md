# KektorDB 

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="250">
</p>

[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

[English](README.md) | [Italiano](README.it.md)

**KektorDB è un database vettoriale/chiave-valore ad alte prestazioni, in-memory, costruito da zero in Go. Fornisce un potente motore HNSW per la ricerca vettoriale, un sistema di ricerca ibrido con ranking BM25, filtraggio avanzato dei metadati e una moderna API REST.**

### Motivazione e Filosofia

Questo progetto è nato come un'impresa personale di apprendimento per approfondire argomenti complessi di ingegneria del software. L'obiettivo era costruire un motore di ricerca robusto, autonomo e consapevole delle dipendenze, incarnando la filosofia del **"SQLite dei Database Vettoriali"**.

KektorDB è disponibile sia come server standalone che come libreria Go incorporabile (`pkg/core`), rendendolo uno strumento flessibile per sviluppatori Go e applicazioni AI/ML che richiedono capacità di ricerca vettoriale veloce e locale.

---

### ✨ Funzionalità Principali

*   **Motore HNSW Personalizzato:** Un'implementazione da zero dell'algoritmo HNSW con un'euristica avanzata di selezione dei vicini per grafi di alta qualità.
*   **Motore di Ricerca Ibrido:**
    *   **Ricerca Full-Text:** Un motore di analisi del testo integrato (supporto per inglese e italiano) e indice invertito per la ricerca di parole chiave.
    *   **Ranking BM25:** I risultati della ricerca testuale sono classificati per rilevanza utilizzando l'algoritmo standard del settore BM25.
    *   **Fusione dei Punteggi:** Le query ibride combinano punteggi vettoriali e testuali utilizzando un parametro `alpha` configurabile per una classifica unificata.
*   **Sincronizzazione Automatica degli Embedding (Vectorizer):** Un servizio in background che monitora le fonti di dati (come directory del filesystem), genera automaticamente embedding tramite API esterne (come Ollama) e mantiene l'indice di ricerca continuamente aggiornato.
*   **Filtraggio Avanzato dei Metadati:** Pre-filtraggio ad alte prestazioni sui metadati. Supporta uguaglianza, intervalli (`price<100`) e filtri composti (`AND`/`OR`).
*   **Compressione e Quantizzazione dei Vettori:**
    *   **Float16:** Comprime gli indici Euclidei del **50%**.
    *   **Int8:** Quantizza gli indici Cosine del **75%**.
*   **API ad Alte Prestazioni ed Ecosistema:**
    *   Una API REST pulita con operazioni batch, gestione asincrona delle task e tuning dinamico della ricerca.
    *   Client ufficiali per **Python** e **Go**.
*   **Persistenza Affidabile:** Un sistema ibrido **AOF + Snapshot** con manutenzione automatica in background garantisce durabilità e riavvii quasi istantanei.
*   **Motore di Calcolo Doppio (Go-nativo vs. Rust-accelerato):**
    *   **Build Predefinita:** Una versione pura in Go che sfrutta `gonum` e `avo` per l'accelerazione SIMD, garantendo massima portabilità e compilazione semplice (`go build`).
    *   **Build Prestazionale:** Una modalità di build opzionale (`-tags rust`) che collega una libreria Rust tramite CGO per calcoli di distanza SIMD altamente ottimizzati.

---

### Benchmark delle Prestazioni

I benchmark sono stati eseguiti su una CPU `12th Gen Intel(R) Core(TM) i5-12500`. KektorDB può essere compilato in due modalità: una versione **Pura Go** per massima portabilità e una versione **Rust-accelerata** (`-tags rust`) per massima performance.

#### Prestazioni di Ricerca End-to-End (QPS & Recall)

Questi benchmark misurano le prestazioni complete del sistema (Query Per Secondo) e l'accuratezza (Recall@10) su dataset reali.

**Build Pura Go (`gonum`-accelerata)**
| Dataset / Configurazione                | Vettori     | Dimensioni | Recall@10 | QPS (Query/sec) |
|----------------------------------------|-------------|------------|-----------|-------------------|
| SIFT / Euclidean `float32`             | 1,000,000   | 128        | **0.9960**  | `~344`            |
| SIFT / Euclidean `float16` (Compresso)  | 1,000,000   | 128        | **0.9910**  | `~266`            |
| GloVe / Cosine `float32`               | 400,000     | 100        | **0.9650**  | `~279`            |
| GloVe / Cosine `int8` (Quantizzato)      | 400,000     | 100        | **0.9330**  | `~147`            |

**Build Rust-Accelerata (`-tags rust`)**
| Dataset / Configurazione                | Vettori     | Dimensioni | Recall@10 | QPS (Query/sec) |
|----------------------------------------|-------------|------------|-----------|-------------------|
| SIFT / Euclidean `float32`             | 1,000,000   | 128        | **0.9960**  | `~344`            |
| SIFT / Euclidean `float16` (Compresso)  | 1,000,000   | 128        | **0.9960**  | `~298`            |
| GloVe / Cosine `float32`               | 400,000     | 100        | **0.9700**  | `~285`            |
| GloVe / Cosine `int8` (Quantizzato)      | 400,000     | 100        | **0.9550**  | `~151`            |

*Parametri: `M=16`, `efConstruction=200`, `efSearch=100` (`efSearch=200` per `int8`).*

#### Prestazioni di Calcolo delle Distanze a Basso Livello (Tempo per Operazione)

Questi benchmark misurano la velocità grezza delle funzioni di distanza principali su diverse dimensioni dei vettori. Più basso è meglio.

**Build Pura Go (`gonum & avo`-accelerata)** `(ns/op)`
| Dimensioni                | 64D   | 128D  | 256D  | 512D  | 1024D | 1536D |
|---------------------------|-------|-------|-------|-------|-------|-------|
| **Euclidean (`float32`)** | 18.16 | 41.96 | 95.69 | 209.8 | 437.0 | 662.3 |
| **Cosine (`float32` `gonum`)**    | 5.981  | 8.465  | 14.13 | 27.74 | 61.19 | 89.89 |
| **Euclidean (`float16` `avo`)** | 109.5 | 117.6 | 138.7 | 172.7  | 250.7  | 314.5  |
| **Cosine (`int8`)**       | 22.72 | 57.09 | 95.08 | 178.3 | 336.8 | -     |

**Build Rust-Accelerata (`-tags rust`)** `(ns/op)`
| Dimensioni                | 64D   | 128D  | 256D  | 512D  | 1024D | 1536D |
|---------------------------|-------|-------|-------|-------|-------|-------|
| **Euclidean (`float32`)** | 18.37 | 43.69 | 61.55 | 104.0 | 168.9 | 242.9 |
| **Cosine (`float32`)**    | 5.932 | 10.24 | 14.10 | 28.20 | 53.88 | 82.73 |
| **Euclidean (`float16`)** | 51.63 | 54.01 | 68.00 | 98.57 | 185.8 | 254.3 |
| **Cosine (`int8`)**       | 21.13 | 47.59 | 46.81 | 50.40 | 65.39 | -     |

*(Nota: La logica "Smart Dispatch" nella build Rust-accelerata seleziona automaticamente la migliore implementazione—Go, Gonum o Rust—per ogni operazione in base alle dimensioni dei vettori. Le versioni pure Go `float16` e `int8` servono come fallback portatili.)*

---

### 🚀 Avvio Rapido (Python)

Questo esempio dimostra un workflow completo: creazione di più indici, inserimento batch di dati con metadati e esecuzione di una potente ricerca ibrida.

1.  **Avvia il Server KektorDB:**
    ```bash
    # Scarica il binario più recente dalla pagina delle Release
    ./kektordb -http-addr=":9091"
    ```

2.  **Installa il Client Python e le Dipendenze:**
    ```bash
    pip install kektordb-client sentence-transformers
    ```

3.  **Usa KektorDB nel tuo script Python:**

    ```python
    from kektordb_client import KektorDBClient, APIError
    from sentence_transformers import SentenceTransformer

    # 1. Inizializza il client e il modello di embedding
    client = KektorDBClient(port=9091)
    model = SentenceTransformer('all-MiniLM-L6-v2') 
    index_name = "quickstart_index"

    # 2. Crea un nuovo indice per la demo
    try:
        client.delete_index(index_name)
        print(f"Rimosso vecchio indice '{index_name}'.")
    except APIError:
        pass # L'indice non esisteva, va bene.

    client.vcreate(
        index_name, 
        metric="cosine", 
        text_language="english"
    )
    print(f"Indice '{index_name}' creato.")
    
    # 3. Prepara e indicizza alcuni documenti in un unico batch
    documents = [
        {"id": "doc_go", "text": "Go is a language designed at Google for efficient software.", "year": 2012},
        {"id": "doc_rust", "text": "Rust is a language focused on safety and concurrency.", "year": 2015},
        {"id": "doc_python", "text": "Python is a high-level, general-purpose programming language. Its design philosophy emphasizes code readability.", "year": 1991},
    ]
    
    batch_payload = []
    for doc in documents:
        batch_payload.append({
            "id": doc["id"],
            "vector": model.encode(doc["text"]).tolist(),
            "metadata": {"content": doc["text"], "year": doc["year"]}
        })
    client.vadd_batch(index_name, batch_payload)
    print(f"{len(batch_payload)} documenti indicizzati.")
    
    
    # 4. Esegui una ricerca ibrida
    query = "a safe and concurrent language"
    print(f"\nRicerca per: '{query}'")

    results = client.vsearch(
        index_name=index_name,
        k=1,
        query_vector=model.encode(query).tolist(),
        # Trova documenti contenenti "language" ma solo quelli dopo il 2010
        filter_str='CONTAINS(content, "language") AND year > 2010',
        alpha=0.7 # Dai più peso alla similarità vettoriale
    )

    print(f"Risultati trovati: {results}")

    # 5. Verifica il risultato
    if results and results[0] == "doc_rust":
        print("\nAvvio Rapido riuscito! Il documento più rilevante è stato trovato correttamente.")
    else:
        print("\nAvvio Rapido fallito. Il documento atteso non era il risultato principale.")

    # 6. Recupera i dati completi per il risultato principale
    if results:
        top_result_data = client.vget(index_name, results[0])
        print("\n--- Dati del Risultato Principale ---")
        print(f"ID: {top_result_data.get('id')}")
        print(f"Metadati: {top_result_data.get('metadata')}")
        print("-----------------------")
    ```

---
### Riferimento API

#### Archivio Key-Value
- `GET /kv/{key}`: Recupera un valore.
- `POST /kv/{key}`: Imposta un valore. Body: `{"value": "..."}`.
- `DELETE /kv/{key}`: Elimina una chiave.

#### Gestione Indici
- `GET /vector/indexes`: Elenca tutti gli indici.
- `GET /vector/indexes/{name}`: Ottiene informazioni dettagliate per un singolo indice.
- `DELETE /vector/indexes/{name}`: Elimina un indice.

#### Azioni Vettoriali (Stile RPC)
- `POST /vector/actions/create`: Crea un nuovo indice vettoriale.
  - Body: `{"index_name": "...", "metric": "...", "precision": "...", "text_language": "...", "m": ..., "ef_construction": ...}`
- `POST /vector/actions/add`: Aggiunge un singolo vettore.
  - Body: `{"index_name": "...", "id": "...", "vector": [...], "metadata": {...}}`
- `POST /vector/actions/add-batch`: Aggiunge più vettori.
  - Body: `{"index_name": "...", "vectors": [{"id": ..., "vector": ...}, ...]}`
- `POST /vector/actions/search`: Esegue una ricerca vettoriale ibrida.
  - Body: `{"index_name": "...", "k": ..., "query_vector": [...], "filter": "...", "ef_search": ..., "alpha": ...}`
- `POST /vector/actions/delete_vector`: Elimina un singolo vettore.
  - Body: `{"index_name": "...", "id": "..."}`
- `POST /vector/actions/get-vectors`: Recupera dati per più vettori per ID.
  - Body: `{"index_name": "...", "ids": ["...", "..."]}`
- `POST /vector/actions/compress`: Comprime asincronamente un indice.
  - Body: `{"index_name": "...", "precision": "..."}`

#### Recupero Dati
- `GET /vector/indexes/{name}/vectors/{id}`: Recupera dati per un singolo vettore.

#### Sistema
- `POST /system/save`: Attiva uno snapshot del database.
- `POST /system/aof-rewrite`: Attiva una compattazione AOF asincrona.
- `GET /system/tasks/{id}`: Ottiene lo stato di una task asincrona.
- `GET /debug/pprof/*`: Espone endpoint di profilazione Go pprof.

---

### Documentazione

Per una guida completa a tutte le funzionalità e endpoint API, consulta la **[Documentazione Completa](https://github.com/sanonone/kektordb/blob/main/DOCUMENTATION.md)**.

---

### 🛣️ Roadmap & Lavoro Futuro

KektorDB è in sviluppo attivo. La roadmap è divisa in priorità a breve termine per la prossima major release e ambizioni a lungo termine.

#### **Obiettivi a Breve Termine**

Queste sono le funzionalità e i miglioramenti di priorità più alta pianificati per le prossime release:

*   **Archivio KV Migliorato:** Espansione del semplice archivio key-value in un componente più ricco di funzionalità con supporto per tipi di dati avanzati e transazioni.
*   **API gRPC:** Introduzione di un'interfaccia gRPC accanto a REST per comunicazioni ad alte prestazioni e bassa latenza in ambienti microservizi.
*   **Stabilità e Rafforzamento:** Un ciclo di sviluppo dedicato al miglioramento della robustezza complessiva del database. Questo coinvolgerà test estensivi, raffinamento della gestione degli errori e garanzia di consistenza transazionale per operazioni critiche.

#### **Visione a Lungo Termine (Idee Esplorative)**

Queste sono funzionalità ambiziose considerate per l'evoluzione a lungo termine del progetto.

*   **Architettura Modulare:** Rifattorizzazione del sistema per supportare un'architettura basata su plugin, permettendo di aggiungere nuove funzionalità (come diversi tipi di indici o fonti di dati) come moduli.
*   **Miglioramenti delle Prestazioni:** Uno sforzo focalizzato per ottimizzare il motore core.
*   **Strategia On-Device & Embedded:** Indagine sulla compilazione del motore core di KektorDB in una libreria nativa portatile. L'obiettivo è fornire binding semplici per vari ecosistemi mobile e edge, permettendo agli sviluppatori di incorporare un motore di ricerca vettoriale potente, privato e offline-capable direttamente nelle loro applicazioni.
*   **Scalabilità Orizzontale (Replica Lettura):** Implementazione di un modello semplice di replica primary-replica. 

---

### Licenza

Licenziato sotto la Licenza Apache 2.0. Vedi il file `LICENSE` per dettagli.
