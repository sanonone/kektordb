# File: clients/python/kektordb_client/client.py

import requests
import time
from typing import List, Dict, Any, Union

# --- Custom Exceptions ---

class KektorDBError(Exception):
    """Base exception class for KektorDB client errors."""
    pass

class APIError(KektorDBError):
    """Raised for status codes >= 400, indicating an API-level error."""
    pass

class ConnectionError(KektorDBError):
    """Raised for network-related errors (e.g., connection refused)."""
    pass

class Task:
    """Rappresenta un task asincrono sul server KektorDB."""
    def __init__(self, client: 'KektorDBClient', data: Dict[str, Any]):
        self._client = client
        self.id = data.get("id")
        self.status = data.get("status")
        self.progress_message = data.get("progress_message")
        self.error = data.get("error")

    def __repr__(self):
        return f"<Task id={self.id} status='{self.status}'>"

    def refresh(self):
        """Aggiorna lo stato del task interrogando il server."""
        data = self._client.get_task_status(self.id)
        self.status = data.get("status")
        self.progress_message = data.get("progress_message")
        self.error = data.get("error")
        return self

    def wait(self, interval: int = 10, timeout: int = 7200) -> 'Task':
        """
        Attende il completamento del task, controllando lo stato a intervalli regolari.

        Args:
            interval: Secondi da attendere tra un controllo e l'altro.
            timeout: Massimo tempo di attesa in secondi.

        Returns:
            L'oggetto Task aggiornato al suo stato finale.
            
        Raises:
            TimeoutError: Se il task non termina entro il timeout.
        """
        start_time = time.time()
        while time.time() - start_time < timeout:
            self.refresh()
            if self.status in ("completed", "failed"):
                return self
            print(f"  ... stato del task '{self.id}': {self.status} (controllo tra {interval}s)")
            time.sleep(interval)
        
        raise TimeoutError(f"Il task '{self.id}' non è terminato entro il timeout di {timeout} secondi.")



