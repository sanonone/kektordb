import requests
import json
from kektordb_client import KektorDBClient, KektorVectorStore

# Configurazione
OLLAMA_URL = "http://localhost:11434/api/embeddings"
MODEL_NAME = "nomic-embed-text" # Il modello che hai installato
KEKTOR_HOST = "localhost"
KEKTOR_PORT = 9091

class OllamaEmbeddingAdapter:
    """
    Questa classe simula un oggetto 'Embeddings' di LangChain.
    Chiama direttamente il tuo Ollama locale.
    """
    def __init__(self, model):
        self.model = model

    def _call_ollama(self, text):
        response = requests.post(
            OLLAMA_URL,
            json={"model": self.model, "prompt": text}
        )
        if response.status_code != 200:
            raise Exception(f"Ollama Error: {response.text}")
        return response.json()["embedding"]

    def embed_documents(self, texts):
        """Converte una lista di testi in una lista di vettori."""
        print(f"   ...Generazione embedding per {len(texts)} documenti con Ollama...")
        embeddings = []
        for text in texts:
            embeddings.append(self._call_ollama(text))
        return embeddings

    def embed_query(self, text):
        """Converte una singola query in un vettore."""
        print(f"   ...Generazione embedding per query: '{text}'...")
        return self._call_ollama(text)

def main():
    print("--- TEST INTEGRAZIONE RAG (KektorDB + Ollama) ---")

    # 1. Inizializza Client
    try:
        client = KektorDBClient(host=KEKTOR_HOST, port=KEKTOR_PORT)
        # Verifica connessione (opzionale, proviamo a listare gli indici)
        client.list_indexes()
        print("✅ Connessione a KektorDB riuscita.")
    except Exception as e:
        print(f"❌ Impossibile connettersi a KektorDB: {e}")
        return

    # 2. Prepara Adapter per Ollama
    embedder = OllamaEmbeddingAdapter(model=MODEL_NAME)
    
    # 3. Inizializza il VectorStore (Il tuo nuovo wrapper)
    index_name = "test_rag_langchain"
    
    # Pulizia preventiva
    try: client.delete_index(index_name)
    except: pass
    
    # Creiamo l'indice (Cosine è standard per nomic-embed-text)
    client.vcreate(index_name, metric="cosine", precision="float32", m=16, ef_construction=200)
    print(f"✅ Indice '{index_name}' creato.")

    store = KektorVectorStore(
        client=client,
        index_name=index_name,
        embedding_function=embedder
    )

    # 4. Aggiungi Documenti (Frasi di test)
    texts = [
        "Il gatto dorme sul divano.",
        "KektorDB è un database veloce scritto in Go.",
        "La pizza margherita è nata a Napoli.",
        "Il cane corre nel parco.",
        "Go usa le goroutine per la concorrenza."
    ]
    metadatas = [
        {"category": "animali"},
        {"category": "tech"},
        {"category": "cibo"},
        {"category": "animali"},
        {"category": "tech"}
    ]

    print("\n--- Caricamento Dati ---")
    store.add_texts(texts, metadatas=metadatas)
    print("✅ Documenti caricati e vettorializzati.")

    # 5. Test Ricerca Semantica
    query = "software e programmazione"
    print(f"\n--- Ricerca Semantica: '{query}' ---")
    
    results = store.similarity_search(query, k=2)

    found_tech = False
    for res in results:
        print(f"  -> Trovato: {res['page_content']} (Meta: {res['metadata']})")
        if "KektorDB" in res['page_content'] or "Go" in res['page_content']:
            found_tech = True

    if found_tech:
        print("\n✅ TEST SUPERATO! La ricerca ha trovato i concetti corretti.")
    else:
        print("\n❌ TEST FALLITO. Risultati non pertinenti.")

    # 6. Test Ricerca con Filtro Metadata
    print(f"\n--- Ricerca con Filtro (category='cibo') ---")
    results_filtered = store.similarity_search("qualcosa da mangiare", k=1, filter="category='cibo'")
    
    if results_filtered and "pizza" in results_filtered[0]['page_content']:
        print(f"  -> Trovato: {results_filtered[0]['page_content']}")
        print("✅ TEST FILTRO SUPERATO!")
    else:
        print(f"❌ TEST FILTRO FALLITO. Risultato: {results_filtered}")

    # Pulizia
    client.delete_index(index_name)

if __name__ == "__main__":
    main()
