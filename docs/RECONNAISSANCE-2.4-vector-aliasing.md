# 2.4 — Aliasing dei vettori restituiti

> **Stato: CHIUSO** in commit `795f82b` (2026-11-06).
>
> Questo documento contiene l'analisi originale, che ha guidato la decisione.
> Il fix, il costo misurato e le ottimizzazioni future sono in
> **`docs/vector-ownership.md`**.
>
> Riassunto: il problema si è rivelato **attivo**, non latente. Oltre alla
> corruzione per scrittura, un ciclo di background che leggeva un vettore dopo
> `Close()` andava in SIGSEGV (riprodotto al 100%). Il fix ha due parti — copia
> ai bordi pubblici e `activeMu` sulle letture — perché la sola copia spostava
> il crash dentro la copia stessa.

---

## 1. In cosa consiste

KektorDB conserva i vettori in una regione di memoria mappata su file (`mmap`).
Le API di lettura non copiano il vettore: restituiscono una slice che **punta
direttamente in quella regione**.

```go
// pkg/storage/mmap/arena.go:519
func (va *VectorArena) GetBytes(internalID uint32) ([]byte, error) {
    ...
    data := va.chunks[chunkID].Data[offset : offset+va.vectorSize]  // finestra sulla mmap
    return data, nil                                                 // nessuna copia
}
```

Il cast a `[]float32` è un `unsafe.Slice`, che non copia:

```go
// pkg/storage/mmap/arena.go
func BytesToFloat32Slice(b []byte, dim int) []float32 {
    return unsafe.Slice((*float32)(unsafe.Pointer(&b[0])), dim)
}
```

La catena completa:

```
file su disco  →  mmap  →  GetBytes (alias)  →  BytesToFloat32Slice (alias)
               →  Node.vec (alias)  →  GetNodeData (alias)
               →  DB.GetVector (alias)  →  Engine.VGet (alias)  →  chiamante
```

Nessun passaggio copia. Il chiamante riceve una slice che è, a tutti gli effetti,
una finestra sull'indice.

---

## 2. Prova che il problema esiste

Eseguito su `pkg/core` con un indice float32, 4 dimensioni, metrica coseno:

```go
got, _ := db.GetVector("idx", "doc1")
// got.Vector = [1 0 0 0]

got.Vector[0] = 999                    // sembra una modifica locale

again, _ := db.GetVector("idx", "doc1")
// again.Vector = [999 0 0 0]           ← l'indice è cambiato
```

E l'effetto non è cosmetico: **la ricerca si rompe**.

```
vettore originale:          [1 0 0 0 0 0 0 0]
dopo una sola scrittura:    [999 0 0 0 0 0 0 0]
ricerca con l'originale:    [{1 -998}]
```

Il punteggio è **-998**. Una distanza coseno vive in `[0, 2]`: un valore così
significa che il nodo è stato reso irraggiungibile dalla ricerca.

### Perché il danno è silenzioso

- Non c'è errore: `vec[0] = 999` è un'operazione Go perfettamente legale
- Non c'è log: nessuno sorveglia le scritture nella mmap
- Non c'è test: nessun test verifica che il vettore restituito sia isolato
- Il sintomo appare lontano: la ricerca restituisce risultati strani, senza
  indicare quale nodo è stato corrotto

---

## 3. Ambito: quali percorsi sono interessati

| Funzione | Aliasing? | Note |
|---|---|---|
| `VectorArena.GetBytes` | **sì** | origine della catena |
| `BytesToFloat32Slice` | **sì** | `unsafe.Slice` |
| `Node.GetVectorF32` | **sì** | `return vd.F32` |
| `Node.GetVectorF16/I8` | **sì** | idem |
| `Index.GetNodeData` | **f32: sì** | f16/int8 dequantizzano in una slice nuova → copia |
| `Index.IterateRaw` | **sì** | passa l'alias al callback |
| `DB.GetVector` / `GetVectors` | **sì** (f32) | `Vector: nodeData.Vector`, nessun `append` |
| `Engine.VGet` / `VGetMany` | **sì** (f32) | inoltrano senza copiare |
| HTTP `GET /vectors/{id}` | no | serializza in JSON nello stesso handler |
| Go/Python/TS client | no | il JSON viene deserializzato in memoria nuova |

**Precisione**: il problema riguarda **float32** (il default). Gli indici float16
e int8 dequantizzano durante la lettura, quindi `DB.GetVector` per quei tipi
restituisce già una copia. Restano aliasati solo gli accessor interni
(`GetVectorF16/I8`, `IterateRaw`).

