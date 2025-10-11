# File: clients/python/kektordb_client/client.py

import requests
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


class KektorDBClient:
    """
    The official Python client for interacting with a KektorDB server via its REST API.
    """
    def __init__(self, host: str = "localhost", port: int = 9091, timeout: int = 30):
        """
        Initializes the KektorDB client.

        Args:
            host: The hostname or IP address of the KektorDB server.
            port: The port of the KektorDB REST API server.
            timeout: The request timeout in seconds.
        """
        self.base_url = f"http://{host}:{port}"
        self.timeout = timeout
        self._session = requests.Session()

    def _request(self, method: str, endpoint: str, **kwargs) -> Union[Dict[str, Any], List[Dict[str, Any]]]:
        """Internal helper method for making HTTP requests."""
        try:
            response = self._session.request(
                method,
                f"{self.base_url}{endpoint}",
                timeout=self.timeout,
                **kwargs
            )
            response.raise_for_status()
            # Handle 204 No Content for successful DELETE operations
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

    def vcreate(self, index_name: str, metric: str = "euclidean", precision: str = "float32", m: int = 0, ef_construction: int = 0) -> None:
        """
        Creates a new vector index.

        Args:
            index_name: The name for the new index.
            metric: The distance metric ('euclidean' or 'cosine').
            precision: The data precision ('float32', 'float16', 'int8').
            m: HNSW M parameter (max connections). Server default if 0.
            ef_construction: HNSW efConstruction parameter. Server default if 0.
        
        Raises:
            APIError: If the index already exists or parameters are invalid.
            ConnectionError: If a network error occurs.
        """
        payload = {"index_name": index_name, "metric": metric, "precision": precision}
        if m > 0: payload["m"] = m
        if ef_construction > 0: payload["ef_construction"] = ef_construction
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

    def vcompress(self, index_name: str, precision: str) -> None:
        """
        Compresses an existing float32 index to a lower precision format.
        NOTE: This can be a long-running operation.
        """
        payload = {"index_name": index_name, "precision": precision}
        
        # --- CORREZIONE: Usa un timeout molto più lungo per questa operazione ---
        # Salva il timeout originale
        original_timeout = self.timeout
        # Imposta un timeout molto lungo (es. 1 ora)
        self.timeout = 3600 
        
        try:
            self._request("POST", "/vector/actions/compress", json=payload)
        finally:
            # Ripristina sempre il timeout originale, anche in caso di errore
            self.timeout = original_timeout 
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

    def vsearch(self, index_name: str, query_vector: List[float], k: int, filter_str: str = "", ef_search: int = 0) -> List[str]:
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

        Returns:
            A list of item IDs of the nearest neighbors.
        
        Raises:
            APIError: If the index does not exist or the query is invalid.
            ConnectionError: If a network error occurs.
        """
        payload = {
            "index_name": index_name,
            "k": k,
            "query_vector": query_vector
        }
        if filter_str:
            payload["filter"] = filter_str
        
        if ef_search > 0:
            payload["ef_search"] = ef_search
            
        data = self._request("POST", "/vector/actions/search", json=payload)
        return data.get("results", [])
        

    # --- System Methods ---

    def aof_rewrite(self) -> None:
        """
        Requests the server to perform an AOF compaction.
        
        Raises:
            APIError: If the server fails to rewrite the AOF.
            ConnectionError: If a network error occurs.
        """
        self._request("POST", "/system/aof-rewrite")
