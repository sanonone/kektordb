from .client import KektorDBClient, KektorDBError, APIError, ConnectionError

try:
    from .langchain import KektorVectorStore

    __all__ = [
        "KektorDBClient",
        "KektorDBError",
        "APIError",
        "ConnectionError",
        "KektorVectorStore",
    ]
except ImportError:
    # LangChain non è installato, escludiamo KektorVectorStore
    __all__ = ["KektorDBClient", "KektorDBError", "APIError", "ConnectionError"]