**Modalità di storage**: il problema esiste in entrambe.

| Modalità | Quando | Cosa aliasa la slice |
|---|---|---|
| Arena | `arenaDir != ""` (produzione: `Engine.Open`) | la regione mmap del file |
| Heap | `arenaDir == ""` (test unitari) | la slice Go originale passata ad `Add` |

---

## 4. Chi lo tocca oggi: tutti in sola lettura

Ricognizione esaustiva di ogni call site. **Nessuno scrive.**

| Chiamante | Uso | Verdetto |
|---|---|---|
| `internal/server` (`handleGetVector`) | serializza in JSON, non trattiene | sola lettura |
| `internal/mcp` (`cosineSimilarity`) | legge per similarità | sola lettura |
| `internal/mcp` (`UnpinMemory`) | passa a `VAdd` | sicuro: `Add` copia prima di normalizzare |
| `pkg/cognitive/gardener` | legge per medie e ricerche | sola lettura |
| `pkg/compiler` | accumula in una slice nuova | sola lettura |
| `pkg/engine/epistemic` | legge per centroidi | sola lettura |
| `internal/server` (`cloneGraphNode`) | `append([]float32(nil), ...)` | copia esplicita |
| `pkg/core` (`Compress`) | copia esplicita, con commento sul perché | copia esplicita |
| `pkg/transfer/memory` | trattiene l'alias e lo passa a `AddBatch` | **esaminato, sicuro** |

### Il caso `transfer` — esaminato e scartato

`pkg/transfer/memory.go:121` fa `newVec = item.Vector` (alias) e poi
`eng.VAddBatch(...)` → `Index.AddBatch`, che **normalizza in place**:

```go
// pkg/core/hnsw/hnsw_index.go:1269
if h.metric == distance.Cosine && h.precision == distance.Float32 {
    normalize(obj.Vector)     // scrive nella slice del chiamante
}
```

Sembrava una corruzione cross-index. Verificato eseguendo:

- **Coseno**: il vettore riletto è già normalizzato (lo normalizza `Add` in fase
  di scrittura), quindi normalizzare di nuovo è idempotente → nessun danno
- **Euclideo**: `normalize` non viene chiamata → nessun danno

Nessuno dei due casi corrompe. **Non è raggiungibile oggi.**

### L'unica scrittura in place in produzione

```go
// pkg/core/hnsw/hnsw_index.go:1269 — AddBatch
normalize(obj.Vector)
```

È l'unico punto che scrive in una slice ricevuta dall'esterno. Non è
un'esploitazione dell'aliasing: opera sull'**input** di `AddBatch`, non su un
vettore recuperato. Va comunque corretto, perché mutare l'argomento di una
funzione è sorprendente, ma è un problema diverso e separato.

---

## 5. Perché non è stato corretto ora

Tre ragioni, in ordine di peso.

**1. È latente.** Nessun percorso in produzione scrive nel vettore restituito.
Il rischio di lasciarlo com'è è teorico; il rischio di toccare il percorso caldo
senza misure è concreto.

**2. Non ci sono dati di performance.** `GetVectors` è usato per idratare grafi:
copiare 1000 vettori da 384 float significa 1.5 MB di allocazioni per chiamata,
su un percorso già parallelo. Senza un benchmark pre/post la scelta tra "copiare
sempre" e "copiare solo ai bordi" è un'opinione.

**3. La soluzione ha un tradeoff di design, non solo di codice.** L'aliasing è
il motivo per cui la ricerca è veloce: la distanza legge direttamente dalla
mmap, senza allocazioni, migliaia di volte al secondo. Un fix affrettato può
degradare proprio la proprietà che rende KektorDB competitivo.

---

## 6. Le quattro strade, con i loro rischi

### A — Copiare sempre

```go
return VectorData{Vector: append([]float32(nil), nodeData.Vector...)}
```

| Pro | Contro |
|---|---|
| Sicuro per costruzione: nessun chiamante può corrompere | Alloca su ogni lettura |
| Una riga per punto di ritorno | Il GC lavora di più su `VGetMany` |
| Nessuna disciplina da ricordare | Impatta il percorso di idratazione dei grafi |

**Rischio principale**: regressione di performance non misurata.

### B — Copiare solo ai bordi pubblici (raccomandata, se si interviene)

Copiare in `Engine.VGet`/`VGetMany`, lasciare l'alias dentro `pkg/core` e nella
ricerca.

