# P27 — Proprietà dei vettori restituiti

> **Stato: corretto** in `795f82b`.
> Documento di riferimento per il fix, il costo misurato e le ottimizzazioni future.
> Analisi originale dell'aliasing: `docs/RECONNAISSANCE-2.4-vector-aliasing.md`.

---

## 1. Il problema

I vettori vivono in un'arena mappata su file (`mmap`). `GetBytes` restituisce una
slice che punta **direttamente** in quella regione, e `BytesToFloat32Slice` la
reinterpreta con `unsafe.Slice` senza copiare. Nessun passaggio copiava, fino al
chiamante.

Aveva **due conseguenze distinte**, che richiedevano due fix diversi.

### A — Scrittura che corrompe l'indice

Chi riceveva il vettore e ci scriveva dentro modificava i dati dell'indice:

```
letto:          [1 0 0 0]
got.Vector[0] = 999
riletto:        [999 0 0 0]        ← l'indice è cambiato
ricerca:        [{1 -998}]         ← score fuori scala, nodo irraggiungibile
```

Nessun percorso in produzione lo faceva, ma l'API pubblica `Engine.VGet` lo
permetteva.

### B — Lettura dopo `Close()`: SIGSEGV

`Engine.Close()` smappa le arene. Un ciclo di background che aveva già ottenuto
una slice e la leggeva dopo, toccava **memoria non mappata**. Non è un errore Go:
è un fault del sistema operativo, il processo muore.

**Riprodotto al 100%** con `go test -race ./pkg/cognitive/`:

```
fatal error: fault
[signal SIGSEGV: segmentation violation]
(*Gardener).consolidateCluster(...)
	gardener.go:1347   ← item.Vector[j] su un'arena già smappata
```

Questo era il problema **attivo**: non serviva che nessuno scrivesse. Bastava che
un ciclo di consolidamento sopravvivesse al Close.

---

## 2. La soluzione

Due parti, perché una sola non basta.

### Parte 1 — Copia ai bordi pubblici

`Engine.VGet` e `Engine.VGetMany` restituiscono **copie proprietarie**.

```go
func (e *Engine) VGet(indexName, id string) (core.VectorData, error) {
    data, err := e.DB.GetVector(indexName, id)
    if err != nil { return data, err }
    data.Vector = cloneVector(data.Vector)
    return data, nil
}
```

`cloneVector` preserva il caso `nil`, che è distinto da una slice vuota: chi
testa `Vector == nil` per dire "nessun vettore" continua a funzionare.

**Perché ai bordi e non in `DB.GetVector`**: la ricerca, la compaction e lo
snapshot leggono l'arena direttamente e non devono pagare nulla. Solo il bordo
pubblico copia.

### Parte 2 — `activeMu` sulle letture

`GetNodeData`, `Iterate` e `IterateRaw` ora prendono `activeMu.RLock` e
controllano `isClosed()`:

```go
func (h *Index) GetNodeData(externalID string) (types.NodeData, bool) {
    if h.isClosed() { return types.NodeData{}, false }
    h.activeMu.RLock()
    defer h.activeMu.RUnlock()
    ...
}
```

`Close()` usa già `activeMu` per aspettare le operazioni in volo prima di
smappare. Le letture ora sono tra quelle operazioni.

**Perché serviva**: con la sola copia, il segfault **si è spostato dentro
`cloneVector`**. Il chiamante leggeva un'arena già chiusa: la copia leggeva
memoria non mappata. La copia protegge il chiamante dai propri errori;
`activeMu` protegge dal ciclo di vita dell'indice.

---

## 3. Il costo misurato

Benchmark: `pkg/engine/vector_copy_bench_test.go`, 384 dimensioni.

| Operazione | Prima | Dopo | Delta |
|---|---|---|---|
| `VGet` singolo | 330 ns, 336 B, 2 alloc | 752 ns, 1872 B, 3 alloc | **+422 ns, +1.5 KB** |
| `VGetMany(100)` | 80 µs, 46 KB, 218 alloc | 123 µs, 200 KB, 318 alloc | **+43 µs, +153 KB** |
| **`VSearch` (percorso caldo)** | **6.0 µs** | **5.6 µs** | **invariato** |

La ricerca non paga: legge l'arena nel calcolo delle distanze e non passa mai da
questo percorso. Lo 0.4 µs di differenza è rumore di misura.

---

## 4. Cos'è l'idratazione e quando costa

**Idratazione** = recuperare il contenuto completo di un nodo (vettore +
metadati) partendo da un ID. Si distingue dalla **ricerca**, che dato un vettore
query trova i K ID più vicini.

| Operazione | Cosa fa | Costo del fix |
|---|---|---|
| `VSearch` | trova i K più vicini | **zero** |
| `VGet` / `VGetMany` | dà il nodo completo | +422 ns / +430 ns per vettore |
| Compaction, vacuum | sposta vettori nell'arena | **zero** |
| Snapshot | serializza (senza vettori) | **zero** |
| `VAdd` | inserisce | **zero** |

### I casi d'uso reali

**1. Dopo una ricerca** — recuperare i 10 risultati:

```
ricerca 5.6 µs + 10 × 0.42 µs = 9.8 µs   (era 5.6 µs)
```

Su un `search-then-fetch` di 10 risultati il costo relativo **raddoppia**. Ma il
server usa `VSearchGraph(hydrate=true)`, che fa tutto in una chiamata: il costo
c'è, ma dentro un'unica operazione già dominata dalla traversata.

