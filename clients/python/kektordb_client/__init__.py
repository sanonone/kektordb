from .client import KektorDBClient, KektorDBError, APIError, ConnectionError
from .cognitive import CognitiveSession, CognitiveOrchestrator, quick_session

try:
    from .langchain import KektorVectorStore

    __all__ = [
        "KektorDBClient",
        "KektorDBError",
        "APIError",
        "ConnectionError",
        "KektorVectorStore",
        "CognitiveSession",
        "CognitiveOrchestrator",
        "quick_session",
    ]
except ImportError:
    # LangChain non è installato, escludiamo KektorVectorStore
    __all__ = [
        "KektorDBClient",
        "KektorDBError",
        "APIError",
        "ConnectionError",
        "CognitiveSession",
        "CognitiveOrchestrator",
        "quick_session",
    ]
