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
  <a href="DOCUMENTATION.md">Documentazione</a> -
  <a href="CONTRIBUTING.md">Contributi</a> -
  <a href="docs/guides/zero_code_rag.md">Guida GraphRAG</a>
</p>

[English](README.md) | [Italiano](README.it.md)

KektorDB e' un **sistema di memoria AI** - non un database che archivia dati, ma un motore che **capisce** cio' che memorizza. Combina ricerca vettoriale ad alte prestazioni (HNSW) con un knowledge graph temporale e un motore cognitivo che analizza continuamente i tuoi dati, rileva contraddizioni, traccia l'importanza e lascia svanire naturalmente le informazioni irrilevanti.

<p align="center">
  <img src="docs/images/kektordb-demo.gif" alt="KektorDB Demo" width="800">
</p>

**In 30 secondi - dai memoria persistente al tuo agente AI:**

```bash
# 1. Installa e configura (una volta sola)
kektordb setup opencode

# 2. Avvia il server di memoria
kektordb --mcp --tools=agent
```

Il tuo agente ora puo' chiamare `save_memory`, `recall_memory`, `start_session` e altri 46 strumenti. Le memorie persistono tra le sessioni, decadono naturalmente quando non usate, e il motore cognitivo rileva contraddizioni e costruisce profili utente in autonomia.

I 49 strumenti agent coprono: **CRUD memoria** (`save_memory`, `recall_memory`, `adaptive_retrieve`), **operazioni grafo** (`connect_entities`, `find_path`, `explore_connections`), **gestione sessioni** (`start_session`, `end_session`), **funzioni cognitive** (`check_subconscious`, `get_user_profile`, `ask_meta_question`), **compilazione conoscenza** (`request_knowledge` con cache artifact), piu' strumenti di indicizzazione, configurazione e KV store. Usa `--tools=all` per tutti i 57.

---

## Cosa Fa KektorDB

### Motore Cognitivo e di Memoria

**Gardener - Memoria che si Auto-Gestisce.** Un processo background (3 modalita': basic, advanced, meta) che analizza continuamente il knowledge graph. Consolida memorie duplicate, rileva quando nuovi fatti contraddicono quelli stabiliti, identifica lacune di conoscenza e porta alla luce insight tramite 11 detector specializzati. Si attiva con `--cognitive-config cognitive.yaml`.

**Motore Epistemico - Sappi Cio' che Sai.** Un framework matematico a tre pilastri (Consenso 40%, Stabilita' 30%, Attrito 30%) assegna punteggi di confidenza a ogni memoria. Identifica se un fatto e' *cristallizzato*, *stabile*, *volatile* o *contestato*. Interroga via `POST /vector/actions/belief-assessment`.

**Rilevamento Contraddizioni - Cattura i Conflitti Subito.** Il Gardener usa analisi basata su LLM per rilevare quando nuove informazioni confliggono con fatti stabiliti. In modalita' advanced, propone risoluzioni e puo' auto-risolvere contraddizioni minori. Gli agenti controllano le contraddizioni in sospeso via `check_subconscious`.

**Semantic Git - Evolvi, Non Sovrascrivere.** Controllo versione per le memorie. Quando un'informazione cambia, `VEvolve` crea una nuova versione collegata alla precedente tramite archi `superseded_by`/`evolves_from`. Storia completa preservata con marcatori `_is_historical`. Interroga lo stato della conoscenza in qualsiasi momento nel tempo.

**Profilazione Utente - Conosci i Tuoi Utenti.** KektorDB costruisce e mantiene autonomamente profili utente: stile di comunicazione, preferenze linguistiche, aree di competenza, dislikes, preferenze dichiarate vs comportamento osservato. I profili si aggiornano dopo una soglia configurabile di interazioni. Interroga via `get_user_profile`.

**Knowledge Engine - Artefatti Pre-Compilati.** Compila artefatti di conoscenza strutturati da query sul grafo usando template predefiniti (`entity_card`, `topic_overview`, `user_profile`, `timeline`, `session_summary`). I campi sono calcolati in modo deterministico o via LLM. Gli artefatti sono cachati (<50ms hit, zero consumo token) e automaticamente ricompilati quando i dati sorgente cambiano tramite l'Artifact Watcher integrato. Attiva la compilazione via `request_knowledge` in MCP o `POST /compile` in REST.

**Memoria Temporale - Decadimento e Rinforzo.** Unifica memoria a breve e lungo termine. I nodi perdono rilevanza nel tempo se non acceduti, e vengono rinforzati al recupero. Fissa fatti fondamentali per evitare che svaniscano. Configura il decadimento per layer di memoria (episodico, semantico, procedurale) con modelli esponenziale, lineare, a gradini o Ebbinghaus.

### Grafo e Ricerca

