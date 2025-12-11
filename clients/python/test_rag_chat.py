import sys
import requests
import json
from kektordb_client import KektorDBClient

# --- CONFIGURAZIONE ---
# Assicurati che questi parametri combacino con il tuo vectorizers.yaml
KEKTOR_HOST = "localhost"
KEKTOR_PORT = 9091
INDEX_NAME = "knowledge_base" # O il nome che hai messo nel yaml

# Configurazione Ollama
OLLAMA_URL = "http://localhost:11434"
EMBED_MODEL = "nomic-embed-text" # Deve essere LO STESSO usato per l'ingestione
CHAT_MODEL = "deepseek-r1:latest" # O gemma3:12b, o quello che preferisci per rispondere

def get_embedding(text):
    """Chiede a Ollama il vettore per la domanda."""
    resp = requests.post(
        f"{OLLAMA_URL}/api/embeddings",
        json={"model": EMBED_MODEL, "prompt": text}
    )
    if resp.status_code != 200:
        print(f"Errore Embedding: {resp.text}")
        sys.exit(1)
    return resp.json()["embedding"]

def generate_answer(context, question):
    """Chiede a Ollama di rispondere usando il contesto."""
    prompt = f"""Use the following pieces of context to answer the user's question.
If you don't know the answer based on the context, just say that you don't know, don't try to make up an answer.

Context:
{context}

Question: {question}

Answer:"""

    # Usa /api/generate per una risposta secca (non stream per semplicità)
    resp = requests.post(
        f"{OLLAMA_URL}/api/generate",
        json={
            "model": CHAT_MODEL,
            "prompt": prompt,
            "stream": False
        }
    )
    if resp.status_code != 200:
        print(f"Errore Generation: {resp.text}")
        return "Error generating answer."
    return resp.json()["response"]

def main():
    if len(sys.argv) < 2:
        print("Uso: python3 test_rag_chat.py \"La tua domanda qui\"")
        sys.exit(1)

    question = sys.argv[1]
    print(f"\n🔎 Domanda: {question}")

    # 1. Connessione a KektorDB
    client = KektorDBClient(host=KEKTOR_HOST, port=KEKTOR_PORT)

    # 2. Embedding della domanda
    print("   ... Calcolo embedding domanda ...")
    query_vec = get_embedding(question)

    # 3. Retrieval (Ricerca su KektorDB)
    print("   ... Ricerca documenti pertinenti ...")
    # Cerchiamo i top 3 risultati
    results_ids = client.vsearch(INDEX_NAME, query_vec, k=13, ef_search=100)
    
    if not results_ids:
        print("❌ Nessun documento trovato nel database.")
        sys.exit(0)

    # Recuperiamo il contenuto testuale (Metadata)
    vectors_data = client.vget_many(INDEX_NAME, results_ids)
    
    context_text = ""
    print(f"\n📄 Contesto Recuperato ({len(vectors_data)} items):")
    
    for i, item in enumerate(vectors_data):
        meta = item.get("metadata", {})
        doc_id = item.get("id", "NO_ID")
        
        # --- DEBUG: STAMPA TUTTO QUELLO CHE TROVI ---
        print(f"   [{i+1}] ID: {doc_id}")
        print(f"       Keys trovate: {list(meta.keys())}")
        # --------------------------------------------

        # Tentativo di recupero intelligente
        # Cerca 'content' O 'text' O 'page_content'
        text_chunk = meta.get("content") or meta.get("text") or meta.get("page_content") or ""
        
        # Cerca 'source' O 'source_file' O 'file_path'
        source = meta.get("source") or meta.get("source_file") or meta.get("file_path") or "unknown"
        
        # Pulisce il testo per la stampa (toglie a capo eccessivi)
        preview = text_chunk[:100].replace('\n', ' ') if text_chunk else "[[TESTO VUOTO]]"
        
        print(f"       Source: {source}")
        print(f"       Preview: \"{preview}...\"")
        print("       ---")

        if text_chunk:
            context_text += f"Source: {source}\nContent:\n{text_chunk}\n\n"

    # 4. Generation (LLM)
    print(f"\n🤖 Generazione Risposta con {CHAT_MODEL}...")
    answer = generate_answer(context_text, question)

    print("\n" + "="*40)
    print(answer)
    print("="*40 + "\n")

if __name__ == "__main__":
    main()