class KektorDBClient:
    """
    The official Python client for interacting with a KektorDB server via its REST API.
    """
    def __init__(self, host: str = "localhost", port: int = 9091, timeout: int = 30, api_key: str = None):
        """
        Initializes the KektorDB client.

        Args:
            host: The hostname or IP address of the KektorDB server.
            port: The port of the KektorDB REST API server.
            timeout: The request timeout in seconds.
            api_key: Optional secret token for authentication.
        """
        self.base_url = f"http://{host}:{port}"
        self.timeout = timeout
        self.api_key = api_key
        self._session = requests.Session()

    def _request(self, method: str, endpoint: str, **kwargs) -> Union[Dict[str, Any], List[Dict[str, Any]]]:
        """Internal helper method for making HTTP requests."""
        
        headers = kwargs.get("headers", {})

        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        kwargs["headers"] = headers

        try:
            response = self._session.request(
                method,
                f"{self.base_url}{endpoint}",
                timeout=self.timeout,
                **kwargs 
            )
            response.raise_for_status()
            
            if response.status_code == 204:
                return {}
            return response.json()
            
        except requests.exceptions.HTTPError as e:
            try:
                msg = e.response.json().get("error", str(e))
            except (ValueError, AttributeError):
                msg = str(e)
            raise APIError(f"KektorDB API Error: {msg}") from e
        except requests.exceptions.RequestException as e:
            raise ConnectionError(f"KektorDB Connection Error: {e}") from e

    # --- Key-Value Store Methods ---

    def set(self, key: str, value: Union[str, bytes]) -> None:
        """
        Sets a value for a given key in the KV store.

        Args:
            key: The key to set.
            value: The value to store (str or bytes).
        
        Raises:
            APIError: If the server returns an error.
            ConnectionError: If a network error occurs.
        """
        if isinstance(value, bytes):
            value_str = value.decode('utf-8')
        else:
            value_str = value
        payload = {"value": value_str}
        self._request("POST", f"/kv/{key}", json=payload)

    def get(self, key: str) -> str:
        """
        Retrieves a value for a given key from the KV store.

        Args:
            key: The key to retrieve.

        Returns:
            The value as a string.
        
        Raises:
            APIError: If the key is not found (404) or another error occurs.
            ConnectionError: If a network error occurs.
        """
        data = self._request("GET", f"/kv/{key}")
        return data.get("value")

    def delete(self, key: str) -> None:
        """
        Deletes a key from the KV store.

        Args:
            key: The key to delete.
        
        Raises:
            APIError: If the server returns an error.
            ConnectionError: If a network error occurs.
        """
        self._request("DELETE", f"/kv/{key}")

    # --- Vector Index Management Methods ---

    def vcreate(self, index_name: str, metric: str = "euclidean", precision: str = "float32", m: int = 0, ef_construction: int = 0, text_language: str = "", maintenance_config: Dict[str, Any] = None) -> None:
        """
        Creates a new vector index.

        Args:
            index_name: The name for the new index.
            metric: The distance metric ('euclidean' or 'cosine').
            precision: The data precision ('float32', 'float16', 'int8').
            m: HNSW M parameter (max connections). Server default if 0.
            ef_construction: HNSW efConstruction parameter. Server default if 0.
            text_language: Enables hybrid search ('english', 'italian').
            maintenance_config: Optional dict for background tasks (vacuum/refine settings).
        
        Raises:
            APIError: If the index already exists or parameters are invalid.
            ConnectionError: If a network error occurs.
        """
        payload = {"index_name": index_name, "metric": metric, "precision": precision}
        if m > 0: payload["m"] = m
        if ef_construction > 0: payload["ef_construction"] = ef_construction
        if text_language:
            payload["text_language"] = text_language

        if maintenance_config:
            payload["maintenance"] = maintenance_config

        self._request("POST", "/vector/actions/create", json=payload)

    def list_indexes(self) -> List[Dict[str, Any]]:
        """Lists all vector indexes and their configuration."""
        return self._request("GET", "/vector/indexes")

    def get_index_info(self, index_name: str) -> Dict[str, Any]:
        """Retrieves detailed information for a single index."""
        return self._request("GET", f"/vector/indexes/{index_name}")

    def delete_index(self, index_name: str) -> None:
        """Deletes an entire vector index and all its data."""
        self._request("DELETE", f"/vector/indexes/{index_name}")

    def vcompress(self, index_name: str, precision: str, wait: bool = True) -> Task:
        """
        AVVIA la compressione di un indice.
        Questa è un'operazione asincrona.

        Args:
            index_name: The name of the index to compress.
            precision: The target precision ('float16' or 'int8').
            wait: Se True, il metodo attenderà il completamento del task.
                  Se False, restituirà immediatamente l'oggetto Task.

        Returns:
            Un oggetto Task che rappresenta l'operazione di compressione.
        """
        payload = {"index_name": index_name, "precision": precision}
        task_data = self._request("POST", "/vector/actions/compress", json=payload)
        task = Task(self, task_data)
        
        if wait:
            return task.wait()
        return task 

    # --- Vector Data Methods ---
    
    def vadd(self, index_name: str, item_id: str, vector: List[float], metadata: Dict[str, Any] = None) -> None:
        """
        Adds a vector to an index.

        Args:
            index_name: The name of the index.
            item_id: A unique ID for the vector.
            vector: The vector embedding as a list of floats.
            metadata: An optional dictionary of metadata.
        
        Raises:
            APIError: If the index does not exist or the vector is invalid.
            ConnectionError: If a network error occurs.
        """
        payload = {"index_name": index_name, "id": item_id, "vector": vector}
        if metadata: payload["metadata"] = metadata
        self._request("POST", "/vector/actions/add", json=payload)

    def vadd_batch(self, index_name: str, vectors: List[Dict[str, Any]]) -> Dict[str, Any]:
        """
        Adds a batch of vectors to an index in a single request.

        Args:
            index_name: The name of the index.
            vectors: A list of dictionaries, where each dictionary must contain
                     'id' (str), 'vector' (List[float]), and optionally 
                     'metadata' (Dict[str, Any]).
                     Example:
                     [
                         {"id": "vec1", "vector": [0.1, 0.2], "metadata": {"tag": "A"}},
                         {"id": "vec2", "vector": [0.3, 0.4]}
                     ]

        Returns:
            A dictionary containing the status and number of vectors added.
        
        Raises:
            APIError: If the index does not exist or an error occurs during ingestion.
            ConnectionError: If a network error occurs.
        """
        payload = {
            "index_name": index_name,
            "vectors": vectors
        }
        return self._request("POST", "/vector/actions/add-batch", json=payload)

    def vimport(self, index_name: str, vectors: List[Dict[str, Any]], wait: bool = True) -> Task:
        """
        Importa massivamente vettori (Async).
        
        Args:
            index_name: Nome indice.
            vectors: Lista vettori.
            wait: Se True, blocca finché l'import non è finito.
        """
        payload = {
            "index_name": index_name,
            "vectors": vectors
        }
        # Non serve più il timeout gigante qui, la risposta è immediata
        response = self._request("POST", "/vector/actions/import", json=payload)
        
        task = Task(self, response)
        if wait:
            return task.wait(interval=1, timeout=600) # Timeout lungo per il polling
        return task

    def vdelete(self, index_name: str, item_id: str) -> None:
        """
        Deletes a vector from an index (soft delete).

        Args:
            index_name: The name of the index.
            item_id: The ID of the vector to delete.
        
        Raises:
            APIError: If the index does not exist.
            ConnectionError: If a network error occurs.
        """
        payload = {"index_name": index_name, "id": item_id}
        self._request("POST", "/vector/actions/delete_vector", json=payload)

    def vget(self, index_name: str, item_id: str) -> Dict[str, Any]:
        """Retrieves data (vector and metadata) for a single vector by its ID."""
        return self._request("GET", f"/vector/indexes/{index_name}/vectors/{item_id}")

    def vget_many(self, index_name: str, item_ids: List[str]) -> List[Dict[str, Any]]:
        """
        Retrieves data for multiple vectors from an index in a single batch request.

        Args:
            index_name: The name of the index.
            item_ids: A list of vector IDs to retrieve.

        Returns:
            A list of dictionaries, where each dictionary represents a found vector.
        
        Raises:
            APIError: If the index does not exist.
            ConnectionError: If a network error occurs.
        """
        payload = {"index_name": index_name, "ids": item_ids}
        return self._request("POST", "/vector/actions/get-vectors", json=payload)

    def vsearch(self, index_name: str, query_vector: List[float], k: int, filter_str: str = "", ef_search: int = 0, alpha: float = 0.5, include_relations: List[str] = None, hydrate_relations: bool = False) -> Union[List[str], List[Dict[str, Any]]]: 
        """
        Performs a nearest neighbor search in an index.

        Args:
            index_name: The name of the index to search in.
            query_vector: The query vector.
            k: The number of nearest neighbors to return.
            filter_str: An optional filter string (e.g., "tag=cat AND price<50").
            ef_search: An optional parameter to control the search breadth.
                       A higher value increases recall at the cost of speed.
                       If 0, the server's `efConstruction` default is used.
            alpha: The weight for hybrid search fusion (0 to 1).
                   1.0 = pure vector search, 0.0 = pure text search.
                   Only used when a CONTAINS filter is present. Default: 0.5.
            include_relations: List of relation types to fetch (e.g. ["parent", "next"]).
                               If provided, returns a list of dictionaries with relations.
                               If None (default), returns list of IDs strings.

        Returns:
            A list of item IDs of the nearest neighbors.
        
        Raises:
            APIError: If the index does not exist or the query is invalid.
            ConnectionError: If a network error occurs.
        """
        payload = {
            "index_name": index_name,
            "k": k,
            "query_vector": query_vector,
        }
        if filter_str:
            payload["filter"] = filter_str
        
        if ef_search > 0:
            payload["ef_search"] = ef_search

        if alpha != 0.5:
            payload["alpha"] = alpha

        if include_relations:
            payload["include_relations"] = include_relations

        if hydrate_relations:
            payload["hydrate_relations"] = True
            
        data = self._request("POST", "/vector/actions/search", json=payload)
        return data.get("results", [])

    # --- Graph / Relationship Methods ---

    def vlink(self, source_id: str, target_id: str, relation_type: str) -> None:
        """
        Creates a directed semantic link between two nodes.
        Example: vlink("chunk_5", "doc_manual_v1", "parent")
        """
        payload = {
            "source_id": source_id,
            "target_id": target_id,
            "relation_type": relation_type
        }
        self._request("POST", "/graph/actions/link", json=payload)

    def vunlink(self, source_id: str, target_id: str, relation_type: str) -> None:
        """
        Removes a directed semantic link.
        """
        payload = {
            "source_id": source_id,
            "target_id": target_id,
            "relation_type": relation_type
        }
        self._request("POST", "/graph/actions/unlink", json=payload)

    def vget_links(self, source_id: str, relation_type: str) -> List[str]:
        """
        Retrieves all target IDs linked from source_id with a specific relation type.
        """
        payload = {
            "source_id": source_id,
            "relation_type": relation_type
        }
        resp = self._request("POST", "/graph/actions/get-links", json=payload)
        return resp.get("targets", [])

    def vget_connections(self, index_name: str, source_id: str, relation_type: str) -> List[Dict[str, Any]]:
        """
        Retrieves full data (vector + metadata) of nodes linked to source_id.
        """
        payload = {
            "index_name": index_name,
            "source_id": source_id,
            "relation_type": relation_type
        }
        data = self._request("POST", "/graph/actions/get-connections", json=payload)
        return data.get("results", [])


    def vupdate_config(self, index_name: str, config: Dict[str, Any]) -> None:
        """
        Updates the background maintenance configuration for an index.
        
        Args:
            index_name: The index to update.
            config: Dictionary with configuration keys:
                    - vacuum_interval (str, e.g. "60s")
                    - delete_threshold (float, 0.0-1.0)
                    - refine_enabled (bool)
                    - refine_interval (str)
                    - refine_batch_size (int)
                    - refine_ef_construction (int)
        """
        self._request("POST", f"/vector/indexes/{index_name}/config", json=config)

    # --- Mantainance ---

    def vtrigger_maintenance(self, index_name: str, task_type: str, wait: bool = True) -> Task:
        """
        Trigger manuale maintenance (Async).
        """
        payload = {"type": task_type}
        response = self._request("POST", f"/vector/indexes/{index_name}/maintenance", json=payload)
        
        task = Task(self, response)
        if wait:
            return task.wait() # Timeout default
        return task
        

    # --- System Methods ---

    '''
    def aof_rewrite(self) -> None:
        """
        Requests the server to perform an AOF compaction.
        
        Raises:
            APIError: If the server fails to rewrite the AOF.
            ConnectionError: If a network error occurs.
        """
        self._request("POST", "/system/aof-rewrite")


    '''
    def aof_rewrite(self, wait: bool = True) -> Task:
        """
        AVVIA la compattazione dell'AOF.
        Operazione asincrona.
        """
        task_data = self._request("POST", "/system/aof-rewrite") # Assumendo che lo modificheremo
        task = Task(self, task_data)

        if wait:
            return task.wait()
        return task

    def save(self) -> None:
        """
        Requests the server to perform a database snapshot (SAVE).
        """
        self._request("POST", "/system/save")
    


    def get_task_status(self, task_id: str) -> Dict[str, Any]:
        """Recupera lo stato di un task a lunga esecuzione."""
        return self._request("GET", f"/system/tasks/{task_id}")