**Knowledge Graph - Relazioni come Cittadini di Prima Classe.** Grafi pesati con proprieta', navigazione bidirezionale, time travel (interroga lo stato a qualsiasi timestamp), attraversamento N-hop e path finding BFS bidirezionale. Regole di auto-collegamento creano connessioni da campi metadati (es. `parent_id` -> `child_of`). Entita' del grafo possono esistere senza vettori - rappresenta utenti, sessioni o concetti astratti.

**Ricerca Ibrida - Vettoriale + Keyword + Grafo.** Combina similarita' vettoriale HNSW con keyword matching BM25 e filtri metadati via roaring bitmap. La ricerca graph-aware restringe i risultati a sottografi raggiungibili da un nodo radice. Il retrieval adattivo espande il contesto seguendo vicini semantici fino a un budget di token.

### Ingegneria

**Persistenza Affidabile.** Architettura ibrida AOF + Snapshot. Il Lazy AOF Writer raggruppa le operazioni (miglioramento throughput 10-100x) con fsync periodico. Framing binario TLVC con controlli di integrita' CRC32. Recovery automatico con resync su corruzione - i dati validi sono preservati anche se il file AOF e' parzialmente corrotto. Compattazione background via AOF rewrite con modalita' snapshot che previene la perdita di dati durante la manutenzione.

**Autenticazione JWT.** Token auto-contenuti ES256 (ECDSA P-256) con controllo accessi basato su ruoli (admin, write, read) e isolamento per namespace opzionale. Endpoint JWKS per verifica da parte di terzi. Denylist `jti` per revoca token. Nessuna memorizzazione lato server del token - il token contiene i propri claims.

**Grafo Auto-Ottimizzante.** La manutenzione in background mantiene l'indice in salute: **Vacuum** recupera memoria dai nodi eliminati e ripara le connessioni del grafo. **Refine** rivaluta continuamente le connessioni del grafo per migliorare la qualita' della ricerca nel tempo - piu' a lungo KektorDB e' in esecuzione, migliori diventano i risultati di ricerca. Entrambi sono configurabili per-indice e funzionano autonomamente.

**Eseguilo Come Vuoi.** Server REST autonomo, libreria Go embedded (zero overhead di rete), server MCP per agenti AI, Gateway/Proxy AI per RAG zero-code, o qualsiasi combinazione. SDK client Python, TypeScript e Go. Embedder ONNX integrato (`all-MiniLM-L6-v2`, 384 dim) per embedding locali zero-config. Strumenti CLI esterni possono elaborare documenti complessi tramite SmartLoader - configura un template di comando in `vectorizers.yaml` e KektorDB usa i parser integrati come fallback in caso di errore.

---

## Come Usare KektorDB

| Modalita' | Comando | Ideale per |
|-----------|---------|------------|
| **Server MCP** | `kektordb --mcp --tools=agent` | Memoria per agenti AI (Claude, Cursor, Codex, Gemini CLI, OpenCode) |
| **Server REST** | `./kektordb` | Backend API HTTP, qualsiasi linguaggio |
| **Libreria Go** | `import "github.com/sanonone/kektordb/pkg/engine"` | Embedded in-process, zero overhead di rete |
| **Gateway AI** | `./kektordb -enable-proxy -proxy-config=proxy.yaml` | RAG zero-code tra Chat UI e LLM |
| **Client Python/TS** | `pip install kektordb-client` | Integrazione applicativa |

---

## Avvio Rapido

### Python (API REST)

```python
from kektordb_client import KektorDBClient
from kektordb_client.cognitive import CognitiveSession

client = KektorDBClient(port=9091)
client.vcreate("agent_memory", metric="cosine")

# Salva memorie collegate a una sessione
with CognitiveSession(client, "agent_memory", user_id="user_42") as session:
    session.save_memory("L'utente sta costruendo un progetto Go chiamato KektorDB",
                        layer="episodic", tags=["project", "go"])
    session.save_memory("L'utente preferisce risposte concise con esempi",
                        layer="semantic", tags=["preference"])

# Cerca
results = client.vsearch("agent_memory", query_vector=embed("ultimo progetto"), k=5)
print(f"Trovate {len(results)} memorie rilevanti")

# Verifica cosa sa KektorDB di questo utente
profile = client.get_user_profile("user_42", "agent_memory")
print(f"Stile: {profile.get('communication_style')}")
```

### Go (Embedded)

```go
import "github.com/sanonone/kektordb/pkg/engine"

db, _ := engine.Open(engine.DefaultOptions("./data"))
defer db.Close()

db.VCreate("docs", distance.Cosine, 16, 200, distance.Float32, "", nil, nil, nil)
db.VAdd("docs", "vec1", []float32{0.1, 0.2, 0.3, 0.4}, map[string]any{"title": "Hello"})

results, _ := db.VSearch("docs", []float32{0.15, 0.25, 0.35, 0.45}, 10, "", "", 100, 1.0, nil)
for _, id := range results {
    data, _ := db.VGet("docs", id)
    fmt.Println(data.ID, data.Metadata["title"])
}
```

