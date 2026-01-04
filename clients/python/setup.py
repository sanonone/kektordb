from setuptools import setup, find_packages

# Legge il README per la descrizione lunga
with open("README.md", "r", encoding="utf-8") as fh:
    long_description = fh.read()

setup(
    name="kektordb_client",
    version="0.4.0", 
    author="Sanonone",
    author_email="fedeld023@gmail.com",
    description="An official Python client for KektorDB. An in-memory Vector Database & AI Gateway written in Go. Supports HNSW, Hybrid Search (BM25), GraphRAG context, a built-in RAG Pipeline, and can be embedded directly into your apps. ",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/sanonone/kektordb", 
    packages=find_packages(),
    classifiers=[
        "Programming Language :: Python :: 3",
        "License :: OSI Approved :: Apache Software License", 
        "Operating System :: OS Independent",
        "Topic :: Database",
        "Topic :: Scientific/Engineering :: Artificial Intelligence",
    ],
    python_requires='>=3.7', 
    install_requires=[
        "requests",
        "numpy", 
    ],
)
