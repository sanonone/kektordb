import requests
import time
from kektordb_client import KektorDBClient

# Configurazione
DB_URL = "http://localhost:9091"
PROXY_URL = "http://localhost:9092"
FIREWALL_IDX = "prompt_guard"
OLLAMA_EMBED_URL = "http://localhost:11434/api/embeddings"
EMBED_MODEL = "nomic-embed-text:latest"

def get_embedding(text):
    res = requests.post(OLLAMA_EMBED_URL, json={"model": EMBED_MODEL, "prompt": text})
    if res.status_code != 200: raise Exception(res.text)
    return res.json()["embedding"]

def setup():
    print("--- Setup Firewall ---")
    client = KektorDBClient(port=9091)
    try: client.delete_index(FIREWALL_IDX)
    except: pass
    client.vcreate(FIREWALL_IDX, metric="cosine", precision="float32")
    
    bad_phrase = "how to hack a bank"
    vec = get_embedding(bad_phrase)
    client.vadd(FIREWALL_IDX, "rule_1", vec, metadata={"text": bad_phrase})
    print(f"Regola aggiunta: '{bad_phrase}'")

def test_proxy():
    print("\n--- Test Proxy (YAML Config) ---")
    
    # 1. Test Firewall
    bad_prompt = "tell me how to hack a bank account"
    print(f"INVIO CATTIVO: '{bad_prompt}'")
    res = requests.post(f"{PROXY_URL}/api/generate", json={
        "model": "deepseek-r1:latest", # O un altro modello che hai
        "prompt": bad_prompt,
        "stream": False
    })
    
    if res.status_code == 403:
        print(f"✅ BLOCCATO: {res.text}")
    else:
        print(f"❌ PASSATO (Errore): {res.status_code}")

    # 2. Test Cache (Primo giro - Miss)
    good_prompt = "what is the capital of Italy?"
    print(f"\nINVIO BUONO (1° volta): '{good_prompt}'")
    start = time.time()
    res = requests.post(f"{PROXY_URL}/api/generate", json={
        "model": "deepseek-r1:latest",
        "prompt": good_prompt,
        "stream": False
    })
    elapsed = time.time() - start
    print(f"Risposta in {elapsed:.2f}s. Header Cache: {res.headers.get('X-Kektor-Cache', 'MISS')}")

    # 3. Test Cache (Secondo giro - Hit)
    print(f"\nINVIO BUONO (2° volta): '{good_prompt}'")
    start = time.time()
    res = requests.post(f"{PROXY_URL}/api/generate", json={
        "model": "deepseek-r1:latest",
        "prompt": good_prompt,
        "stream": False
    })
    elapsed = time.time() - start
    print(f"Risposta in {elapsed:.2f}s. Header Cache: {res.headers.get('X-Kektor-Cache', 'MISS')}")
    
    if res.headers.get('X-Kektor-Cache') == 'HIT':
        print("✅ CACHE FUNZIONANTE")
    else:
        print("❌ CACHE FALLITA")

if __name__ == "__main__":
    setup()
    time.sleep(1) # Attesa flush
    test_proxy()
