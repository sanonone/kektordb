/**
 * Contract test runner for KektorDB API.
 * Reads testdata/api_contracts.json and validates each endpoint.
 *
 * Usage:
 *   npx vitest run tests/contracts.test.ts
 */

import { describe, it, expect, beforeAll } from "vitest";
import { readFileSync } from "fs";
import { resolve } from "path";

const BASE_URL = "http://localhost:9091";
const CONTRACTS_PATH = resolve(__dirname, "../../../testdata/api_contracts.json");

interface ContractTest {
  name: string;
  method: string;
  path: string;
  request?: Record<string, any>;
  expected_status: number;
  expected_response_fields?: string[];
  depends_on?: string[];
  cleanup?: { method: string; path: string };
}

interface Contracts {
  version: string;
  tests: ContractTest[];
}

function loadContracts(): Contracts {
  const data = readFileSync(CONTRACTS_PATH, "utf-8");
  return JSON.parse(data);
}

describe("API Contracts", () => {
  let contracts: Contracts;
  const results: Record<string, boolean> = {};

  beforeAll(() => {
    contracts = loadContracts();
  });

  for (const tc of loadContracts().tests) {
    it(tc.name, async () => {
      // Check dependencies
      for (const dep of tc.depends_on ?? []) {
        if (!results[dep]) {
          return; // skip (vitest doesn't have t.skip in it blocks easily)
        }
      }

      const url = `${BASE_URL}${tc.path}`;
      const init: RequestInit = {
        method: tc.method,
        headers: { "Content-Type": "application/json" },
      };

      if (tc.request && ["POST", "PUT", "PATCH"].includes(tc.method)) {
        init.body = JSON.stringify(tc.request);
      }

      try {
        const resp = await fetch(url, init);

        // Validate status
        expect(resp.status).toBe(tc.expected_status);

        // Validate response fields
        if (resp.status === 200 && tc.expected_response_fields) {
          const body = await resp.json();
          for (const field of tc.expected_response_fields) {
            expect(body).toHaveProperty(field);
          }
        }

        results[tc.name] = true;

        // Cleanup
        if (tc.cleanup) {
          await fetch(`${BASE_URL}${tc.cleanup.path}`, {
            method: tc.cleanup.method,
          });
        }
      } catch (e: any) {
        if (e.name === "AssertionError") throw e;
        // Connection error — test fails
        throw new Error(`Request to ${url} failed: ${e.message}`);
      }
    });
  }
});