**2. Idratazione di grafi** — `VGetConnections(hydrate=true)` su un nodo con 100
connessioni: **+43 µs, +153 KB**. È il caso peggiore, e resta sotto i 50 µs.

**3. Consolidamento del Gardener** — cluster di 2-10 membri: **+0.4-4 µs**.

**4. Export** — 10.000 nodi: **+4.3 ms, +15 MB** su un'operazione che già impiega
centinaia di ms.

**5. HTTP `GET /vectors/{id}`** — +0.4 µs, contro ~10 µs di serializzazione JSON.
**Il costo della copia è rumore.**

---

## 5. Quanto margine c'è per ottimizzare

Analisi dei **87 call site** di `VGet`/`VGetMany` in produzione:

| Cosa usa il risultato | Call site | Percentuale |
|---|---|---|
| Solo `.Metadata` | **53** | **61%** |
| Anche `.Vector` | 21 | 24% |
| Indeterminato (passthrough, check esistenza) | 13 | 15% |

**Il 61% dei chiamanti paga 1.5 KB di copia per un vettore che non legge.**

Esempi verificati:

- `internal/mcp/service.go:1035` — legge `item.Metadata["content"]`
- `internal/server/http_handlers.go:2055` — legge `data.Metadata["expertise_areas"]`
- `pkg/rag/adaptive_retriever.go:155` — `ar.extractContent(chunk.Metadata)`
- `internal/server/http_handlers.go:1544` — `Metadata: item.Metadata` nella risposta

### L'ottimizzazione con il rapporto migliore

Una variante che non restituisce il vettore:

```go
// oggi: copia sempre
data := eng.VGet("mem", id)
fmt.Println(data.Metadata["content"])   // 1.5 KB copiati per niente

// possibile: nessuna copia del vettore
data := eng.VGetMetadata("mem", id)
```

Non è solo più veloce: è **più corretto semanticamente**. Chi chiede i metadati
non dovrebbe ricevere (e pagare) il vettore.

**Guadagno stimato**: sul 61% dei call site, il costo del fix scende a zero.
Rimarrebbe solo sul 24% che usa davvero il vettore, dove è inevitabile.

### Altre strade, e perché le sconsiglio

| Opzione | Perché no |
|---|---|
| Pool di buffer | Un buffer riusato è di nuovo un alias: reintroduce il problema in forma più difficile da diagnosticare |
| Copia lazy (copy-on-write) | Complesso in Go, e il tipo restituito diventerebbe mutabile in modo sorprendente |
| Non copiare e documentare | È lo stato precedente: ha prodotto un SIGSEGV |
| Non smappare mai | 64 MB per indice mai rilasciati, inaccettabile per uso embedded |

---

## 6. La decisione

**Il costo attuale è accettabile.** Le ragioni:

1. **Il percorso caldo è intatto.** La ricerca — l'operazione che definisce un
   database vettoriale — non paga nulla.
2. **Dove si paga, è poco in assoluto.** 422 ns per un vettore, 43 µs per
   un'idratazione di 100 nodi. Su un'operazione da 80 µs è un +54%, non un
   raddoppio del sistema.
3. **Il costo maggiore (61% dei casi) è eliminabile** con `VGetMetadata`, che è
   un guadagno netto e non un compromesso.
4. **In cambio si chiude un crash del processo.** Un SIGSEGV non è un tradeoff
   negoziabile con dei nanosecondi.

### Priorità per il futuro

| Ottimizzazione | Guadagno | Costo | Priorità |
|---|---|---|---|
| `VGetMetadata` (senza vettore) | elimina il costo sul 61% dei call site | ~1 giorno | **Alta**, quando l'idratazione pesa |
| Pool di buffer per `VGetMany` | riduce le allocazioni | ~2 giorni | Bassa: reintroduce l'alias |
| Copia solo se `.Vector` è realmente letto | richiede analisi statica o lazy | alto | Bassa |

### Quando rivalutare

Se l'idratazione diventa un collo di bottiglia misurabile — cioè se un profilo
mostra `cloneVector` o `runtime.mallocgc` sopra il 10% del tempo in un carico
reale — allora `VGetMetadata` è il primo passo, non il pool di buffer.

---

## 7. Cosa impedisce la regressione

`pkg/engine/vector_ownership_test.go` (5 test):

| Test | Cosa pinna |
|---|---|
| `TestVGetReturnsOwnedCopy` | scrivere nel risultato non tocca l'indice |
| `TestVGetManyReturnsOwnedCopies` | idem sul percorso batch |
| `TestVGetAfterCloseDoesNotCrash` | leggere dopo `Close()` non uccide il processo |
| `TestIterateAfterCloseDoesNotCrash` | idem per l'iterazione |
| `TestNilVectorStaysNil` | `nil` resta distinto da slice vuota |

`pkg/engine/vector_copy_bench_test.go` misura il costo a ogni run, così una
regressione di performance si vede invece di essere scoperta tardi.

**Controprove eseguite**:

- rimuovendo le copie: `writing to a retrieved vector changed the index: got 999, want 1`
- rimuovendo `activeMu` da `GetNodeData`: il SIGSEGV torna nella suite `-race`

---

## 8. Cosa resta aperto

| ID | Problema | Nota |
|---|---|---|
| P28 | `AddBatch` normalizza in place la slice del chiamante | Una riga, non toccato: muta l'argomento, non un vettore riletto |
| — | `DB.GetVector`/`GetVectors` aliasano ancora | Deliberato: sono interni a `pkg/core`, documentati come tali. Chi li chiama oggi è codice interno che non trattiene la slice |
