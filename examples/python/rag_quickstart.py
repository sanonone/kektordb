# File: examples/python/rag_quickstart.py
"""
KektorDB RAG (Retrieval-Augmented Generation) Quickstart Example.

This script demonstrates a complete workflow:
1.  Loading a sentence-transformer model for creating embeddings.
2.  Connecting to a KektorDB server.
3.  Indexing a small knowledge base of documents.
4.  Performing a filtered vector search to find relevant context.
5.  Constructing a prompt for a Large Language Model (LLM).

To run this example:
1.  Ensure the KektorDB server is running.
2.  Install the required Python libraries:
    pip install kektordb-client sentence-transformers torch
"""
import time
from sentence_transformers import SentenceTransformer
from kektordb_client import KektorDBClient, APIError

# --- 1. Configuration ---
KDB_HOST = "localhost"
KDB_PORT = 9091
INDEX_NAME = "rag_documents"
EMBEDDING_MODEL = 'all-MiniLM-L6-v2' # A fast, high-quality model

# --- 2. Initialize Connections ---
print(f"Connecting to KektorDB server at {KDB_HOST}:{KDB_PORT}...")
client = KektorDBClient(host=KDB_HOST, port=KDB_PORT)

print(f"Loading sentence-transformer model '{EMBEDDING_MODEL}'...")
# The first time this runs, it will download the model from the internet.
model = SentenceTransformer(EMBEDDING_MODEL)

# --- 3. Indexing Phase ---
def index_documents():
    """Creates an index and populates it with a sample knowledge base."""
    print(f"\n--- Phase: Indexing ---")
    
    try:
        print(f"Creating index '{INDEX_NAME}' with cosine similarity...")
        # We use 'cosine' for text embeddings and 'int8' for memory efficiency.
        client.vcreate(INDEX_NAME, metric="cosine", precision="float32") # Start with float32
    except APIError as e:
        if "already exists" in str(e):
            print(f"Index '{INDEX_NAME}' already exists. Skipping creation.")
        else:
            raise e

    documents = [
        {"id": "doc-01", "text": "The sky is blue due to Rayleigh scattering.", "author": "scientist_a", "year": 2021},
        {"id": "doc-02", "text": "A cat is a small carnivorous mammal.", "author": "scientist_b", "year": 2022},
        {"id": "doc-03", "text": "Photosynthesis is the process plants use to convert light into chemical energy.", "author": "scientist_a", "year": 2022},
        {"id": "doc-04", "text": "The domestic feline enjoys sleeping in sunbeams.", "author": "scientist_c", "year": 2023},
    ]

    print(f"Generating embeddings and indexing {len(documents)} documents...")
    for doc in documents:
        vector = model.encode(doc["text"]).tolist()
        
        metadata = {
            "text": doc["text"],
            "author": doc["author"],
            "year": doc["year"]
        }
        
        client.vadd(INDEX_NAME, doc["id"], vector, metadata)
        print(f"  > Indexed '{doc['id']}'")
    
    # Optional: Compress the index for memory savings
    print("\nCompressing index to int8 for memory efficiency...")
    try:
        client.vcompress(INDEX_NAME, precision="int8")
        print("Index compressed successfully.")
    except APIError as e:
        print(f"Could not compress index: {e}")


# --- 4. Retrieval Phase ---
def search_and_generate_prompt(query: str):
    """Searches for relevant documents and constructs an LLM prompt."""
    print(f"\n--- Phase: Retrieval ---")
    print(f"Query: '{query}'")

    # Generate embedding for the query
    query_vector = model.encode(query).tolist()
    
    # Perform a filtered search
    filter_str = "year > 2021"
    print(f"Searching for top 2 results with filter: '{filter_str}'")

    try:
        results_ids = client.vsearch(
            index_name=INDEX_NAME,
            k=2,
            query_vector=query_vector,
            filter_str=filter_str
        )
        print(f"Found relevant document IDs: {results_ids}")

        # In a real application, you would fetch the full text using the IDs.
        # Here we simulate it for the prompt.
        # This requires a 'vget' or multi-get functionality not yet in KektorDB,
        # so we'll just show the concept.
        
        context = f"Context from KektorDB search results (IDs: {', '.join(results_ids)}):\n"
        # Dummy context retrieval
        if "doc-02" in results_ids:
            context += "- A cat is a small carnivorous mammal.\n"
        if "doc-04" in results_ids:
             context += "- The domestic feline enjoys sleeping in sunbeams.\n"

        prompt = f"""
        Use the following context to answer the question.

        Context:
        ---
        {context}
        ---

        Question: {query}
        """

        print("\n--- Generated LLM Prompt ---")
        print(prompt)
        print("--------------------------")

    except APIError as e:
        print(f"Search failed: {e}")


if __name__ == "__main__":
    index_documents()
    time.sleep(1) # Give server a moment
    search_and_generate_prompt("What are cats?")
