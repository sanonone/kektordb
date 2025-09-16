from setuptools import setup, find_packages

setup(
    name="kektordb_client",
    version="0.1.0",
    author="Il Tuo Nome",
    author_email="tua@email.com",
    description="Un client Python per il database vettoriale KektorDB",
    long_description=open('README.md').read(),
    long_description_content_type="text/markdown",
    url="https://github.com/tuo_username/kektordb", # URL al tuo repository
    packages=find_packages(where=".", include=("kektordb_client",)),
    classifiers=[
        "Programming Language :: Python :: 3",
        "License :: OSI Approved :: MIT License", # Scegli una licenza
        "Operating System :: OS Independent",
    ],
    python_requires='>=3.6',
    install_requires=[
        "requests",
    ],
)
