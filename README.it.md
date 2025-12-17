# KektorDB

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Docker](https://img.shields.io/docker/pulls/sanonone/kektordb)](https://hub.docker.com/r/sanonone/kektordb)

[English](README.md) | [Italiano](README.it.md)

> [!TIP]
> **Supporto Docker:** Preferisci i container? Un `Dockerfile` è incluso nella root per costruire le tue immagini.

**KektorDB è un database vettoriale in-memory, integrabile (embeddable), progettato per semplicità e velocità.**

Colma il divario tra le librerie HNSW grezze e i complessi database distribuiti. Scritto in puro Go, funziona come un singolo binario o si integra direttamente nella tua applicazione, fornendo una **Pipeline RAG completa**, **Ricerca Ibrida** e **Persistenza** pronte all'uso.

> *Costruito con la filosofia di SQLite: opzione serverless, zero dipendenze e facile da gestire.*

---

## Perché KektorDB?

La maggior parte dei database vettoriali richiede container Docker, dipendenze esterne o pipeline Python solo per indicizzare pochi documenti. KektorDB adotta un approccio diverso: **"Batteries Included"**.

*   **Zero Setup:** Nessuno script Python richiesto. Punta KektorDB su alcune cartelle di file PDF/Markdown/Code/Text e lui gestisce chunking, embedding e indicizzazione automaticamente.
*   **Embeddable:** Stai costruendo un'app in Go? Importa `pkg/engine` ed esegui il database all'interno del tuo processo. Nessun overhead di rete.
*   **AI Gateway:** Agisce come un proxy trasparente per client OpenAI/Ollama (come **Open WebUI**). Inietta automaticamente il contesto RAG e mette in cache le risposte senza che tu debba scrivere una sola riga di codice.

---

## Casi d'Uso

KektorDB non è progettato per sostituire cluster distribuiti che gestiscono miliardi di vettori. Invece, brilla in scenari specifici ad alto valore:

### 1. RAG Locale & Knowledge Base
Perfetto per applicazioni desktop o agenti AI locali.
*   **Scenario:** Hai alcune cartelle di documentazione (`.md`, `.pdf`).
*   **Soluzione:** KektorDB monitora le cartelle, sincronizza le modifiche istantaneamente e fornisce un'API di ricerca con finestra di contesto automatica (chunk prev/next).
*   **Vantaggio:** Il tempo di setup scende a minuti.

### 2. Ricerca Go Integrata (Embedded)
Ideale per sviluppatori Go che costruiscono monoliti o microservizi.
*   **Scenario:** Devi aggiungere ricerca semantica al tuo backend Go (es. "Trova prodotti simili").
*   **Soluzione:** `import "github.com/sanonone/kektordb/pkg/engine"`.
*   **Vantaggio:** Zero complessità di deployment. Il DB vive e muore con il processo della tua app.

### 3. Backend RAG "Drop-in"
Collega la tua interfaccia AI preferita direttamente ai tuoi dati.
*   **Scenario:** Vuoi chattare con i tuoi documenti locali usando una UI come **Open WebUI**, ma non vuoi configurare pipeline complesse.
*   **Soluzione:** Configura la tua UI per usare `http://localhost:9092/v1` (Proxy KektorDB) invece dell'LLM direttamente.
*   **Vantaggio:** KektorDB intercetta la chat, inietta il contesto rilevante dai tuoi file nel prompt e lo inoltra all'LLM.

---

## RAG Zero-Code (Integrazione Open WebUI)

KektorDB può funzionare come **middleware intelligente** tra la tua Chat UI e il tuo LLM. Intercetta le richieste, esegue il retrieval e inietta il contesto automaticamente.

**Architettura:**
`Open WebUI` -> `Proxy KektorDB (9092)` -> `Ollama / LocalAI (11434)`

**Come configurarlo:**

1.  **Avvia KektorDB** con il proxy abilitato:
    ```bash
    ./kektordb -vectorizers-config vectorizers.yaml -enable-proxy -proxy-target "http://localhost:11434"
    ```
2.  **Configura Open WebUI:**
    *   Vai su **Impostazioni > Connessioni**.
    *   Aggiungi una connessione **OpenAI Compatible**.
    *   Base URL: `http://localhost:9092/v1`
    *   API Key: `kektor` (o qualsiasi stringa).
3.  **Chatta:** Fai semplicemente domande sui tuoi documenti. KektorDB gestisce il resto.

👉 **[Leggi la Guida Completa: Creare un sistema RAG Veloce con Open WebUI](docs/guides/zero_code_rag.md)**

---

## ✨ Funzionalità Principali

*   **Motore HNSW:** Implementazione personalizzata ottimizzata per letture ad alta concorrenza.
*   **Ricerca Ibrida:** Combina Similarità Vettoriale + BM25 (Parole Chiave) + Filtri sui Metadati.
*   **Motore a Grafo (GraphRAG):** Supporta la creazione di relazioni semantiche arbitrarie tra i vettori. La pipeline RAG usa questa funzionalità per collegare automaticamente i chunk sequenziali (`prev`/`next`), ma l'utente può definire qualsiasi tipo di collegamento custom via API.
*   **Efficienza della Memoria:** Supporta **Quantizzazione Int8** (risparmio RAM del 75%) e **Float16**.
*   **Ingestione Automatizzata:** Pipeline integrate per file PDF, Testo e Codice.
*   **Persistenza:** Un sistema ibrido **AOF + Snapshot** garantisce la durabilità dei dati tra i riavvii.
*   **Manutenzione & Ottimizzazione:**
    *   **Vacuum:** Un processo in background che ripulisce i nodi cancellati per recuperare memoria e riparare le connessioni del grafo.
    *   **Refine:** Un ottimizzatore continuo che rivaluta le connessioni del grafo per migliorare la qualità della ricerca (recall) nel tempo.
*   **AI Gateway & Middleware:** Agisce come un proxy intelligente per client compatibili con OpenAI/Ollama. Include **Caching Semantico** per fornire risposte istantanee a query ricorrenti (risparmiando costi e tempo) e un **Firewall Semantico** per bloccare prompt malevoli basandosi sulla similarità vettoriale, indipendentemente dalla pipeline RAG.
*   **Doppia Modalità:** Eseguibile come **Server REST** standalone o come **Libreria Go**.

---

## Benchmark Preliminari

I benchmark sono stati eseguiti su una macchina Linux locale (Hardware Consumer, Intel i5-12500). Il confronto è stato fatto con **Qdrant** e **ChromaDB** (via Docker con host networking) per garantire una base equa.

> **Disclaimer:** Fare benchmark sui database è complesso. Questi risultati riflettono uno scenario specifico (**single-node, read-heavy, client Python**) sulla mia macchina di sviluppo. Sono intesi per dimostrare le capacità di KektorDB come motore embedded ad alte prestazioni, non per rivendicare una superiorità assoluta in scenari di produzione distribuiti.

#### 1. Carico di Lavoro NLP (GloVe-100d, Coseno)
*400k vettori, precisione float32.*
KektorDB sfrutta l'Assembly Go ottimizzato (Gonum) per la similarità del Coseno. In questo setup, mostra un throughput molto elevato.

| Database | Recall@10 | **QPS (Query/sec)** | Tempo Indicizzazione (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9664 | **1073** | 102.9s |
| Qdrant | 0.9695 | 848 | **32.3s** |
| ChromaDB | 0.9519 | 802 | 51.5s |

#### 2. Carico di Lavoro Computer Vision (SIFT-1M, Euclidea)
*1 Milione di vettori, precisione float32.*
KektorDB usa un motore ibrido Go/Rust (`-tags rust`) per questo test. Nonostante l'overhead di CGO per i vettori 128d, le prestazioni sono competitive con i motori nativi C++/Rust.

| Database | Recall@10 | **QPS (Query/sec)** | Tempo Indicizzazione (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9906 | **881** | 481.4s |
| Qdrant | 0.998 | 845 | **88.5s** |
| ChromaDB | 0.9956 | 735 | 211.2s |

> *Nota sulla Velocità di Indicizzazione:* KektorDB è attualmente più lento nell'ingestione rispetto ai motori maturi. Questo è dovuto in parte al fatto che costruisce il grafo interrogabile completo immediatamente all'inserimento, ma soprattutto all'attuale architettura a grafo singolo. **Ottimizzare l'ingestione massiva è la priorità assoluta per la prossima major release.**

> [!TIP]
> **Ottimizzazione delle prestazioni: "Ingerisci velocemente, perfeziona dopo"**
>
> Se devi indicizzare rapidamente dataset di grandi dimensioni, crea l'indice con un `ef_construction` inferiore (ad esempio, 40). Questo riduce significativamente i tempi di indicizzazione.
> Puoi quindi abilitare il processo di **raffinamento** in background con una qualità target più elevata (ad esempio, 200). KektorDB ottimizzerà progressivamente le connessioni del grafico in background, rimanendo comunque disponibile per le query.

#### Efficienza della Memoria (Compressione e Quantizzazione)
KektorDB offre un risparmio di memoria significativo tramite quantizzazione e compressione, permettendo di caricare dataset più grandi in RAM con impatto minimo su prestazioni o recall.

| Scenario | Config | Impatto Memoria | QPS | Recall |
| :--- | :--- | :--- | :--- | :--- |
| **NLP (GloVe-100d)** | Float32 | 100% (Baseline) | ~974 | 0.971 |
| | **Int8** | **~25%** | ~767 | 0.908 |
| **Vision (SIFT-1M)** | Float32 | 100% (Baseline) | ~753 | 0.990 |
| | **Float16** | **~50%** | **~785** | 0.980 |

*(La logica di "Smart Dispatch" nella build accelerata con Rust seleziona automaticamente l'implementazione migliore—Go, Gonum o Rust—per ogni operazione in base alle dimensioni del vettore. Le versioni pure Go `float16` e `int8` fungono da fallback portatili.)*

[Report Benchmark Completo](BENCHMARKS.md)

---

## Installazione

### Come Server (Docker)
Il modo più semplice per eseguire KektorDB.

```bash
docker run -p 9091:9091 -p 9092:9092 -v $(pwd)/data:/data sanonone/kektordb:latest
```

### Come Server (Binario)
Scarica il binario pre-compilato dalla pagina delle [Releases](https://github.com/sanonone/kektordb/releases).

```bash
# Linux/macOS
./kektordb
```

> **Nota di Compatibilità:** Tutto lo sviluppo e i test sono stati eseguiti su **Linux (x86_64)**. Le build Pure Go dovrebbero funzionare su Windows/Mac/ARM.

---

## Utilizzo come Libreria Go (Embedded)

Uno degli obiettivi principali di KektorDB è la facilità di integrazione. Puoi importare l'engine direttamente nella tua applicazione Go, eliminando la necessità di servizi esterni o container.

```bash
go get github.com/sanonone/kektordb
```

```go
package main

import (
	"fmt"
	"github.com/sanonone/kektordb/pkg/core/distance"
	"github.com/sanonone/kektordb/pkg/engine"
)

func main() {
	// 1. Inizializza l'Engine (gestisce la persistenza automaticamente)
	opts := engine.DefaultOptions("./kektor_data")
	db, err := engine.Open(opts)
	if err != nil { panic(err) }
	defer db.Close()

	// 2. Crea Indice
	db.VCreate("products", distance.Cosine, 16, 200, distance.Float32, "english")

	// 3. Aggiungi Dati
	db.VAdd("products", "p1", []float32{0.1, 0.2}, map[string]any{"category": "electronics"})

	// 4. Cerca
	results, _ := db.VSearch("products", []float32{0.1, 0.2}, 10, "category=electronics", 100, 0.5)
	fmt.Println("ID Trovati:", results)
}
```
---

### 🚀 Quick Start (Python)

Questo esempio mostra un flusso completo: creazione indice, inserimento batch con metadati e Ricerca Ibrida (Vettoriale + Parole chiave).

1.  **Installa Client e Utilità:**
    ```bash
    pip install kektordb-client sentence-transformers
    ```

2.  **Esegui lo script:**

    ```python
    from kektordb_client import KektorDBClient
    from sentence_transformers import SentenceTransformer

    # 1. Inizializzazione
    client = KektorDBClient(port=9091)
    model = SentenceTransformer('all-MiniLM-L6-v2') 
    index = "quickstart"

    # 2. Crea Indice (Abilita Hybrid Search)
    try: client.delete_index(index)
    except: pass
    client.vcreate(index, metric="cosine", text_language="english")

    # 3. Inserimento Dati (Batch)
    docs = [
        {"text": "Go è efficiente per i backend.", "type": "code"},
        {"text": "Rust garantisce sicurezza della memoria.", "type": "code"},
        {"text": "La pizza margherita è un classico.", "type": "food"},
    ]
    
    batch = []
    for i, doc in enumerate(docs):
        batch.append({
            "id": f"doc_{i}",
            "vector": model.encode(doc["text"]).tolist(),
            "metadata": {"content": doc["text"], "category": doc["type"]}
        })
    
    client.vadd_batch(index, batch)
    print(f"Indicizzati {len(batch)} documenti.")

    # 4. Ricerca (Ibrida: Vettore + Filtro Metadati)
    # Cerchiamo "linguaggi veloci" MA filtriamo solo la categoria 'code'
    query_vec = model.encode("linguaggi veloci").tolist()
    
    results = client.vsearch(
        index,
        query_vector=query_vec,
        k=2,
        filter_str="category='code'", # Filtro sui Metadati
        alpha=0.7 # 70% Similarità vettoriale, 30% Keywords
    )

    print(f"Miglior Risultato ID: {results[0]}")
    ```

---

### 🦜 Integrazione con LangChain

KektorDB include un wrapper integrato per **LangChain Python**, permettendoti di inserirlo direttamente nelle tue pipeline AI esistenti.

```python
from kektordb_client.langchain import KektorVectorStore
# Vedi clients/python/README.md per i dettagli completi
```

---

## Riferimento API (Sommario)

Per una guida completa a tutte le funzionalità ed endpoint, vedi la **[Documentazione Completa](DOCUMENTATION.md)**.

*   `POST /vector/actions/import`: Caricamento massivo ad alta velocità.
*   `POST /vector/actions/search`: Ricerca vettoriale ibrida.
*   `POST /vector/actions/add`: Aggiungi singolo vettore.
*   `POST /graph/actions/link`: Crea relazioni semantiche.
*   `POST /rag/retrieve`: Ottieni chunk di testo per RAG.
*   `GET /system/tasks/{id}`: Monitora task a lunga esecuzione.
*   `POST /system/save`: Snapshot manuale.

---

## 🛣️ Roadmap

KektorDB è un progetto giovane in sviluppo attivo.

*   **Breve Termine:** Roaring Bitmaps per i filtri, Snapshot Nativo, Rifinitura Concorrenza.
*   **Lungo Termine:** Indici su Disco, Replicazione.

---

## 🤝 Come Contribuire

**KektorDB è un progetto personale nato dal desiderio di imparare le logiche interne dei database vettoriali.**

Come unico maintainer, ho costruito questo motore per esplorare CGO, SIMD e le ottimizzazioni Go a basso livello. Sono orgoglioso delle prestazioni raggiunte finora, ma so che c'è sempre un modo migliore di scrivere codice.

Se noti race conditions, ottimizzazioni mancate o pattern Go non idiomatici, **per favore apri una Issue o una PR**. Considero ogni contributo come un'opportunità di apprendimento e cerco persone che vogliano costruire questo progetto insieme.

### Aree di Contributo
Il progetto è attualmente in `v0.4.0`. Apprezzerei aiuto con:

1.  **Ottimizzazione Core:** Revisione dell'implementazione HNSW e delle strategie di locking.
2.  **Funzionalità:** Implementare Roaring Bitmaps o Graph Healing (vedi Roadmap).
3.  **Client:** Rendere i client Python/Go più idiomatici.
4.  **Testing:** Aggiungere test per casi limite e fuzzing.

### Setup di Sviluppo
1.  Forka il repository.
2.  Clona il tuo fork.
3.  Esegui `make test` per assicurarti che tutto funzioni.
4.  Crea un feature branch.
5.  Fai il commit e apri una **Pull Request**.

---

### Licenza

Rilasciato sotto Licenza Apache 2.0. Vedi il file `LICENSE` per i dettagli.
