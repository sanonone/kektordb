# KektorDB

[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

[English](README.md) | Italiano

KektorDB è un database vettoriale **in-memory** ad alte prestazioni, sviluppato interamente in Go da zero.
Fornisce un motore basato su **HNSW** per la ricerca approssimata dei vicini più prossimi (ANN), filtri avanzati sui metadati e un accesso moderno tramite **API REST**.

---

### Motivazione e Filosofia

Questo progetto è nato come un percorso personale di apprendimento, per esplorare a fondo argomenti complessi di ingegneria del software: dall’ottimizzazione a basso livello fino all’architettura dei database.
Sebbene l’obiettivo principale fosse didattico, lo sviluppo è stato condotto con l’intento di creare uno strumento robusto e realmente utile.

Il risultato non vuole essere un concorrente “definitivo” dei database vettoriali di produzione, ma piuttosto una rappresentazione del processo di ingegneria che lo ha generato.
La missione è offrire un motore di ricerca vettoriale potente, semplice e autosufficiente, incarnando la filosofia di una **“SQLite dei database vettoriali”**.

---

### Funzionalità Principali

* **Motore HNSW personalizzato:** Implementazione da zero dell’algoritmo HNSW per ricerche ANN ad alta accuratezza, con euristiche avanzate per la selezione dei vicini.
* **Filtraggio avanzato dei metadati:** Pre-filtraggio ad alte prestazioni sui metadati. Supporta uguaglianze (`tag="cat"`), range (`price<100`) e filtri composti (`AND` / `OR`).
* **Supporto a più metriche di distanza:** Compatibile sia con **Euclidea (L2)** che con **Cosine Similarity**, configurabili per indice.
* **Compressione e Quantizzazione dei Vettori:**

  * **Float16:** Comprime gli indici euclidei del **50%**, mantenendo un recall superiore al 99%.
  * **Int8:** Quantizza gli indici con metrica Cosine riducendo lo spazio del **75%**, mantenendo oltre il 93% di recall su dataset di grandi dimensioni.
* **API ad alte prestazioni:** Interfaccia REST pulita, con operazioni batch e tuning dinamico della ricerca (`ef_search`).
* **Persistenza affidabile:** Sistema ibrido **AOF + Snapshot** che garantisce durabilità e riavvii quasi istantanei.
  Include manutenzione automatica e configurabile per snapshot e compattazione AOF.
* **Ecosistema:** Client ufficiali per **Python** e **Go** per un’integrazione semplice.

---

### Benchmark delle Prestazioni

I test sono stati eseguiti su una CPU `12th Gen Intel(R) Core(TM) i5-12500`.

#### Prestazioni End-to-End

Questi benchmark misurano le prestazioni complessive del sistema, includendo l’overhead delle API, su dataset reali.

| Dataset / Configurazione               | Vettori   | Dimensioni | Recall@10  | QPS (Query/sec) |
| -------------------------------------- | --------- | ---------- | ---------- | --------------- |
| SIFT / Euclidean `float32`             | 1,000,000 | 128        | **0.9960** | `~344`          |
| SIFT / Euclidean `float16` (Compresso) | 1,000,000 | 128        | **0.9910** | `~266`          |
| GloVe / Cosine `float32`               | 400,000   | 100        | **0.9650** | `~279`          |
| GloVe / Cosine `int8` (Quantizzato)    | 400,000   | 100        | **0.9330** | `~147`          |

*Parametri: `M=16`, `efConstruction=200`, `efSearch=100` (`efSearch=200` per `int8`).*

#### Prestazioni delle Funzioni di Distanza (Low-Level)

Questi benchmark misurano la velocità delle funzioni di distanza fondamentali su vettori a 128 dimensioni.

| Funzione                  | Implementazione       | Tempo per Operazione |
| ------------------------- | --------------------- | -------------------- |
| **Euclidean (`float32`)** | Pure Go (Compiler Opt.) | `~27 ns/op`          |
| **Cosine (`float32`)**    | `gonum` (SIMD)        | `~10 ns/op`          |
| **Euclidean (`float16`)** | Pure Go (Fallback)    | `~320 ns/op`         |
| **Euclidean (`float16`)** | Avo     (SIMD)        | `~118 ns/op`         |
| **Cosine (`int8`)**       | Pure Go (Fallback)    | `~27 ns/op`          |

*(Nota: Le prestazioni per le ricerche compresse/quantizzate (`float16` / `int8`) sono attualmente limitate dalle versioni “pure Go” delle funzioni di distanza.
Un’area chiave di miglioramento futuro sarà sostituirle con versioni accelerate via SIMD.)*

---

### 🚀 Avvio Rapido (Python)

1. **Avvia il Server KektorDB:**

   ```bash
   # Scarica il binario più recente dalla pagina Releases
   ./kektordb -http-addr=":9091"
   ```

2. **Installa il client Python:**

   ```bash
   pip install kektordb-client
   ```

3. **Usa KektorDB:**

   ```python
   from kektordb_client import KektorDBClient

   client = KektorDBClient(port=9091)
   index_name = "knowledge_base"

   client.vcreate(index_name, metric="cosine", precision="int8")

   client.vadd(
       index_name=index_name,
       item_id="doc1",
       vector=[0.1, 0.8, 0.3],
       metadata={"source": "manual_v1.pdf", "page": 42}
   )

   results = client.vsearch(
       index_name=index_name,
       k=1,
       query_vector=[0.15, 0.75, 0.35],
       filter_str='source=manual_v1.pdf AND page>40'
   )

   print(f"Risultati trovati: {results}")
   ```

---

### 📚 Riferimento API

#### Key-Value Store

* `GET /kv/{key}`: Recupera un valore.
* `POST /kv/{key}`: Imposta un valore. Corpo: `{"value": "..."}`.
* `DELETE /kv/{key}`: Elimina una chiave.

#### Gestione Indici

* `GET /vector/indexes`: Elenca tutti gli indici.
* `GET /vector/indexes/{name}`: Restituisce informazioni dettagliate su un singolo indice.
* `DELETE /vector/indexes/{name}`: Elimina un indice.

#### Operazioni sui Vettori

* `POST /vector/actions/create`: Crea un nuovo indice vettoriale.
  Corpo: `{"index_name": "...", "metric": "...", "precision": "...", "m": ..., "ef_construction": ...}`
* `POST /vector/actions/add`: Aggiunge un singolo vettore.
  Corpo: `{"index_name": "...", "id": "...", "vector": [...], "metadata": {...}}`
* `POST /vector/actions/add-batch`: Aggiunge più vettori in batch.
  Corpo: `{"index_name": "...", "vectors": [{"id": ..., "vector": ...}, ...]}`
* `POST /vector/actions/search`: Esegue una ricerca vettoriale.
  Corpo: `{"index_name": "...", "k": ..., "query_vector": [...], "filter": "...", "ef_search": ...}`
* `POST /vector/actions/delete_vector`: Elimina un vettore.
  Corpo: `{"index_name": "...", "id": "..."}`
* `POST /vector/actions/get-vectors`: Recupera dati per più vettori tramite ID.
  Corpo: `{"index_name": "...", "ids": ["...", "..."]}`
* `POST /vector/actions/compress`: Comprime un indice a una precisione inferiore.
  Corpo: `{"index_name": "...", "precision": "..."}`

#### Sistema

* `POST /system/save`: Esegue un salvataggio (snapshot) del database.
* `POST /system/aof-rewrite`: Avvia una compattazione dell’AOF.
* `GET /system/tasks/{id}`: Restituisce lo stato di un task asincrono.
* `GET /debug/pprof/*`: Esporta gli endpoint di profiling `pprof` di Go.

---

### 🛣 Roadmap e Lavori Futuri

KektorDB è un progetto in continuo sviluppo. I prossimi passi si concentreranno su:

* **Ricerca Ibrida (BM25):** Integrazione di un indice full-text per combinare rilevanza semantica e per parole chiave.
* **Ottimizzazioni delle Prestazioni:** Sostituzione delle versioni “pure Go” per `float16` e `int8` con implementazioni SIMD ad alte prestazioni (probabilmente tramite `avo`).
* **Distribuzione Mobile/Edge:** Esplorazione di una modalità di build che compili KektorDB come libreria condivisa per l’integrazione con framework mobili come Flutter.

---

### Licenza

Distribuito sotto licenza **Apache 2.0**.
Consulta il file `LICENSE` per maggiori dettagli.