| Pro | Contro |
|---|---|
| Costo zero sui percorsi interni (ricerca, snapshot, compaction) | Richiede disciplina: un metodo pubblico nuovo deve ricordarsene |
| Copre tutto ciò che un utente embedded può toccare | Due contratti diversi nello stesso package |
| È la convenzione Go: i bordi copiano, il centro ottimizza | `DB.GetVector` resta un trabocchetto per chi lavora nel package |

**Rischio principale**: regressione futura, se qualcuno aggiunge un'API pubblica
senza copiare. Mitigabile con un test dedicato.

### C — Documentare e lasciare aperto ← **scelta effettuata**

| Pro | Contro |
|---|---|
| Zero rischio di regressione | **Non impedisce il bug**: `vec[0]=x` funziona lo stesso |
| Il percorso caldo resta intatto | Serve che chi legge la doc la rispetti |
| Il problema resta tracciato per un'analisi con dati | Un uso embedded scorretto può corrompere l'indice |

**Rischio principale**: un utente embedded che modifica il vettore. Mitigato da:
godoc esplicita su tutti i metodi coinvolti, sezione nel documento, e un
`TODO(aliasing)` nel codice che punta qui.

### D — Pool di buffer

| Pro | Contro |
|---|---|
| Costo di allocazione quasi zero | Complessità: chi libera, quando |
| | Un buffer riusato è di nuovo un alias → stesso bug |
| | Chi riceve deve sapere che il buffer scade |

**Rischio principale**: reintroduce il problema in forma più difficile da
diagnosticare. Sconsigliata.

---

## 7. Cosa serve prima di decidere

Un benchmark, in sola lettura, che misuri:

1. `VGet` singolo: costo attuale vs con copia
2. `VGetMany` con 100 / 1000 / 10000 vettori da 384 dimensioni
3. Impatto su `VGetConnections` (idratazione di grafi, il caso d'uso reale)
4. Allocazioni per operazione (`testing.B.ReportAllocs`)

**Criterio di decisione proposto**: se la copia ai bordi costa meno del 2% in
tempo e allocazioni ragionevoli, adottare **B** e chiudere il problema. Se costa
di più, rivalutare se il contratto "read-only" documentato è sufficiente.

---

## 8. Cosa è stato fatto ora

Solo documentazione, nessun cambio di comportamento:

| File | Cosa |
|---|---|
| `pkg/storage/mmap/arena.go` | `GetBytes`: avviso che la slice è una finestra sulla mmap, non va modificata né trattenuta oltre la compaction |
| `pkg/core/hnsw/hnsw_node.go` | `GetVectorF32`: avviso che il risultato aliata memoria viva |
| `pkg/core/hnsw/hnsw_index.go` | `GetNodeData`: distingue il caso f32 (alias) da f16/int8 (copia) |
| `pkg/core/hnsw/hnsw_index.go` | `IterateRaw`: avviso sul callback |
| `pkg/core/core.go` | `GetVector`/`GetVectors`: contratto completo + esempio di clone |
| `pkg/engine/ops.go` | `VGet`/`VGetMany`: contratto + `TODO(aliasing)` con rimando a questo documento |

Verificato che i commenti non alterino il comportamento: suite completa e
`gofmt`/`go vet` invariati.

---

## 9. Come riprendere

1. Scrivere il benchmark della sezione 7 (in sola lettura, nessun rischio)
2. Decidere tra A e B sulla base dei numeri
3. Implementare la copia nel punto scelto
4. Aggiungere un test che **fallisca** se un'API pubblica restituisce un alias:

```go
func TestVGetReturnsCopy(t *testing.T) {
    data, _ := eng.VGet("idx", "doc1")
    original := data.Vector[0]
    data.Vector[0] = 999

    again, _ := eng.VGet("idx", "doc1")
    if again.Vector[0] != original {
        t.Fatal("VGet returns an alias: writing to the result corrupts the index")
    }
}
```

5. Rimuovere il `TODO(aliasing)` e questo stato "aperto"

---

## 10. Problemi collegati

| ID | Relazione |
|---|---|
| **P26** | Le shadow write di uno snapshot fallito dopo un truncate riuscito vanno perse. Stessa area (durabilità), fix diverso |
| **`AddBatch` normalizza in place** | `hnsw_index.go:1269` muta l'argomento del chiamante. Non è un caso di aliasing ma è una mutazione sorprendente: da correggere separatamente |
| **P18** | `GET /kv/{key}` corrompe i valori binari in JSON. Stessa classe: dati che attraversano un confine e si trasformano in silenzio |
