from typing import Any, Iterable, List, Optional, Type, Dict
from .client import KektorDBClient

class KektorVectorStore:
    """
    A lightweight wrapper compatible with LangChain's VectorStore interface.
    It allows using KektorDB in standard AI pipelines without importing the full LangChain library.
    """

    def __init__(
        self,
        client: KektorDBClient,
        index_name: str,
        embedding_function: Any,  # Duck-typed: expects embed_documents & embed_query
    ):
        """
        Args:
            client: Authenticated KektorDBClient instance.
            index_name: Target index name.
            embedding_function: An object with `embed_documents` and `embed_query` methods.
        """
        self.client = client
        self.index_name = index_name
        self.embedding_function = embedding_function

    def add_texts(
        self,
        texts: Iterable[str],
        metadatas: Optional[List[dict]] = None,
        ids: Optional[List[str]] = None,
        **kwargs: Any,
    ) -> List[str]:
        """
        Embeds and adds texts to the vector store.
        
        Args:
            texts: Iterable of strings to add.
            metadatas: Optional list of metadata dicts associated with the texts.
            ids: Optional list of IDs. If None, IDs are generated.

        Returns:
            List of IDs of the added texts.
        """
        # 1. Generate Embeddings using the provided model
        embeddings = self.embedding_function.embed_documents(list(texts))
        
        # 2. Prepare Batch
        batch_data = []
        generated_ids = []

        for i, text in enumerate(texts):
            # Generate ID if missing
            if ids:
                doc_id = ids[i]
            else:
                # Simple deterministic ID based on content to avoid dupes
                doc_id = f"{self.index_name}_{hash(text)}"
            
            generated_ids.append(doc_id)

            # Metadata logic
            meta = metadatas[i] if metadatas else {}
            # Store the text content in metadata so we can retrieve it later
            meta["page_content"] = text 
            
            batch_data.append({
                "id": doc_id,
                "vector": embeddings[i],
                "metadata": meta
            })
            
        # 3. Upload to KektorDB
        # Using VAddBatch (writes to AOF) for safety. 
        # For huge initial loads, one might manually use vimport instead.
        self.client.vadd_batch(self.index_name, batch_data)
        
        return generated_ids

    def similarity_search(
        self, 
        query: str, 
        k: int = 4, 
        filter: Optional[str] = None, 
        **kwargs: Any
    ) -> List[Dict[str, Any]]:
        """
        Return documents most similar to the query.

        Returns:
            List of dictionaries containing 'page_content' and 'metadata'.
        """
        # 1. Embed Query
        query_vector = self.embedding_function.embed_query(query)
        
        # 2. Search IDs
        # Pass filter if present (e.g. "author='John'")
        results_ids = self.client.vsearch(
            self.index_name, 
            query_vector=query_vector, 
            k=k, 
            filter_str=filter or ""
        )
        
        if not results_ids:
            return []
            
        # 3. Retrieve Full Data (Hydrate)
        vectors_data = self.client.vget_many(self.index_name, results_ids)
        
        # 4. Format as standard Documents
        documents = []
        for v in vectors_data:
            meta = v.get("metadata", {})
            content = meta.pop("page_content", "") # Extract content
            
            documents.append({
                "page_content": content,
                "metadata": meta,
                "id": v["id"]
            })
            
        return documents

    @classmethod
    def from_texts(
        cls,
        texts: List[str],
        embedding: Any,
        client: KektorDBClient,
        index_name: str,
        metadatas: Optional[List[dict]] = None,
        **kwargs: Any,
    ) -> "KektorVectorStore":
        """Factory method to create a VectorStore and add texts in one go."""
        store = cls(client, index_name, embedding)
        store.add_texts(texts, metadatas=metadatas, **kwargs)
        return store