### Scarica e Avvia

```bash
# Scarica il binario dalla pagina Release di GitHub
wget https://github.com/sanonone/kektordb/releases/latest/download/kektordb-linux-amd64
chmod +x kektordb-linux-amd64

# Avvia il server
./kektordb-linux-amd64

# Oppure usa Docker
docker build -t kektordb .
docker run -p 9091:9091 -v $(pwd)/data:/data kektordb
```

---

## Benchmark

Hardware desktop (Intel i5-12500, SSD consumer). Confronto con Qdrant e ChromaDB via Docker host networking.

| Database | NLP QPS | Vision QPS | Recall@10 |
|----------|---------|------------|-----------|
| **KektorDB** | **1073** | **881** | 0.97 |
| Qdrant | 848 | 845 | 0.97 |
| ChromaDB | 802 | 735 | 0.96 |

> KektorDB e' ottimizzato per scenari embedded a nodo singolo. Per deployment distribuiti su scala miliardaria, considera soluzioni specializzate. [Report completo ->](BENCHMARKS.md)

---

## Embedding Integrato (Opzionale)

KektorDB include un embedder ONNX integrato opzionale (`all-MiniLM-L6-v2`, 384 dimensioni) basato su Rust/Candle per embedding locali zero-config - nessun Ollama richiesto.

**Build con supporto Rust:**
```bash
make build-rust-native    # richiede protoc (scaricato automaticamente dal Makefile)
make run-rust
```

Il modello ONNX (~90 MB) viene scaricato automaticamente da HuggingFace al primo avvio con verifica SHA256.

| Modalita' | Descrizione |
|-----------|-------------|
| `auto` | Rilevamento automatico: ONNX locale se disponibile, Ollama come fallback (default) |
| `ollama` / `ollama_api` | Usa API embedding di Ollama |
| `openai` / `openai_compatible` | Usa API embedding compatibile OpenAI |
| `gemini` / `google` | Usa API `embedContent` di Gemini |
| `local` | Modello ONNX integrato (richiede build `-tags rust`) |

---

## Ecosistema

| Risorsa | Descrizione |
|---------|-------------|
| [Documentazione](DOCUMENTATION.md) | Riferimento tecnico completo: architettura, API, configurazione |
| [Contributi](CONTRIBUTING.md) | Istruzioni di build, stile codice, processo PR |
| [Guida RAG](docs/guides/zero_code_rag.md) | RAG zero-code con Open WebUI in 5 passaggi |
| [Client Go](pkg/client/README.md) | Riferimento SDK Go |
| [Client Python](https://pypi.org/project/kektordb-client/) | `pip install kektordb-client` |
| [Client TypeScript](https://www.npmjs.com/package/kektordb-client) | `npm install kektordb-client` |
| [LangChain](https://python.langchain.com/) | `from kektordb_client.langchain import KektorVectorStore` |

---

## Roadmap

### v0.6.0 (corrente)

- **Stabilita' motore:** 6 bug P1+P2 risolti - riordino memory-before-AOF, nil-pointer Stat(), recovery corruzione AOF, perdita dati silenziosa alla chiusura, race taskIDCounter, leak task asincroni
- **MCP:** 57 strumenti (49 agent + 8 admin), prompt `memory_instructions`, supporto API Gemini
- **Auth:** Token JWT ES256, RBAC, endpoint JWKS, denylist `jti`
- **Recovery:** Resync AOF dopo corruzione, RewriteAOF con modalita' snapshot

### All'Orizzonte

- Wizard di setup interattivo (`kektordb init`) — configura tutto in 30 secondi
- Docker Hub + Docker Compose
- Git Sync (push/pull memorie tra macchine)
- Ottimizzazioni SIMD/AVX per piu' metriche di distanza
- API nativa di backup/restore

> [Apri una Issue](https://github.com/sanonone/kektordb/issues) per influenzare la roadmap.

---

## Contribuire

Se noti race condition, ottimizzazioni mancate o pattern Go non idiomatici, **apri una Issue o una PR** - ogni contributo e' benvenuto.

Vedi [CONTRIBUTING.md](CONTRIBUTING.md) per istruzioni di build e convenzioni di codice.

---

## Limitazioni Attuali

**Nodo singolo:** KektorDB non supporta il clustering. Scala verticalmente entro i limiti di una singola macchina.

---

## Licenza

Apache 2.0 - vedi [LICENSE](LICENSE).

---

## Supporto

<a href="https://ko-fi.com/sanon">
  <img src="https://ko-fi.com/img/githubbutton_sm.svg" alt="ko-fi" width="180">
</a>
