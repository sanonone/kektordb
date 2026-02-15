import time
from typing import Any, Dict, Iterable, List, Optional, Type

from langchain.schema import (
    AIMessage,
    BaseChatMessageHistory,
    BaseMessage,
    HumanMessage,
)

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

            batch_data.append({"id": doc_id, "vector": embeddings[i], "metadata": meta})

        # 3. Upload to KektorDB
        # Using VAddBatch (writes to AOF) for safety.
        # For huge initial loads, one might manually use vimport instead.
        self.client.vadd_batch(self.index_name, batch_data)

        return generated_ids

    def similarity_search(
        self, query: str, k: int = 4, filter: Optional[str] = None, **kwargs: Any
    ) -> List[Dict[str, Any]]:
        """
        Return documents most similar to the query.

        Returns:
            List of dictionaries containing 'page_content' and 'metadata'.
        """

        # Extract graph_filter from kwargs
        graph_filter = kwargs.get("graph_filter", None)

        # 1. Embed Query
        query_vector = self.embedding_function.embed_query(query)

        # 2. Search IDs
        # Pass filter if present (e.g. "author='John'")
        results_ids = self.client.vsearch(
            self.index_name,
            query_vector=query_vector,
            k=k,
            filter_str=filter or "",
            graph_filter=graph_filter,
        )

        if not results_ids:
            return []

        # 3. Retrieve Full Data (Hydrate)
        vectors_data = self.client.vget_many(self.index_name, results_ids)

        # 4. Format as standard Documents
        documents = []
        for v in vectors_data:
            meta = v.get("metadata", {})
            content = meta.pop("page_content", "")  # Extract content

            documents.append({"page_content": content, "metadata": meta, "id": v["id"]})

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

    class KektorChatMessageHistory(BaseChatMessageHistory):
        """
        Stores chat history in KektorDB using vectors + graph linking.
        It automatically creates a graph node for the session.
        """

        def __init__(
            self,
            client: KektorDBClient,
            index_name: str,
            session_id: str,
            embedding_function: Any = None,
        ):
            self.client = client
            self.index_name = index_name
            self.session_id = session_id
            self.embedding_function = embedding_function
            # Ensure the index exists (user responsibility) or create it with proper config

        def add_message(self, message: BaseMessage):
            # Mappa LangChain message -> KektorDB
            role = "user" if isinstance(message, HumanMessage) else "ai"

            # Qui sfruttiamo l'Auto-Linking che abbiamo creato in Go!
            # Passando session_id nei metadati, il DB crea il link al nodo sessione.
            metadata = {
                "session_id": self.session_id,
                "role": role,
                "timestamp": time.time(),
            }

            # Generate Embedding if function is provided
            vector = [0.0] * 1536  # Default fallback
            if self.embedding_function:
                # Support both embed_query (LangChain standard) and direct call
                if hasattr(self.embedding_function, "embed_query"):
                    vector = self.embedding_function.embed_query(message.content)
                elif callable(self.embedding_function):
                    vector = self.embedding_function(message.content)

            # Generate unique message ID
            import uuid

            msg_id = f"msg_{self.session_id}_{uuid.uuid4().hex[:8]}"

            self.client.vadd(
                self.index_name,
                msg_id,
                vector,
                {**metadata, "content": message.content},
            )

        @property
        def messages(self) -> List[BaseMessage]:
            # Usiamo il Grafo per recuperare i messaggi della sessione!
            # Invece di search, usiamo vget_connections o graph filter

            # 1. Recupera ID collegati al nodo sessione (Reverse Index!)
            # Assumendo che Auto-Link abbia creato link: Msg -> belongs_to -> SessionID
            # Quindi SessionID ha incoming links.
            msg_ids = self.client.get_incoming(self.session_id, "belongs_to_session")

            if not msg_ids:
                return []

            data = self.client.vget_many(self.index_name, msg_ids)

            # Ordina per timestamp
            data.sort(key=lambda x: x["metadata"].get("timestamp", 0))

            messages = []
            for item in data:
                content = item["metadata"].get("content", "")
                role = item["metadata"].get("role", "user")
                if role == "user":
                    messages.append(HumanMessage(content=content))
                else:
                    messages.append(AIMessage(content=content))

            return messages

        def clear(self):
            """Clears all messages for this session."""
            # 1. Find all messages linked to this session
            # Assumes Auto-Linking created "belongs_to_session" links
            try:
                msg_ids = self.client.get_incoming(
                    self.session_id, "belongs_to_session"
                )
                for mid in msg_ids:
                    # Soft delete vectors
                    self.client.vdelete(self.index_name, mid)
            except Exception as e:
                print(f"Error clearing session {self.session_id}: {e}")
