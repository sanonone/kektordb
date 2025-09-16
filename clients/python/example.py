# File: clients/python/example.py
from kektordb_client import KektorDBClient

# Inizializza il client
client = KektorDBClient()
print("Client KektorDB inizializzato.")

try:
    # --- Test KV ---
    print("\n--- Test Key-Value ---")
    client.set("messaggio", "Ciao da Python!")
    print("SET 'messaggio' a 'Ciao da Python!'")
    
    valore = client.get("messaggio")
    print(f"GET 'messaggio' -> '{valore}'")
    assert valore == "Ciao da Python!"
    
    client.delete("messaggio")
    print("DELETE 'messaggio'")
    
    # Questo dovrebbe fallire con un'eccezione
    try:
        client.get("messaggio")
    except Exception as e:
        print(f"GET 'messaggio' (dopo delete) ha fallito come previsto: {e}")

    # --- Test Vettoriale ---
    print("\n--- Test Vettoriale ---")
    index_name = "test_python_sdk"
    client.vcreate(index_name)
    print(f"VCREATE '{index_name}'")

    client.vadd(index_name, "vec1", [0.1, 0.2, 0.3])
    client.vadd(index_name, "vec2", [0.9, 0.8, 0.7])
    print("VADD 'vec1' e 'vec2'")

    risultati = client.vsearch(index_name, [0.15, 0.25, 0.35], k=1)
    print(f"VSEARCH -> Risultati: {risultati}")
    assert risultati == ["vec1"]

    print("\n✅ Tutti i test del client sono passati!")

except Exception as e:
    print(f"\n❌ Un errore è occorso: {e}")
