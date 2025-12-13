import time
from kektordb_client import KektorDBClient

# Setup
client = KektorDBClient(port=9091)
INDEX = "graph_test"

# 1. Creazione Indice
try: client.delete_index(INDEX)
except: pass
client.vcreate(INDEX, metric="cosine", precision="float32", m=16, ef_construction=200)
print(f"✅ Indice '{INDEX}' creato.")

# 2. Dati Simulati
# Immagina che questi vettori siano embedding reali
doc_embedding = [0.9, 0.9, 0.9]   # Il documento "Padre"
chunk_embedding = [0.91, 0.91, 0.91] # Un chunk molto simile (figlio)

# Inseriamo i dati
client.vadd(INDEX, "doc_manual_v1", doc_embedding, metadata={"title": "Manuale Utente 2024", "type": "document"})
client.vadd(INDEX, "chunk_001", chunk_embedding, metadata={"text": "Per accendere premere ON", "type": "chunk"})
print("✅ Dati inseriti (Doc + Chunk).")

# 3. Creazione Relazione (Il Grafo)
# Colleghiamo il Chunk al Padre
print("🔗 Creazione Link: chunk_001 -> (parent) -> doc_manual_v1")
client.vlink("chunk_001", "doc_manual_v1", "parent")

# Opzionale: Link bidirezionale manuale o altro tipo
# client.vlink("doc_manual_v1", "chunk_001", "child") 

# 4. Ricerca GraphRAG
print("\n🔎 Esecuzione Ricerca GraphRAG...")
# Simuliamo una query simile al chunk
query_vec = [0.92, 0.92, 0.92] 

# Chiediamo esplicitamente di includere la relazione "parent"
results = client.vsearch(
    INDEX, 
    query_vec, 
    k=1, 
    include_relations=["parent"]
)

# 5. Verifica
print("\nRisultati:")
for res in results:
    print(res)
    
    # Verifica Automatica
    if res['id'] == "chunk_001":
        rels = res.get('relations', {})
        parents = rels.get('parent', [])
        
        if "doc_manual_v1" in parents:
            print("\n✅ SUCCESSO! Il chunk ha riportato correttamente il suo genitore.")
        else:
            print("\n❌ ERRORE: Relazione 'parent' mancante o errata.")
    else:
        print("\n⚠️ Warning: Il primo risultato non è il chunk atteso (normale se i vettori sono troppo simili).")

# Pulizia
# client.delete_index(INDEX)
