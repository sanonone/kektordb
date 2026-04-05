# Module: pkg/auth

## Purpose

Role-Based Access Control (RBAC) and API key management for KektorDB. Handles the full lifecycle of API keys: generation, verification, revocation, and permission checking. All state is persisted in the engine's KV store under the `_sys_auth::` namespace prefix, never in memory.

## Key Types & Critical Paths

**Critical structs:**
- `APIKeyPolicy` -- `{ID, Description, Role string, Namespaces []string, CreatedAt int64}`. Serialized to JSON and stored in KV store under `_sys_auth::<hashed_token>`.
- `AuthService` -- `{kv *core.KVStore}`. Thin wrapper around KV store operations with auth-specific logic.

**Critical paths (hot functions):**
- `GenerateKey()` -- 32 bytes from `crypto/rand`, `kek_` prefix, SHA-256 hash, store policy in KV. Returns clear-text token (only time it's visible).
- `VerifyToken()` -- SHA-256 hash of incoming token, KV lookup, JSON unmarshal. Called by HTTP middleware on every authenticated request.
- `HasAccess()` -- Role hierarchy check + namespace membership scan. O(n) over `Namespaces` slice.

## Architecture & Data Flow

**Three-role hierarchy:** `admin` (full access, bypasses all checks), `write` (read + write data to specific namespaces), `read` (read-only access to specific namespaces). The `HasAccess()` method checks role hierarchy and namespace membership. Admin unconditionally passes all checks.

**Key generation:** `GenerateKey()` produces 32 bytes of crypto-random data via `crypto/rand`, prefixes with `kek_`, SHA-256 hashes it, creates an `APIKeyPolicy` (ID, Description, Role, Namespaces, CreatedAt), and stores it under `_sys_auth::<hashed_token>` in the KV store. Only the clear-text token is returned to the caller once -- the hash is what gets persisted.

**Token verification:** `VerifyToken()` re-hashes the incoming token, looks up `_sys_auth::<hash>` in the KV store, unmarshals the policy JSON, and returns it. Used by the HTTP middleware to authorize incoming requests.

**Namespace enforcement:** Policies have a `Namespaces` slice (scoped index access, `["*"]` = wildcard). `HasAccess()` iterates the slice with O(n) lookup. The HTTP middleware extracts the target index from the URL path or JSON body and checks it against the policy's namespaces.

## Cross-Module Dependencies

**Depends on:**
- `pkg/core` -- `KVStore` for persistent storage of API key policies.
- `crypto/rand`, `crypto/sha256` -- Standard library for secure token generation and hashing.
- `encoding/json` -- Policy serialization/deserialization.

**Used by:**
- `internal/server` -- Creates `AuthService` on startup. Middleware calls `VerifyToken()` and `HasAccess()` on every authenticated request.
- `pkg/client` -- `CreateApiKey`, `ListApiKeys`, `RevokeApiKey` methods call the server's auth endpoints.

## Concurrency & Locking Rules

**No explicit concurrency control:** The module delegates all thread-safety concerns to the underlying `core.KVStore`. `GenerateKey`, `VerifyToken`, and `RevokeKey` all call KV store methods which are assumed to be thread-safe internally.

**SHA-256 hashing is stateless:** Token hashing uses `crypto/sha256.Sum256()` which is a pure function -- no shared state, trivially goroutine-safe.

**`crypto/rand` for token generation:** Uses the OS CSPRNG, not `math/rand`. Safe for concurrent use; each call produces independent random bytes.

## Known Pitfalls / Gotchas

- **No token expiration or rotation** -- Policies have a `CreatedAt` field but no `ExpiresAt`. Once a key is created, it's valid forever until manually revoked. There is no mechanism for automatic key rotation or time-limited tokens.
- **No rate limiting on `VerifyToken`** -- Every authenticated request triggers a KV lookup. An attacker with a valid but low-privilege key could flood the server with requests, each causing a KV read. No throttling or caching is implemented.
- **O(n) namespace scan** -- `HasAccess()` linearly iterates the `Namespaces` slice. For small lists (typical: 1-5 namespaces) this is fine. For large multi-tenant setups with hundreds of namespaces, a `map[string]struct{}` would be more efficient.
- **Clear-text token is returned only once** -- If the caller doesn't save the token from `GenerateKey()`, it's lost forever. The hash is stored, but SHA-256 is one-way. There is no "show key again" functionality.
- **`_sys_auth::` prefix is a convention, not enforced** -- Nothing prevents a user from creating a KV key with the `_sys_auth::` prefix manually. If they do, they could create a fake API key policy. The prefix is only a naming convention, not a security boundary.
- **Role hierarchy is minimal** -- Only 3 roles with simple checks. If you need more granular permissions (e.g., "read but not search", "write but not delete"), the current model doesn't support it.

## Design Trade-offs

| Trade-off | Decision | Rationale |
|---|---|---|
| **Tokens never stored in plaintext** | Only SHA-256 hashes persisted | Even a full database compromise does not expose valid API keys |
| **No token expiration/rotation** | `CreatedAt` field exists but no `ExpiresAt` | Simplicity; no mechanism for automatic key rotation or time-limited tokens |
| **No rate limiting on VerifyToken** | KV lookup per call with no throttling | Assumes external rate limiting (nginx, etc.) or trusted network |
| **O(n) namespace scan** | Linear iteration over `Namespaces` slice | Fine for small lists; a map or set would be better for large namespace lists |
| **Minimal role hierarchy** | Only 3 roles with simple checks | Easy to audit; could become hard to maintain if more roles are added |
| **`_sys_auth::` prefix convention** | Clean namespace for internal system keys | Separates auth data from user data in the KV store |
