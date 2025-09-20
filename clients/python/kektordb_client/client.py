# File: clients/python/kektordb_client/client.py

import requests
from typing import List, Dict, Any, Union

# Definiamo delle eccezioni custom per una migliore gestione degli errori.
class KektorDBError(Exception):
    """Classe base per gli errori del client KektorDB."""
    pass

class APIError(KektorDBError):
    """Sollevata quando l'API restituisce un errore HTTP."""
    pass

class ConnectionError(KektorDBError):
    """Sollevata per problemi di connessione di rete."""
    pass


class KektorDBClient:
    """
    Un client Python per interagire con un server KektorDB tramite la sua API REST.
    """
    def __init__(self, host: str = "localhost", port: int = 9091):
        """
        Inizializza il client.
        
        :param host: L'host del server KektorDB.
        :param port: La porta del server HTTP di KektorDB.
        """
        self.base_url = f"http://{host}:{port}"

    def _request(self, method: str, endpoint: str, **kwargs) -> Dict[str, Any]:
        """Metodo helper interno per eseguire le richieste HTTP."""
        try:
            response = requests.request(method, f"{self.base_url}{endpoint}", **kwargs)
            response.raise_for_status()
            return response.json()
        except requests.exceptions.HTTPError as e: # <-- GESTITO PER PRIMO
            # Questo cattura errori specifici dell'API (4xx, 5xx).
            try:
                error_payload = e.response.json()
                msg = error_payload.get("error", str(e))
            except:
                msg = str(e)
            raise APIError(f"Errore API da KektorDB: {msg}") from e
        except requests.exceptions.RequestException as e: # <-- GESTITO PER SECONDO
            # Questo cattura tutti gli altri errori di rete.
            raise ConnectionError(f"Errore di connessione a KektorDB: {e}") from e

    # --- Metodi per Key-Value Store ---

    def set(self, key: str, value: Union[str, bytes]) -> None:
        """
        Imposta un valore per una chiave.
        
        :param key: La chiave da impostare.
        :param value: Il valore (stringa o bytes).
        """
        # Se il valore è in bytes, decodificalo in stringa per il JSON
        if isinstance(value, bytes):
            value_str = value.decode('utf-8')
        else:
            value_str = value
            
        payload = {"value": value_str}
        # Sostituisci la vecchia chiamata con questa:
        self._request("POST", f"/kv/{key}", json=payload)
        

    def get(self, key: str) -> str:
        """
        Recupera un valore data una chiave.
        
        :param key: La chiave da recuperare.
        :return: Il valore come stringa.
        """
        data = self._request("GET", f"/kv/{key}")
        return data.get("value")

    def delete(self, key: str) -> None:
        """
        Elimina una chiave.
        
        :param key: La chiave da eliminare.
        """
        self._request("DELETE", f"/kv/{key}")
        
    # --- Metodi per Indici Vettoriali ---

    def vcreate(
        self,
        index_name: str,
        metric: str = "euclidean",
        m: int = 0, # 0 significa che il server userà il suo default
        ef_construction: int = 0
    ) -> None:
        """
        Crea un nuovo indice vettoriale.

        :param index_name: Il nome dell'indice da creare.
        :param metric: La metrica di distanza ('euclidean' o 'cosine'). Default: 'euclidean'.
        :param m: Il numero massimo di connessioni per nodo. Default: server-side.
        :param ef_construction: La dimensione della lista candidati durante la costruzione. Default: server-side.
        """
        payload = {
            "index_name": index_name,
            "metric": metric,
            # Includiamo i parametri solo se specificati dall'utente
            # per mantenere il payload pulito.
        }
        if m > 0:
            payload["m"] = m
        if ef_construction > 0:
            payload["ef_construction"] = ef_construction
            
        self._request("POST", "/vector/create", json=payload)

    def vadd(
        self, 
        index_name: str, 
        item_id: str, 
        vector: List[float], 
        metadata: Dict[str, Any] = None
    ) -> None:
        """
        Aggiunge un vettore a un indice, con metadati opzionali.

        :param index_name: Il nome dell'indice.
        :param item_id: L'ID univoco dell'elemento.
        :param vector: L'embedding vettoriale.
        :param metadata: Un dizionario di metadati (es. {"tag": "gatto", "prezzo": 99.99}).
        """
        payload = {
            "index_name": index_name,
            "id": item_id,
            "vector": vector
        }
        if metadata:
            payload["metadata"] = metadata
        
        self._request("POST", "/vector/add", json=payload) 


    def vsearch(
        self, 
        index_name: str, 
        query_vector: List[float], 
        k: int, 
        filter_str: str = ""
    ) -> List[str]:
        """
        Esegue una ricerca di similarità in un indice, con un filtro opzionale.

        :param index_name: Il nome dell'indice in cui cercare.
        :param query_vector: Il vettore di query.
        :param k: Il numero di vicini da restituire.
        :param filter_str: Una stringa di filtro (es. "tag=gatto AND prezzo<100").
        :return: Una lista di ID degli elementi più simili.
        """
        payload = {
            "index_name": index_name,
            "k": k,
            "query_vector": query_vector
        }
        if filter_str:
            payload["filter"] = filter_str
            
        data = self._request("POST", "/vector/search", json=payload)
        return data.get("results", [])

    def vdelete(self, index_name: str, item_id: str) -> None:
        """
        Elimina un vettore da un indice (soft delete).

        :param index_name: Il nome dell'indice.
        :param item_id: L'ID dell'elemento da eliminare.
        """
        payload = {
            "index_name": index_name,
            "id": item_id
        }
        self._request("POST", "/vector/delete", json=payload)

     # --- Metodi di Amministrazione ---

    def aof_rewrite(self) -> None:
        """
        Richiede al server KektorDB di eseguire una compattazione del file AOF.
        Questa è un'operazione bloccante che può richiedere tempo.
        """
        self._request("POST", "/system/aof-rewrite")
