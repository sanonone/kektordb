"""
Pytest fixtures for KektorDB integration tests.

These tests assume a KektorDB server is running on localhost:9091,
or they will start one automatically.
"""

import os
import subprocess
import time
import socket
from typing import Generator

import pytest

from kektordb_client import KektorDBClient


def is_port_open(host: str, port: int) -> bool:
    """Check if a port is open."""
    try:
        with socket.create_connection((host, port), timeout=1):
            return True
    except (socket.timeout, ConnectionRefusedError):
        return False


@pytest.fixture(scope="session")
def kektor_server() -> Generator[str, None, None]:
    """
    Ensures a KektorDB server is available for testing.

    If a server is already running on localhost:9091, uses that.
    Otherwise, attempts to start one automatically.
    """
    host = "localhost"
    port = 9091
    base_url = f"http://{host}:{port}"

    # Check if server already running
    if is_port_open(host, port):
        print(f"\nUsing existing KektorDB server at {base_url}")
        yield base_url
        return

    # Try to start server automatically
    # Look for binary in common locations
    possible_paths = [
        os.path.join(os.path.dirname(__file__), "../../../kektordb"),
        os.path.join(os.path.dirname(__file__), "../../kektordb"),
        "/usr/local/bin/kektordb",
        "./kektordb",
    ]

    binary = None
    for path in possible_paths:
        if os.path.isfile(path) and os.access(path, os.X_OK):
            binary = path
            break

    if binary is None:
        pytest.skip(
            "No KektorDB server running on localhost:9091 and "
            "no kektordb binary found. Please start a server manually: "
            "./kektordb --http localhost:9091"
        )

    # Start server
    print(f"\nStarting KektorDB server from {binary}...")
    proc = subprocess.Popen(
        [binary, "--http", f"{host}:{port}"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait for server to start (max 30 seconds)
    for _ in range(30):
        if is_port_open(host, port):
            print(f"Server ready at {base_url}")
            break
        time.sleep(1)
    else:
        proc.terminate()
        proc.wait()
        pytest.skip(f"Server failed to start within 30 seconds")

    yield base_url

    # Cleanup
    print("\nShutting down KektorDB server...")
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()


@pytest.fixture
def client(kektor_server: str) -> KektorDBClient:
    """Create a fresh client for each test."""
    from urllib.parse import urlparse

    parsed = urlparse(kektor_server)
    return KektorDBClient(host=parsed.hostname, port=parsed.port)


@pytest.fixture
def test_index_name() -> str:
    """Generate a unique test index name."""
    import uuid

    return f"test_idx_{uuid.uuid4().hex[:8]}"
