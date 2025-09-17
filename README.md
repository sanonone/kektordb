# KektorDB 

**KektorDB è un database in-memory ad alte prestazioni, scritto da zero in Go, che combina uno store Key-Value con un motore di ricerca vettoriale all'avanguardia.**

È progettato per essere **estremamente veloce, facile da usare e embeddabile**, puntando a diventare "l'SQLite dei Vector DB" per gli sviluppatori Go e per l'ecosistema AI/ML.

---

### Caratteristiche Principali (Stato Attuale)

*   **Motore di Ricerca Vettoriale Custom:**
    *   Implementazione dell'algoritmo **HNSW** scritta da zero per il controllo totale sulle performance.
    *   Funzioni di distanza accelerate via **Assembly (AVX2/FMA)**, con speedup fino a **5.5x** rispetto a implementazioni Go standard.
    *   Supporto per metriche di distanza multiple (**Euclidea** e **Similarità del Coseno**), configurabili per ogni indice.
*   **Filtraggio Avanzato sui Metadati:**
    *   **Pre-filtering ad alte prestazioni:** I filtri vengono applicati prima della ricerca HNSW per ridurre drasticamente lo spazio di ricerca.
    *   Supporto per filtri di uguaglianza (`tag=gatto`), di range (`prezzo<100`) e composti (`AND`/`OR`).
    *   Utilizza indici secondari ottimizzati (Indice Invertito e B-Tree).
*   **Affidabilità e Accessibilità:**
    *   **Persistenza AOF:** Ogni operazione di scrittura viene salvata su disco. Il database ripristina il suo stato al riavvio.
    *   **API REST Universale:** Un'interfaccia HTTP/JSON moderna per interagire con il database da qualsiasi linguaggio.
    *   **Client Python:** Un SDK Python (`kektordb-client`) per un'integrazione semplice e pulita con applicazioni AI/ML.
*   **Configurabilità:**
    *   Porte di rete e percorso del file di persistenza configurabili tramite flag da riga di comando.
    *   Parametri HNSW (`M`, `efConstruction`) e metrica di distanza configurabili per ogni indice.

---

### Quick Start (Esempio con `curl`)

1.  **Avvia il server KektorDB:**
    ```bash
    # Assumendo di avere Go installato
    go run ./cmd/kektordb
    ```

2.  **Crea un indice vettoriale (via API REST):**
    ```bash
    curl -X POST -d '{"index_name": "articoli", "metric": "cosine"}' http://localhost:9091/vector/create
    ```

3.  **Aggiungi un vettore con metadati:**
    ```bash
    curl -X POST -d '{"index_name": "articoli", "id": "doc1", "vector": [0.1, 0.8, 0.3], "metadata": {"autore": "gemma", "anno": 2024}}' http://localhost:9091/vector/add
    ```

4.  **Esegui una ricerca filtrata:**
    ```bash
    # Cerca documenti simili, ma solo quelli dell'autore "gemma"
    curl -X POST -d '{"index_name": "articoli", "k": 1, "query_vector": [0.15, 0.75, 0.35], "filter": "autore=gemma"}' http://localhost:9091/vector/search
    ```

---

### Roadmap Futura

Questo progetto è in sviluppo attivo. Le prossime grandi feature includono:

*   [ ] **Compattazione AOF:** Per ottimizzare lo spazio su disco e i tempi di riavvio.
*   [ ] **Quantizzazione Vettoriale:** Per ridurre drasticamente l'uso della RAM.
*   [ ] **Snapshotting (Persistenza RDB):** Per riavvii istantanei su dataset di grandi dimensioni.
*   [ ] **Documentazione Completa e Benchmark Ufficiali.**

---

*(Questo è un progetto personale per l'apprendimento e lo sviluppo di software ad alte prestazioni. Feedback e contributi sono i benvenuti!)*
