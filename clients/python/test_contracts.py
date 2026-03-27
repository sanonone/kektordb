"""
Contract test runner for KektorDB API.
Reads testdata/api_contracts.json and validates each endpoint.

Usage:
    # Start KektorDB server first, then:
    python clients/python/test_contracts.py

    Or with pytest:
    pytest clients/python/test_contracts.py -v
"""

import json
import os
import sys
import time
import requests


BASE_URL = "http://localhost:9091"
CONTRACTS_PATH = os.path.join(
    os.path.dirname(__file__), "..", "..", "testdata", "api_contracts.json"
)


def load_contracts():
    with open(CONTRACTS_PATH) as f:
        return json.load(f)


def test_contracts():
    contracts = load_contracts()
    results = {}
    passed = 0
    failed = 0
    skipped = 0

    for tc in contracts["tests"]:
        name = tc["name"]

        # Check dependencies
        deps_ok = True
        for dep in tc.get("depends_on", []):
            if not results.get(dep):
                print(f"  SKIP {name} (dependency '{dep}' not passed)")
                deps_ok = False
                skipped += 1
                break

        if not deps_ok:
            continue

        # Build request
        method = tc["method"]
        path = tc["path"]
        url = f"{BASE_URL}{path}"
        headers = {"Content-Type": "application/json"}

        try:
            if method == "GET":
                resp = requests.get(url, headers=headers, timeout=10)
            elif method == "POST":
                body = tc.get("request")
                resp = requests.post(url, headers=headers, json=body, timeout=10)
            elif method == "PUT":
                body = tc.get("request")
                resp = requests.put(url, headers=headers, json=body, timeout=10)
            elif method == "DELETE":
                resp = requests.delete(url, headers=headers, timeout=10)
            else:
                print(f"  SKIP {name} (unsupported method {method})")
                skipped += 1
                continue

            # Validate status
            expected = tc["expected_status"]
            if resp.status_code == expected:
                # Validate response fields
                all_fields_ok = True
                if resp.status_code == 200 and tc.get("expected_response_fields"):
                    try:
                        resp_json = resp.json()
                        for field in tc["expected_response_fields"]:
                            if field not in resp_json:
                                print(f"  FAIL {name} (missing field '{field}')")
                                all_fields_ok = False
                                break
                    except json.JSONDecodeError:
                        pass

                if all_fields_ok:
                    print(f"  PASS {name}")
                    results[name] = True
                    passed += 1
                else:
                    results[name] = False
                    failed += 1
            else:
                print(f"  FAIL {name} (expected {expected}, got {resp.status_code})")
                results[name] = False
                failed += 1

            # Cleanup
            if tc.get("cleanup"):
                c = tc["cleanup"]
                requests.request(c["method"], f"{BASE_URL}{c['path']}", timeout=10)

        except requests.ConnectionError:
            print(f"  ERROR {name} (cannot connect to {BASE_URL})")
            results[name] = False
            failed += 1

    print(f"\nResults: {passed} passed, {failed} failed, {skipped} skipped")
    return failed == 0


if __name__ == "__main__":
    success = test_contracts()
    sys.exit(0 if success else 1)
