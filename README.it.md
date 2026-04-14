# KektorDB

*Il layer di memoria cognitiva per agenti AI.*

<p align="center">
  <img src="docs/images/logo.png" alt="KektorDB Logo" width="500">
</p>

[![GitHub Sponsors](https://img.shields.io/badge/Sponsor-sanonone-pink?logo=github)](https://github.com/sponsors/sanonone)
[![Ko-fi](https://img.shields.io/badge/Support%20me-Ko--fi-orange?logo=ko-fi)](https://ko-fi.com/sanon)
[![Go Reference](https://pkg.go.dev/badge/github.com/sanonone/kektordb.svg)](https://pkg.go.dev/github.com/sanonone/kektordb)
[![PyPI version](https://badge.fury.io/py/kektordb-client.svg)](https://badge.fury.io/py/kektordb-client)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

<p align="center">
  <a href="DOCUMENTATION.md">📚 Documentazione</a> •
  <a href="CONTRIBUTING.md">🤝 Contributi</a> •
  <a href="docs/guides/zero_code_rag.md">🤖 Guida RAG Open WebUI</a>
</p>

[English](README.md) | [Italiano](README.it.md)

> [!TIP]
> **Supporto Docker:** Preferisci i container? Un `Dockerfile` è incluso nella root per creare le tue immagini.

KektorDB è un **sistema di memoria in-memory per applicazioni AI** che combina ricerca vettoriale ad alte prestazioni con un knowledge graph temporale. Memorizza informazioni comprendendole—tracciando importanza, relazioni ed evoluzione nel tempo. Un motore cognitivo integrato (Gardener) consolida automaticamente le memorie, rileva contraddizioni e lascia svanire le informazioni irrilevanti attraverso il decadimento temporale.

> *Costruito per sviluppatori che costruiscono agenti AI, sistemi RAG e applicazioni ad alta intensità di conoscenza.*

<p align="center">
  <img src="docs/images/kektordb-demo.gif" alt="Demo Grafo KektorDB" width="800">
</p>

---

## Cos'è KektorDB?

KektorDB è un **sistema di memoria AI** che combina due motori complementari:

1. **Ricerca vettoriale ad alte prestazioni (HNSW)** per similarità semantica—trova cose che *significano* lo stesso, non solo corrispondenze esatte.
2. **Knowledge graph temporale** per relazioni strutturate—comprende *come le cose sono connesse* e *come la conoscenza evolve*.

**I database tradizionali memorizzano i dati.** Conservano fatti senza comprendere il loro contesto, importanza o relazioni. Li interroghi, e ti restituiscono ciò che è memorizzato. Semplice, ma limitato.

**KektorDB pensa mentre memorizza.** Traccia cosa è importante versus dimenticato, rileva contraddizioni, e aiuta la tua AI a recuperare la *giusta* informazione al *momento* giusto.

### Costruito per Ingegneri AI

Se stai costruendo:
- **Agenti AI** che necessitano di memoria persistente e auto-organizzante
- **Sistemi RAG** che dovrebbero comprendere contesto e importanza
- **Sistemi Multi-Agente** con conoscenza condivisa
- **Assistenti AI Personali** che imparano nel tempo

...allora KektorDB è progettato per te.

### Ma Resta un Motore Potente

Sotto il cofano, ottieni primitive di database collaudate:
- **HNSW** per ricerca di similarità vettoriale ad alte prestazioni
- **Ricerca Ibrida** che combina vettori + corrispondenza keyword BM25
- **Quantizzazione efficiente in memoria** (Int8, Float16) per memorizzare più dati in RAM
- **Attraversamento del grafo** per recupero contesto N-hop

Questi sono strumenti. Il prodotto è un'AI che *comprende* i tuoi dati.

---

## Casi d'Uso

### 1. Memoria per Agenti AI (Primario)

Dai al tuo agente AI una memoria persistente e auto-organizzante che comprende la rilevanza.

*Scenario:* Costruire un assistente AI che dovrebbe ricordare preferenze utente, conversazioni passate e costruire conoscenza nel tempo.

*Soluzione:* Usa le sessioni per tracciare le conversazioni. Lascia che il Gardener estragga fatti e rilevi contraddizioni. Abilita il decadimento della memoria così che le informazioni obsolete sbiadiscano naturalmente.

*Vantaggio:* Gli utenti hanno l'impressione di parlare con qualcuno che si ricorda davvero di loro.

### 2. Memoria Condivisa Multi-Agente

Abilita più agenti AI a condividere conoscenza e imparare l'uno dall'altro.

*Scenario:* Molteplici agenti specializzati (ricerca, coding, scrittura) che necessitano di condividere contesto e trasferire informazioni apprese.

*Soluzione:* Usa MCP o API diretta per condividere memorie tra indici degli agenti. KektorDB traccia la provenienza della conoscenza e aiuta a risolvere i conflitti.

*Vantaggio:* Gli agenti smettono di ripetere gli errori degli altri.

### 3. RAG con Memoria

Vai oltre "recupera e inietta"—costruisci RAG che comprende davvero la rilevanza.

*Scenario:* Il tuo sistema RAG continua a recuperare documenti tecnicamente simili ma contestualmente errati.

*Soluzione:* Abilita il recupero basato su grafo per seguire relazioni semantiche. Usa l'espansione contestuale adattiva. Lascia che il Gardener evidenzi le lacune nella conoscenza.

*Vantaggio:* Maggiore recall, minor rischio di allucinazioni.

### 4. Ricerca Vettoriale Embedded (Go)

Ricerca vettoriale ad alte prestazioni integrata nella tua applicazione Go senza overhead operativo.

*Scenario:* Implementare "Prodotti Correlati" o "Ricerca Semantica" in un backend Go.

*Soluzione:* `import "github.com/sanonone/kektordb/pkg/engine"` per eseguire il DB nel processo.

*Vantaggio:* Zero complessità di deployment. Il DB scala con la tua app.

---

## Differenziatori Chiave

### Memoria che Si Gestisce Da Sola

Il **Gardener** è un processo in background che analizza continuamente i tuoi dati:

- **Consolidamento**: Unisce informazioni duplicate, rafforza le memorie frequentemente usate
- **Rilevamento Contraddizioni**: Segnala quando nuove informazioni confliggono con fatti stabiliti
- **Analisi Lacune di Conoscenza**: Identifica cosa manca per una comprensione completa
- **Dimenticanza**: Depriorizza naturalmente le informazioni non utilizzate

### Memoria Consapevole del Tempo

A differenza dei database statici, KektorDB comprende *quando* le informazioni contano:

- **Decadimento**: Le memorie sbiadiscono naturalmente se non rafforzate
- **Rafforzamento**: Recuperare una memoria la rende più prominente
- **Grafo Temporale**: Interroga lo stato della conoscenza in qualsiasi momento nel passato
- **Fatti Core**: Fissa i fatti essenziali per evitare che sbiadiscano mai

### Risposte Consapevoli dell'Utente

KektorDB costruisce e mantiene **profili utente**:

- Stile di comunicazione e preferenze linguistiche
- Aree di competenza e conoscenza nota
- Preferenze dichiarate versus comportamento osservato
- Risoluzione di informazioni conflittuali auto-dichiarate

### Relazioni come Cittadini di Prima Classe

Il knowledge graph non è un'aggiunta—è centrale:

- Estrazione automatica di entità dai documenti
- Collegamento semantico di concetti correlati
- Attraversamento N-hop per scoperta contestuale
- Grafo pesato con proprietà e timestamp

### Supporto MCP Nativo

KektorDB parla nativamente il **Model Context Protocol**. Connetti Claude Desktop o qualsiasi client MCP direttamente.

**Strumenti Memoria:** `save_memory`, `recall_memory`, `scoped_recall`, `adaptive_retrieve`

**Strumenti Grafo:** `create_entity`, `connect_entities`, `explore_connections`, `find_connection`

**Strumenti Cognitivi:** `start_session`, `end_session`, `get_user_profile`, `check_subconscious`, `resolve_conflict`, `ask_meta_question`

**Utilità:** `transfer_memory`, `unpin_memory`, `filter_vectors`

---

## RAG Zero-Code (Integrazione Open WebUI)

<p align="center">
  <img src="docs/images/kektordb-rag-demo.gif" alt="Demo RAG KektorDB" width="800">
</p>

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

### Funzionalità Cognitive & Agentiche
*   **Motore Cognitivo (Gardener):** Un demone in background che esegue convalide incrociate di confidenza. Analizza il grafo per contraddizioni, traccia profili utente, modella l'evoluzione della conoscenza e risolve i conflitti usando LLM.
*   **Estrazione Fatti Core:** Il Gardener estrae automaticamente fatti immutabili dalle interazioni utente (nome, professione, preferenze) e crea nodi di memoria pinned con `type="core_fact"` per recupero persistente e veloce senza decadimento temporale.
*   **Recupero Adattivo:** Una sofisticata pipeline RAG che usa l'espansione del contesto graph-aware, recuperando chunk iniziali e seguendo automaticamente i vicini semantici fino a un limite di token definito dinamicamente.
*   **Riscrittura Query (CQR):** Riscrive automaticamente le domande dell'utente per risolvere il problema della memoria a breve termine.
*   **Grounded HyDe:** Genera risposte ipotetiche radicate su frammenti reali di dati, per migliorare drasticamente il recall per le query vaghe.
*   **Compressione Contesto ("Caveman Mode"):** Compressione lessicale sicura che riduce il conteggio token del 20-35% per il contesto LLM preservando il significato semantico. Rimuove stopwords sicure (articoli, preposizioni) ma preserva rigorosamente le negazioni e gli operatori logici (non, e, o, ma, se).
*   **EventBus:** Un sistema pub/sub integrato per reagire in tempo reale alle operazioni su vettori e grafo, con supporto per i Server-Sent Events (SSE).

### Performance & Ingegneria
*   **Motore HNSW:** Implementazione personalizzata ottimizzata per letture ad alta concorrenza.
*   **Ricerca Ibrida:** Combina Similarità Vettoriale + BM25 (Parole chiave) + Filtri Metadati.
*   **Efficienza della Memoria:** Supporta **Quantizzazione Int8** (75% di risparmio RAM) con auto-training zero-shot e **Float16**.
*   **Manutenzione & Ottimizzazione:**
    *   **Vacuum:** Un processo in background che pulisce i nodi eliminati per recuperare memoria e riparare le connessioni del grafo.
    *   **Refine:** Un'ottimizzazione continua che rivaluta le connessioni del grafo per migliorare la qualità della ricerca (recall) nel tempo.
*   **AI Gateway & Middleware:** Agisce come proxy intelligente per client compatibili con OpenAI/Ollama. Dispone di **Caching Semantico** per servire risposte istantanee a query ricorrenti e un **Firewall Semantico** per bloccare prompt dannosi basandosi su similarità vettoriale o liste di negazione esplicite.
*   **Lazy AOF Writer:** Ottimizzazione delle performance di scrittura con batch flushing (miglioramento throughput 10-100x) mantenendo la durabilità.
*   **Supporto Visione:** Processa immagini e PDF con capacità OCR tramite integrazione Vision LLM.
*   **Persistenza:** Ibrida **AOF + Snapshot** assicura durabilità.
*   **Osservabilità:** Metriche Prometheus (`/metrics`), logging strutturato e endpoint di profiling Go pprof.
*   **Doppia Modalità:** Esegui come **Server REST** autonomo o come **Libreria Go**.

### Motore a Grafo Semantico
*   **Estrazione Entità Automatizzata:** Usa un LLM locale per identificare concetti (Persone, Progetti, Tecnologie) durante l'ingestione e collega documenti correlati.
*   **Grafo Pesato e con Proprietà:** Supporta "Archi Ricchi" con attributi (pesi, proprietà arbitrarie) per abilitare algoritmi complessi di raccomandazione e ranking.
*   **Grafo Temporale (Time Travel):** Ogni relazione è versionata. Il supporto al soft delete permette di interrogare lo stato del grafo in qualsiasi momento nel passato.
*   **Memory Decay & Reinforcement:** Unifica la memoria a breve e lungo termine. I nodi non interrogati decadono naturalmente, e vengono rinforzati quando si recupera il contesto. Supporta i nodi "pinned" per bypassare il decay.
*   **Navigazione Bidirezionale:** Gestione automatica degli archi in entrata per consentire il recupero O(1) di "chi punta al nodo X", potenziando l'attraversamento efficiente del grafo.
*   **Entità di Grafo:** Supporto per nodi senza vettore per rappresentare entità astratte come "Utenti" o "Sessioni" nella stessa struttura a grafo.
*   **Attraversamento del Grafo:** La ricerca attraversa qualsiasi tipo di relazione (come `prev`, `next`, `parent`, `mentions`) per fornire una finestra di contesto olistica.
*   **Filtri sul Grafo:** Combina la ricerca vettoriale con filtri sulla topologia del grafo (es. "cerca solo nei figli del Documento X"), potenziato dai Roaring Bitmaps.
*   **Path Finding (FindPath):** Scopri il percorso più breve tra due nodi usando BFS bidirezionale. Supporta query time-travel.
*   **Node Search:** Esegue filtri solo su metadati senza similarità vettoriale.

<p align="center">
  <img src="docs/images/kektordb-graph-entities.png" alt="Visualizzazione Grafo di Conoscenza" width="700">
  <br>
  <em>Visualizzazione delle connessioni semantiche tra documenti tramite entità estratte.</em>
</p>

### Supporto Model Context Protocol (MCP)
KektorDB funziona come un **Cognitive Memory Server** completo basato sul protocollo MCP [Model Context Protocol](https://modelcontextprotocol.io/). Questo permette agli agenti LLM (come Claude via Claude Desktop) di utilizzare direttamente KektorDB come memoria a lungo termine auto-organizzativa.

*   **Come avviarlo:**
    ```bash
    ./kektordb --mcp
    ```
*   **Integrazione:** Aggiungi KektorDB alla configurazione del tuo client MCP usando il flag `--mcp`.

---

### Dashboard Integrata
Disponibile su `http://localhost:9091/ui/`.
*   **Graph Explorer:** Visualizza il tuo grafo di conoscenza con un layout force-directed.
*   **Debugger di Ricerca:** Testa le tue query e vedi esattamente perché un documento è stato recuperato.

---

## Installazione

### Come Server (Docker)

```bash
docker run -p 9091:9091 -p 9092:9092 -v $(pwd)/data:/data sanonone/kektordb:latest
```

### Come Server (Binario)
Scarica il binario pre-compilato dalla [Pagina delle Release](https://github.com/sanonone/kektordb/releases).

```bash
# Linux/macOS
./kektordb

# Con opzioni personalizzate
./kektordb -http-addr :9091 -save "30 500" -log-level debug
```

**Flag della linea di comando:**
*   `-http-addr`: Indirizzo del server HTTP (default: `:9091`)
*   `-aof-path`: Percorso del file di persistenza (default: `kektordb.aof`)
*   `-save`: Policy di auto-snapshot `"secondi cambiamenti"` (default: `"60 1000"`, vuoto per disabilitare)
*   `-auth-token`: Token di autenticazione API
*   `-log-level`: Livello di logging (`debug`, `info`, `warn`, `error`)
*   `-enable-proxy`: Abilita AI Gateway/Proxy
*   `-proxy-config`: Percorso del file di configurazione del proxy
*   `-vectorizers-config`: Percorso del file di configurazione dei vectorizers
*   `-mcp`: Esegui come Server MCP (Stdio)

> **Nota sulla Compatibilità:** Tutto lo sviluppo e i test sono stati eseguiti su **Linux (x86_64)**. Le build pure Go dovrebbero funzionare su Windows/Mac/ARM.

---

## 🚀 Avvio Rapido (Python)

Questo esempio dimostra come costruire un **Agente AI con Memoria** usando sessioni e funzionalità cognitive.

1.  **Installa il Client:**
    ```bash
    pip install kektordb-client sentence-transformers
    ```

2.  **Esegui lo script:**

    ```python
    from kektordb_client import KektorDBClient
    from kektordb_client.cognitive import CognitiveSession

    # 1. Inizializzazione
    client = KektorDBClient(port=9091)
    index = "agent_memory"

    # 2. Crea indice con memoria abilitata (half-life 30 giorni)
    try:
        client.delete_index(index)
    except:
        pass
    client.vcreate(index, metric="cosine", text_language="english")

    # 3. Avvia una sessione (es. conversazione utente)
    with CognitiveSession(client, index, user_id="user_123") as session:
        # Salva memorie collegate a questa conversazione
        session.save_memory(
            "L'utente si chiama Marco e preferisce risposte concise",
            layer="episodic",
            tags=["user_preference"]
        )
        session.save_memory(
            "Marco sta lavorando a un progetto Go chiamato KektorDB",
            layer="semantic",
            tags=["project", "go"]
        )
        session.save_memory(
            "Marco ha chiesto come implementare RAG. Ho spiegato ricerca vettoriale + attraversamento grafo.",
            layer="episodic"
        )

    # 4. Successivamente: Recupera profilo utente
    profile = client.get_user_profile("user_123", index)
    print(f"Stile di comunicazione: {profile.get('communication_style', 'N/A')}")
    print(f"Aree di competenza: {profile.get('expertise_areas', [])}")

    # 5. Cerca memorie
    results = client.vsearch(
        index,
        query_vector=[0.1, 0.2, 0.3, 0.4],  # Embed della tua query
        k=3,
        filter_str="tags ? 'project'"
    )
    print(f"Trovate {len(results)} memorie rilevanti")
    ```

👉 **[Leggi la Documentazione Completa](DOCUMENTATION.md)** per tutti gli endpoint e le funzionalità disponibili.

---

### 🦜 Integrazione con LangChain

KektorDB include un wrapper integrato per **LangChain Python**, permettendoti di collegarlo direttamente alle tue pipeline AI esistenti.

```python
from kektordb_client.langchain import KektorVectorStore
```

---

## Benchmark

I benchmark sono stati eseguiti su una macchina Linux locale (Hardware Consumer, Intel i5-12500). Il confronto avviene contro **Qdrant** e **ChromaDB** (via Docker con host networking) per garantire una base di riferimento equa.

| Database | QPS NLP | QPS Vision | Recall@10 |
| :--- | :--- | :--- | :--- |
| **KektorDB** | **1073** | **881** | 0.97 |
| Qdrant | 848 | 845 | 0.97 |
| ChromaDB | 802 | 735 | 0.96 |

> *Nota: KektorDB è ottimizzato per scenari embedded a nodo singolo. Per deployment distribuiti su scala miliardaria, considera soluzioni specializzate.*

[Report Completo dei Benchmark](BENCHMARKS.md)

---

## 🛣️ Roadmap

### Rilasciato in v0.5.0 ✅
*   [x] **mmap Arena Zero-Copy:** Dati vettoriali memorizzati in file memory-mapped, rompendo il limite RAM.
*   [x] **Motore Cognitivo (Gardener):** Demone in background per risoluzione conflitti del grafo e profilazione utente.
*   [x] **Estrazione Fatti Core:** Estrazione automatica di fatti utente immutabili con nodi pinned.
*   [x] **Compressione Contesto:** Compressione lessicale sicura che riduce i token LLM del 20-35%.
*   [x] **Client TypeScript:** SDK Node.js/TypeScript ufficiale.

### Pianificati (Breve Termine)
*   [x] **Filtri sul Grafo:** Combina ricerca vettoriale con filtri sulla topologia del grafo (Roaring Bitmaps).
*   [x] **Property Graphs:** Supporto per "Rich Edges" con attributi e timestamp.
*   [ ] **Backup/Restore Nativo:** API semplice per salvare snapshot su S3/MinIO/Locale.
*   [ ] **Ottimizzazioni SIMD/AVX:** Estensione Assembly Go puro a più metriche di distanza.
*   [x] **RBAC & Sicurezza:** Controllo Accessi Basato sui Ruoli per applicazioni multi-tenant.

> **Vuoi influenzare la roadmap?** [Apri una Issue](https://github.com/sanonone/kektordb/issues) o vota quelle esistenti!

---

## 🛑 Limitazioni Attuali (v0.5.1)
*   **Singolo nodo:** KektorDB attualmente non supporta il clustering. Scala verticalmente entro i limiti delle risorse della macchina.

---

### ⚠️ Stato del Progetto e Filosofia

Sebbene l'ambizione sia alta, la velocità di sviluppo dipende dal tempo libero disponibile e dai contributi della community. La roadmap rappresenta la **visione** per il progetto, ma le priorità potrebbero cambiare in base al feedback degli utenti e alle necessità di stabilità.

Se ti piace la visione e vuoi accelerare il processo, **le Pull Request sono le benvenute!**

---

## 🤝 Contribuire

Se noti race conditions, ottimizzazioni mancate o pattern Go non idiomatici, **per favore apri una Issue o una PR**.

👉 **[Per saperne di più](CONTRIBUTING.md)**

---

### Licenza

Distribuito sotto Licenza Apache 2.0. Vedi il file `LICENSE` per i dettagli.

---

## ☕ Supporta il Progetto

Se trovi questo strumento utile per il tuo setup RAG locale o le tue applicazioni Go, per favore considera di supportare lo sviluppo.

Il tuo supporto mi aiuta a dedicare più tempo alla manutenzione, a nuove funzionalità e alla documentazione.

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>
