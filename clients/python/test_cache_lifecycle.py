import requests
import time
import sys
from kektordb_client import KektorDBClient

PROXY_URL = "http://localhost:9092"
CACHE_INDEX = "semantic_cache_test" # Stesso nome del YAML

def ask_proxy(prompt):
    print(f"Chiedo: '{prompt}'")
    start = time.time()
    try:
        res = requests.post(f"{PROXY_URL}/api/generate", json={
            "model": "deepseek-r1:latest",
            "prompt": prompt,
            "stream": False
        })
        elapsed = time.time() - start
        
        if res.status_code != 200:
            print(f"❌ Errore Proxy: {res.text}")
            return "ERROR"
        
        cache_status = res.headers.get("X-Kektor-Cache", "MISS")
        print(f"   ⏱️ Tempo: {elapsed:.2f}s | Cache: {cache_status}")
        return cache_status
    except Exception as e:
        print(f"❌ Eccezione: {e}")
        return "ERROR"

def main():
    print("--- TEST AUTOMATIC CACHE MAINTENANCE ---")
    
    client = KektorDBClient(port=9091)
    # Pulizia iniziale
    try: client.delete_index(CACHE_INDEX)
    except: pass
    
    prompt = "Why is the sky blue?"
    
    # 1. Prima Richiesta (Crea l'indice e configura il Vacuum automaticamente)
    print("\n[Step 1] Prima richiesta (Popola Cache + Auto Config)")
    ask_proxy(prompt)
    
    # 2. Verifica immediata
    print("\n[Step 2] Richiesta Immediata (Atteso: HIT)")
    status = ask_proxy(prompt)
    if status != "HIT":
        print("GRAVE: Cache Hit fallito subito dopo insert.")
        sys.exit(1)

    # 3. Attesa Scadenza TTL
    print("\n[Step 3] Attendo 6 secondi (TTL è 5s)...")
    time.sleep(6)
    
    # 4. Trigger Scadenza (Lazy Delete)
    print("\n[Step 4] Richiesta Post-TTL (Atteso: MISS + Trigger Delete)")
    ask_proxy(prompt)
    
    # 5. Attesa Vacuum Automatico
    print("\n[Step 5] Attendo 5 secondi per il Vacuum AUTOMATICO...")
    time.sleep(5)
    
    # 6. Verifica
    try:
        info = client.get_index_info(CACHE_INDEX)
        print(f"\nStato Indice: {info.get('vector_count')} vettori attivi.")
        
        print("log server: '[Optimizer] Vacuum complete. Repaired...'")
        
    except Exception as e:
        print(f"Errore verifica: {e}")

if __name__ == "__main__":
    main()
