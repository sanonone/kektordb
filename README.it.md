# KektorDB

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![GitHub Sponsors](https://img.shields.io/badge/Sponsor-sanonone-pink?logo=github)](https://github.com/sponsors/sanonone)
[![Ko-fi](https://img.shields.io/badge/Support%20me-Ko--fi-orange?logo=ko-fi)](https://ko-fi.com/sanon)
[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

<p align="center">
  <a href="DOCUMENTATION.md">📚 Documentation</a> •
  <a href="CONTRIBUTING.md">🤝 Contributing</a> •
  <a href="docs/guides/zero_code_rag.md">🤖 RAG Open WebUI Guide</a>
</p>

[English](README.md) | [Italiano](README.it.md)

> [!TIP]
> **Supporto Docker:** Preferisci i container? Un `Dockerfile` è incluso nella root per creare le tue immagini.

**KektorDB è un database vettoriale in-memory integrabile che agisce come Livello di Memoria per Agenti AI Locali.**

Scritto in puro Go, colma il divario tra librerie HNSW grezze e complessi sistemi distribuiti. Fornisce una **Pipeline RAG Agentica** completa, una **Memoria a Grafo Semantico** e una **Dashboard Visiva** pronte all'uso, eseguite come un singolo binario senza dipendenze esterne.

> *Costruito con la filosofia di SQLite: opzione serverless, zero dipendenze e facile da gestire.*

<p align="center">
  <img src="docs/images/kektordb-demo.gif" alt="Demo Grafo KektorDB" width="800">
</p>

---

## Perché KektorDB?

La maggior parte dei database vettoriali sono solo motori di archiviazione. Per costruire un sistema RAG, devi comunque scrivere codice Python complesso per logiche di chunking, embedding e recupero. KektorDB adotta un approccio diverso: **"Batterie Incluse"**.

*   **RAG Zero-Configurazione:** Punta KektorDB verso una cartella di file PDF/Markdown/DOCX. Gestisce automaticamente il chunking, l'embedding e il **Collegamento delle Entità (Entity Linking)** utilizzando LLM locali.
*   **Recupero Agentico:** Non cerca solo parole chiave. Utilizza **HyDe** (Hypothetical Document Embeddings) e **Riscritura delle Query** per comprendere l'intento dietro domande vaghe e correggere il contesto della memoria della chat.
*   **Grafo di Conoscenza Visivo:** Include una Web UI integrata (`/ui`) per visualizzare come i tuoi documenti sono connessi semanticamente (es. quali documenti parlano della stessa "Entità").
*   **Integrabile (Embeddable):** Stai costruendo un'app Go? Importa `pkg/engine` ed esegui il database all'interno del tuo processo. Nessun overhead di rete.

---

## Casi d'Uso

KektorDB non è progettato per sostituire cluster distribuiti che gestiscono miliardi di vettori. Invece, brilla in scenari specifici e ad alto valore:

### 1. RAG Locale & Knowledge Base
Perfetto per applicazioni desktop o agenti AI locali.
*   **Scenario:** Hai una cartella di documentazione (`.md`, `.pdf`, `.docx`) e vuoi chattare con essa.
*   **Soluzione:** KektorDB sorveglia la cartella, estrae entità (es. collega menzioni di "Progetto X" tra i file) e fornisce un'API di ricerca.
*   **Beneficio:** Trasforma file statici in un Grafo di Conoscenza strutturato in pochi minuti.

### 2. AI Gateway per Open WebUI
*   **Scenario:** Usi una UI come **Open WebUI** + Ollama e vuoi aggiungere memoria a lungo termine.
*   **Soluzione:** Configura la tua UI per usare `http://localhost:9092/v1` (KektorDB Proxy).
*   **Beneficio:** KektorDB intercetta la chat, riscrive le query per risolvere problemi di memoria, inietta il contesto rilevante e inoltra il tutto all'LLM.

### 3. Ricerca Go Integrata
Ideale per sviluppatori Go che costruiscono monoliti o microservizi.
*   **Scenario:** Devi aggiungere ricerca semantica al tuo backend Go (es. "Trova prodotti simili").
*   **Soluzione:** `import "github.com/sanonone/kektordb/pkg/engine"`.
*   **Beneficio:** Zero complessità di deployment. Il DB vive e muore con il processo della tua app.

---

## RAG Zero-Code (Integrazione Open WebUI)

KektorDB può funzionare come **middleware intelligente** tra la tua Chat UI e il tuo LLM. Intercetta le richieste, esegue il recupero e inietta il contesto automaticamente.

**Architettura:**
`Open WebUI` -> `KektorDB Proxy (9092)` -> `Ollama / LocalAI (11434)`

**Come configurarlo:**

1.  **Configura `vectorizers.yaml`** per puntare ai tuoi documenti e abilitare l'Estrazione Entità.
2.  **Configura `proxy.yaml`** per puntare al tuo LLM Locale (Ollama) o OpenAI.
3.  **Esegui KektorDB** con il proxy abilitato:
    ```bash
    ./kektordb -vectorizers-config='vectorizers.yaml' -enable-proxy -proxy-config='proxy.yaml'
    ```
4.  **Configura Open WebUI:**
    *   **Base URL:** `http://localhost:9092/v1`
    *   **API Key:** `kektor` (o qualsiasi stringa).
5.  **Chat:** Fai semplicemente domande sui tuoi documenti. KektorDB gestisce il resto.

👉 **[Leggi la Guida Completa: Costruire un Sistema RAG Veloce con Open WebUI](docs/guides/zero_code_rag.md)**

---

## ✨ Caratteristiche Principali

### 🧠 Pipeline RAG Agentica
*   **Riscritura Query (CQR):** Riscrive automaticamente le domande dell'utente basandosi sulla cronologia della chat (es. "Come installarlo?" -> "Come installare KektorDB?"). Risolve problemi di memoria a breve termine.
*   **Grounded HyDe:** Genera risposte ipotetiche per migliorare il recall su query vaghe, usando frammenti di dati reali per ancorare l'allucinazione.
*   **Safety Net:** Ritorna automaticamente alla ricerca vettoriale standard se la pipeline avanzata non riesce a trovare contesto rilevante.

### 🕸️ Motore a Grafo Semantico
*   **Estrazione Entità Automatizzata:** Usa un LLM locale per identificare concetti (Persone, Progetti, Tecnologie) durante l'ingestione e collega documenti correlati ("Unire i puntini").
*   **Attraversamento del Grafo:** La ricerca attraversa i link `prev` (precedente), `next` (successivo), `parent` (genitore) e `mentions` (menziona) per fornire una finestra di contesto olistica.

<p align="center">
  <img src="docs/images/kektordb-graph-entities.png" alt="Visualizzazione Grafo di Conoscenza" width="700">
  <br>
  <em>Visualizzazione delle connessioni semantiche tra documenti tramite entità estratte.</em>
</p>

### ⚡ Performance & Ingegneria
*   **Motore HNSW:** Implementazione personalizzata ottimizzata per letture ad alta concorrenza.
*   **Ricerca Ibrida:** Combina Similarità Vettoriale + BM25 (Parole chiave) + Filtri Metadati.
*   **Efficienza della Memoria:** Supporta **Quantizzazione Int8** (75% di risparmio RAM) con auto-training zero-shot e **Float16**.
*   **Manutenzione & Ottimizzazione:**
    *   **Vacuum:** Un processo in background che pulisce i nodi eliminati per recuperare memoria e riparare le connessioni del grafo.
    *   **Refine:** Un'ottimizzazione continua che rivaluta le connessioni del grafo per migliorare la qualità della ricerca (recall) nel tempo.
*   **AI Gateway & Middleware:** Agisce come proxy intelligente per client compatibili con OpenAI/Ollama. Dispone di **Caching Semantico** per servire risposte istantanee a query ricorrenti e un **Firewall Semantico** per bloccare prompt dannosi basandosi sulla similarità vettoriale, indipendentemente dalla pipeline RAG.
*   **Persistenza:** Ibrida **AOF + Snapshot** assicura durabilità.
*   **Osservabilità:** Metriche Prometheus (`/metrics`) e logging strutturato.
*   **Doppia Modalità:** Esegui come **Server REST** autonomo o come **Libreria Go**.

### 🖥️ Dashboard Integrata
Disponibile su `http://localhost:9091/ui/`.
*   **Graph Explorer:** Visualizza il tuo grafo di conoscenza con un layout force-directed.
*   **Debugger di Ricerca:** Testa le tue query e vedi esattamente perché un documento è stato recuperato.

---

## Installazione

### Come Server (Docker)
Il modo più semplice per eseguire KektorDB.

```bash
docker run -p 9091:9091 -p 9092:9092 -v $(pwd)/data:/data sanonone/kektordb:latest
```

### Come Server (Binario)
Scarica il binario pre-compilato dalla [Pagina delle Release](https://github.com/sanonone/kektordb/releases).

```bash
# Linux/macOS
./kektordb
```

> **Nota sulla Compatibilità:** Tutto lo sviluppo e i test sono stati eseguiti su **Linux (x86_64)**. Le build pure Go dovrebbero funzionare su Windows/Mac/ARM.

---

## Uso come Libreria Go Integrata

KektorDB può essere importato direttamente nella tua applicazione Go, rimuovendo la necessità di servizi esterni o container.

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
	// 1. Inizializza il Motore (gestisce la persistenza automaticamente)
	opts := engine.DefaultOptions("./kektor_data")
	db, err := engine.Open(opts)
	if err != nil { panic(err) }
	defer db.Close()

	// 2. Crea Indice
	db.VCreate("products", distance.Cosine, 16, 200, distance.Float32, "english", nil)

	// 3. Aggiungi Dati
	db.VAdd("products", "p1", []float32{0.1, 0.2}, map[string]any{"category": "electronics"})

	// 4. Cerca
	results, _ := db.VSearch("products", []float32{0.1, 0.2}, 10, "category=electronics", 100, 0.5)
	fmt.Println("Trovati ID:", results)
}
```

---

### 🚀 Avvio Rapido (Python)

Questo esempio dimostra un flusso di lavoro completo: creazione di un indice, inserimento batch di dati con metadati ed esecuzione di una Ricerca Ibrida (Vettoriale + Parole Chiave).

1.  **Installa Client & Utility:**
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

    # 2. Crea Indice (Ibrido abilitato)
    try: client.delete_index(index)
    except: pass
    client.vcreate(index, metric="cosine", text_language="english")

    # 3. Aggiungi Dati (Batch)
    docs = [
        {"text": "Go is efficient for backend systems.", "type": "code"},
        {"text": "Rust guarantees memory safety.", "type": "code"},
        {"text": "Pizza margherita is classic Italian food.", "type": "food"},
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

    # 4. Cerca (Ibrido: Vettore + Filtro Metadati)
    # Trovare "fast programming languages" MA solo nella categoria 'code'
    query_vec = model.encode("fast programming languages").tolist()
    
    results = client.vsearch(
        index,
        query_vector=query_vec,
        k=2,
        filter_str="category='code'", # Filtro Metadati
        alpha=0.7 # 70% Sim. Vettoriale, 30% Rank Parole Chiave
    )

    print(f"ID Risultato Top: {results[0]}")
    ```

---

### 🦜 Integrazione con LangChain

KektorDB include un wrapper integrato per **LangChain Python**, permettendoti di collegarlo direttamente alle tue pipeline AI esistenti.

```python
from kektordb_client.langchain import KektorVectorStore
```

---

## Benchmark Preliminari

I benchmark sono stati eseguiti su una macchina Linux locale (Hardware Consumer, Intel i5-12500). Il confronto avviene contro **Qdrant** e **ChromaDB** (via Docker con host networking) per garantire una base di riferimento equa.

> **Avvertenza:** Il benchmarking dei database è complesso. Questi risultati riflettono uno scenario specifico (**nodo singolo, heavy-read, client Python**) sulla mia macchina di sviluppo. Sono intesi per dimostrare le capacità di KektorDB come motore integrato ad alte prestazioni, non per rivendicare superiorità assoluta in scenari di produzione distribuiti.

#### 1. Carico di Lavoro NLP (GloVe-100d, Cosine)
*400k vettori, precisione float32.*
KektorDB sfrutta Go Assembly (Gonum) ottimizzato per la similarità del Coseno. In questo setup specifico, mostra un throughput molto elevato.

| Database | Recall@10 | **QPS (Query/sec)** | Tempo Indicizzazione (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9664 | **1073** | 102.9s |
| Qdrant | 0.9695 | 848 | **32.3s** |
| ChromaDB | 0.9519 | 802 | 51.5s |

#### 2. Carico di Lavoro Computer Vision (SIFT-1M, Euclidean)
*1 Milione di vettori, precisione float32.*
KektorDB usa un motore ibrido Go/Rust (`-tags rust`) per questo test. Nonostante l'overhead di CGO per vettori da 128d, le prestazioni sono competitive con i motori nativi C++/Rust.

| Database | Recall@10 | **QPS (Query/sec)** | Tempo Indicizzazione (s) |
| :--- | :--- | :--- | :--- |
| **KektorDB** | 0.9906 | **881** | 481.4s |
| Qdrant | 0.998 | 845 | **88.5s** |
| ChromaDB | 0.9956 | 735 | 211.2s |

> *Nota sulla Velocità di Indicizzazione:* KektorDB è attualmente più lento nell'ingestione rispetto a motori maturi. Questo è in parte dovuto al fatto che costruisce il grafo completamente interrogabile immediatamente all'inserimento, ma soprattutto a causa dell'attuale architettura a grafo singolo. **Ottimizzare la velocità di ingestione massiva (bulk) è la priorità assoluta per la prossima major release.**

> [!TIP]
> **Ottimizzazione Prestazioni: "Ingest Fast, Refine Later"**
>
> Se devi indicizzare grandi dataset velocemente, crea l'indice con un `ef_construction` più basso (es. 40). Questo riduce significativamente il tempo di indicizzazione.
> Puoi poi abilitare il processo di **Refine** in background con una qualità target più alta (es. 200). KektorDB ottimizzerà progressivamente le connessioni del grafo in background rimanendo disponibile per le query.

#### Efficienza della Memoria (Compressione & Quantizzazione)
KektorDB offre risparmi di memoria significativi attraverso quantizzazione e compressione, permettendoti di far stare dataset più grandi in RAM con un impatto minimo su prestazioni o recall.

| Scenario | Config | Impatto Memoria | QPS | Recall |
| :--- | :--- | :--- | :--- | :--- |
| **NLP (GloVe-100d)** | Float32 | 100% (Base) | ~1073 | 0.9664 |
| | **Int8** | **~25%** | ~858 | 0.905 |
| **Vision (SIFT-1M)** | Float32 | 100% (Base) | ~881 | 0.9906 |
| | **Float16** | **~50%** | ~834 | 0.9770 |

*(La logica "Smart Dispatch" nella build accelerata con Rust seleziona automaticamente la migliore implementazione—Go, Gonum, o Rust—per ogni operazione basandosi sulle dimensioni del vettore. Le versioni in puro Go `float16` e `int8` servono come fallback portatili.)*

[Report Completo dei Benchmark](BENCHMARKS.md)

---

## Riferimento API (Sommario)

Per una guida completa a tutte le funzionalità e agli endpoint API, consulta la **[Documentazione Completa](DOCUMENTATION.md)**.

*   `POST /vector/actions/search`: Ricerca vettoriale ibrida.
*   `POST /vector/actions/import`: Caricamento massivo ad alta velocità.
*   `POST /vector/indexes`: Crea e gestisci indici.
*   `POST /graph/actions/link`: Crea relazioni semantiche.
*   `POST /graph/actions/traverse`: Attraversamento profondo del grafo (N-Hop) partendo da un ID nodo specifico.
*   `POST /rag/retrieve`: Ottieni chunk di testo per RAG.
*   `GET /system/tasks/{id}`: Monitora task a lunga esecuzione.
*   `POST /system/save`: Snapshot manuale.

---

## 🛣️ Roadmap

KektorDB è un progetto giovane in sviluppo attivo.

*   **v0.4.0 (Corrente):** RAG Agentico, HyDe, Estrazione Entità, Web UI, Osservabilità.
*   **A breve termine:** Roaring Bitmaps per filtri, ottimizzazioni simd avo.
*   **v0.5.0 (Prossimo):** Archiviazione Ibrida su Disco (Vettori su disco) per superare il limite della RAM.
*   **A lungo termine:** **forse** Replicazione e Consenso Distribuito.

---

## 🤝 Contribuire

**KektorDB è un progetto personale nato dal desiderio di imparare.**

Come unico manutentore, ho costruito questo motore per esplorare CGO, SIMD e ottimizzazioni Go a basso livello. Sono orgoglioso delle prestazioni raggiunte finora, ma so che c'è sempre un modo migliore per scrivere codice.

Se noti race conditions, ottimizzazioni mancate o pattern Go non idiomatici, **per favore apri una Issue o una PR**.

👉 **[Per saperne di più](CONTRIBUTING.md)**

---

### Licenza

Distribuito sotto Licenza Apache 2.0. Vedi il file `LICENSE` per i dettagli.

---

## ☕ Supporta il Progetto

KektorDB è un progetto open-source sviluppato da un singolo manutentore.
Se trovi questo strumento utile per il tuo setup RAG locale o le tue applicazioni Go, per favore considera di supportare lo sviluppo.

Il tuo supporto mi aiuta a dedicare più tempo alla manutenzione, a nuove funzionalità e alla documentazione.

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>
