from setuptools import find_packages, setup

# Legge il README per la descrizione lunga
with open("README.md", "r", encoding="utf-8") as fh:
    long_description = fh.read()

setup(
    name="kektordb_client",
    version="0.5.1",
    author="Sanonone",
    author_email="fedeld023@gmail.com",
    description="An official Python client for KektorDB. AI memory system combining vector search with temporal knowledge graph. Built-in cognitive engine for agents. Supports memory decay, contradiction detection, and MCP integration.",
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
    python_requires=">=3.7",
    install_requires=[
        "requests",
        "numpy",
    ],
)
