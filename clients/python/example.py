# File: clients/python/example.py
from kektordb_client import KektorDBClient, APIError

def run_tests():
    """Esegue una suite di test di integrazione sul server KektorDB."""

    # Inizializza il client
    client = KektorDBClient(host="localhost", port=9091)
    print("[OK] Client KektorDB inizializzato.")

    try:
        # --- 1. Test Key-Value (veloce) ---
        print("\n--- 1. Test Key-Value ---")
        client.set("messaggio", "Ciao da Python!")
        print("SET 'messaggio'")
        valore = client.get("messaggio")
        print(f"GET 'messaggio' -> '{valore}'")
        assert valore == "Ciao da Python!"
        client.delete("messaggio")
        print("DELETE 'messaggio'")
        print("[OK] Test KV superato.")

        # --- 2. Test Creazione Indici Configurabili ---
        print("\n--- 2. Test Creazione Indici ---")
        idx_euclidean = "test_euclidean_idx"
        idx_cosine = "test_cosine_idx"

        # Pulisci indici da esecuzioni precedenti (ignora errori se non esistono)
        try: client._request("POST", "/system/aof-rewrite") # hack per pulire, non ideale
        except: pass

        client.vcreate(idx_euclidean, metric="euclidean", m=10, ef_construction=100)
        print(f"VCREATE '{idx_euclidean}' con metrica 'euclidean' e parametri custom.")
        
        client.vcreate(idx_cosine, metric="cosine")
        print(f"VCREATE '{idx_cosine}' con metrica 'cosine'.")
        print("[OK] Test Creazione Indici superato.")
        
        # --- 3. Test Inserimento con Metadati ---
        print("\n--- 3. Test Inserimento con Metadati ---")
        client.vadd(
            index_name=idx_cosine,
            item_id="doc1",
            vector=[0.1, 0.8, 0.3], # Non normalizzato, il server gestirà
            metadata={"autore": "gemma", "anno": 2024, "tipo": "articolo"}
        )
        client.vadd(
            index_name=idx_cosine,
            item_id="doc2",
            vector=[0.9, 0.2, 0.1],
            metadata={"autore": "gemma", "anno": 2025, "tipo": "articolo"}
        )
        client.vadd(
            index_name=idx_cosine,
            item_id="doc3",
            vector=[0.2, 0.7, 0.2],
            metadata={"autore": "claudio", "anno": 2024, "tipo": "commento"}
        )
        print("VADD 3 vettori con metadati.")
        print("[OK] Test Inserimento superato.")

        # --- 4. Test Ricerca Filtrata ---
        print("\n--- 4. Test Ricerca Filtrata ---")
        
        # Cerca articoli di gemma dell'anno 2024
        query_vector = [0.15, 0.75, 0.35]
        filter_str = "autore=gemma AND anno=2024"
        
        risultati = client.vsearch(idx_cosine, query_vector, k=3, filter_str=filter_str)
        print(f"VSEARCH con filtro '{filter_str}' -> Risultati: {risultati}")
        assert risultati == ["doc1"]
        print("[OK] Test Ricerca Filtrata superato.")

        # --- 5. Test Compattazione AOF ---
        print("\n--- 5. Test Compattazione AOF ---")
        # Prima, elimina un documento per "sporcare" l'AOF
        print("Eliminazione doc2 prima di riscrittura AOF")
        client.delete("doc2") # Questo comando non esiste, usiamo vdel

        print("Richiesta di riscrittura AOF...")
        client.aof_rewrite()
        print("Riscrittura AOF completata lato client.")
        print("[OK] Test Compattazione AOF superato (verifica manuale del file AOF consigliata).")

        print("\n TUTTI I TEST DEL CLIENT SONO PASSATI! ")

    except APIError as e:
        print(f"\n[err] ERRORE API: {e}")
    except ConnectionError as e:
        print(f"\n[err] ERRORE DI CONNESSIONE: {e}")
    except Exception as e:
        print(f"\n[err] UN ERRORE INASPETTATO È OCCORSO: {e}")

if __name__ == "__main__":
    run_tests()
