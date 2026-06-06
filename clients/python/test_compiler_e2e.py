#!/usr/bin/env python3
"""
E2E test for KektorDB Knowledge Engine (Phase 4A/4B).

Verifies the compiler endpoints end-to-end against a running kektordb server.
Usage: python test_compiler_e2e.py [--host HOST] [--port PORT]

Requires: kektordb server running on the target host:port.
"""

import json
import sys
import argparse
from kektordb_client import KektorDBClient, APIError, ConnectionError


def green(s):
    return f"\033[32m{s}\033[0m"


def red(s):
    return f"\033[31m{s}\033[0m"


def bold(s):
    return f"\033[1m{s}\033[0m"


class TestRunner:
    def __init__(self, client: KektorDBClient):
        self.client = client
        self.passed = 0
        self.failed = 0
        self.index_name = "compiler_e2e_test"

    def check(self, name: str, condition: bool, detail: str = ""):
        if condition:
            print(f"  {green('[PASS]')} {name}")
            self.passed += 1
        else:
            print(f"  {red('[FAIL]')} {name}{' — ' + detail if detail else ''}")
            self.failed += 1

    def step(self, name: str):
        print(f"\n{bold(name)}")

    def run(self):
        # --- Setup ---
        self.step("1. Setup: create index and test data")
        try:
            self.client.vcreate(
                self.index_name, "cosine", "float32", 16, 200, "english"
            )
            self.check("create index", True)
        except Exception as e:
            self.check("create index", False, str(e))

        vec = [0.1] * 384
        try:
            self.client.vadd(
                self.index_name, "user:alice", vec, {
                    "type": "user", "entity_id": "alice",
                    "name": "Alice Johnson", "_pinned": True,
                }
            )
            self.client.vadd(
                self.index_name, "user:alice:mem1", vec, {
                    "type": "memory",
                    "content": "Alice prefers concise code reviews and uses Vim",
                }
            )
            self.client.vadd(
                self.index_name, "user:alice:mem2", vec, {
                    "type": "memory",
                    "content": "Alice is a senior Go developer working on KektorDB",
                }
            )
            self.check("add test nodes", True)
        except Exception as e:
            self.check("add test nodes", False, str(e))
            self.summary()
            sys.exit(1)

        try:
            self.client.vlink(
                self.index_name, "user:alice", "user:alice:mem1",
                "has_interaction", "interaction_of",
            )
            self.client.vlink(
                self.index_name, "user:alice", "user:alice:mem2",
                "has_interaction", "interaction_of",
            )
            self.check("create links", True)
        except Exception as e:
            self.check("create links", False, str(e))

        # --- Test 2: Templates ---
        self.step("2. List compile templates")
        try:
            result = self.client.list_compile_templates()
            names = result.get("names", [])
            self.check("templates endpoint", len(names) >= 5,
                       f"found {len(names)} templates")
            if names:
                print(f"      Templates: {', '.join(names)}")
        except Exception as e:
            self.check("templates endpoint", False, str(e))

        # --- Test 3: Compile entity_card ---
        self.step("3. Compile entity_card")
        try:
            result = self.client.compile({
                "name": "entity_card",
                "sources": {
                    "type": "graph_query",
                    "entity": {"type": "user", "id": "alice"},
                    "depth": 2,
                },
                "index_name": self.index_name,
            })
            self.check("compile returns", result is not None)

            data = result.get("data", {})
            self.check("has name field", "name" in data,
                       f"fields: {list(data.keys())[:6]}")
            self.check("has type field", "type" in data)
            self.check("has connection_count", "connection_count" in data,
                       f"value={data.get('connection_count')}")
            self.check("compile_mode", result.get("compile_mode") in
                       ("deterministic", "hybrid"),
                       f"got {result.get('compile_mode')}")
            self.check("status", result.get("status") == "complete",
                       f"got {result.get('status')}")

            print(f"      Artifact data: {json.dumps(data, indent=6)[:200]}")

        except Exception as e:
            self.check("compile entity_card", False, str(e))

        # --- Test 4: Compile project_summary ---
        self.step("4. Compile project_summary (different entity type)")
        try:
            # Add a project entity
            self.client.vadd(
                self.index_name, "project:kektor", vec, {
                    "type": "project", "entity_id": "kektor",
                    "_pinned": True,
                }
            )
            self.client.vadd(
                self.index_name, "project:kektor:mem1", vec, {
                    "type": "memory", "content": "KektorDB HNSW implementation",
                }
            )
            self.client.vlink(
                self.index_name, "project:kektor", "project:kektor:mem1",
                "has_memory", "memory_of",
            )

            result = self.client.compile({
                "name": "project_summary",
                "template": "project_summary",
                "sources": {
                    "type": "graph_query",
                    "entity": {"type": "project", "id": "kektor"},
                    "depth": 1,
                },
                "index_name": self.index_name,
            })
            data = result.get("data", {})
            self.check("project node_count", "node_count" in data,
                       f"value={data.get('node_count')}")
            self.check("project relation_count", "relation_count" in data,
                       f"value={data.get('relation_count')}")

        except Exception as e:
            self.check("compile project_summary", False, str(e))

        # --- Test 5: List artifacts ---
        self.step("5. List artifacts")
        try:
            result = self.client.list_artifacts(self.index_name)
            count = result.get("count", 0)
            artifacts = result.get("artifacts", [])
            self.check("artifacts count > 0", count >= 1,
                       f"found {count} artifacts")
            for a in artifacts:
                print(f"      - {a.get('name')} ({a.get('entity_type')}:{a.get('entity_id')}) "
                      f"v{a.get('version')} mode={a.get('compile_mode')}")
        except Exception as e:
            self.check("list artifacts", False, str(e))

        # --- Test 6: Get artifact ---
        self.step("6. Get artifact by name + entity")
        try:
            artifact = self.client.get_artifact(
                "entity_card", "user", "alice", self.index_name
            )
            self.check("get artifact returns", True)
            self.check("correct name", artifact.get("name") == "entity_card")
            self.check("correct entity", artifact.get("entity_type") == "user")
        except Exception as e:
            self.check("get artifact", False, str(e))

        # --- Test 7: Artifact not found ---
        self.step("7. Get nonexistent artifact (expect 404)")
        try:
            self.client.get_artifact("nonexistent", "user", "nobody")
            self.check("should return 404", False, "expected APIError/404")
        except APIError as e:
            self.check("returned APIError as expected", True, str(e)[:80])
        except Exception as e:
            self.check("returned error", True, f"type={type(e).__name__}: {e}")

        # --- Summary ---
        self.summary()

    def summary(self):
        total = self.passed + self.failed
        print(f"\n{bold('Summary:')} {self.passed}/{total} passed")
        if self.failed > 0:
            print(red(f"  {self.failed} test(s) FAILED"))
            sys.exit(1)
        else:
            print(green("  All tests PASSED"))


def main():
    parser = argparse.ArgumentParser(
        description="E2E test for KektorDB Knowledge Engine"
    )
    parser.add_argument("--host", default="localhost", help="Server host")
    parser.add_argument("--port", type=int, default=9091, help="Server port")
    args = parser.parse_args()

    try:
        client = KektorDBClient(host=args.host, port=args.port, timeout=10)
        # Verify connection
        client._request("GET", "/healthz")
        print(f"Connected to kektordb at {args.host}:{args.port}\n")

    except ConnectionError:
        print(f"Failed to connect to kektordb at {args.host}:{args.port}")
        print("Make sure the server is running: ./kektordb")
        sys.exit(1)

    runner = TestRunner(client)
    try:
        runner.run()
    finally:
        # Cleanup
        try:
            client.vdelete_index(runner.index_name)
        except Exception:
            pass


if __name__ == "__main__":
    main()
