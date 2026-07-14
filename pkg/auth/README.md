# Module: pkg/auth

## Purpose

JWT-based authentication and authorization for KektorDB. Issues self-contained ES256 (ECDSA P-256) signed tokens with role-based access control (RBAC) and optional namespace isolation. Supports token generation, verification, key rotation, and revocation via a `jti` denylist. All state is persisted in the engine's KV store under the `_sys_auth::` namespace prefix.

## Key Types & Critical Paths

**Critical structs:**
- `JWTProvider` — Main auth facade: `signer *Signer`, `kvStore *core.KVStore`. Implements both `TokenVerifier` and `KeyManager` interfaces.
- `Signer` — Wraps an ECDSA P-256 private key. Loads or creates the key, persists it in KV at `_sys_auth::ecdsa_private_key`. Provides `SignToken()` and `PublicKey()`.
- `KektorClaims` — JWT claims embedded in every token: standard `RegisteredClaims` (sub, iat, nbf, exp, jti) plus `Role`, `Namespaces`, `Description`.
- `APIKeyPolicy` — Display-only struct returned on token creation: `ID` (jti), `Description`, `Role`, `Namespaces`, `CreatedAt`.

**Critical interfaces:**
- `TokenVerifier` — `VerifyToken(tokenStr string) (*KektorClaims, error)` — parses JWT, validates signature, checks expiry, and verifies the jti is not in the denylist.
- `KeyManager` — `GenerateKey(description, role string, namespaces []string) (string, *APIKeyPolicy, error)` plus `RevokeKey(jti string) error`, `ListKeys() ([]APIKeyPolicy, error)`.

**Critical paths (hot functions):**
- `GenerateKey` — Validates role (admin/write/read), generates UUID jti, builds `KektorClaims` with 90-day expiry, signs with ECDSA P-256, returns compact JWT string. Used by `POST /auth/keys`.
- `VerifyToken` — Parses JWT with the signer's public key, validates standard claims, checks `_sys_auth::revoked::<jti>` in KV denylist. Used by the auth middleware on every authenticated request.
- `loadOrCreateSigner` — Checks `_sys_auth::ecdsa_private_key` in KV. If found, loads the existing key. If not, generates a fresh ECDSA P-256 key, persists it as PEM-encoded ASN.1 DER, and returns the signer. Enables persistent key across restarts.
- `RevokeKey` — Writes a revocation marker at `_sys_auth::revoked::<jti>` in the KV store. `VerifyToken` checks this prefix before accepting a token. Revocation is permanent for the life of the KV store.
- `PublicKeyJWKS` — Returns the public key as a JWKS JSON document served at `GET /.well-known/jwks.json`. Enables third-party token verification without calling KektorDB.

## Architecture & Data Flow

**Token lifecycle:**
1. `POST /auth/keys` → `GenerateKey()` → signs JWT → returns compact token string (given to caller once, never stored)
2. Every authenticated request → `VerifyToken()` → parses JWT → checks signature against public key → checks `jti` not in denylist → extracts role/namespaces
3. `POST /auth/keys/{jti}/revoke` → `RevokeKey()` → writes `_sys_auth::revoked::<jti>` in KV
4. Key rotation: delete the private key from KV (`_sys_auth::ecdsa_private_key`), restart — on next boot `loadOrCreateSigner` generates a fresh key (all existing tokens invalidated)

**Auth middleware flow:** `internal/server/middleware.go` → `s.authService.VerifyToken(token)` → `JWTProvider.VerifyToken()` → returns `*KektorClaims` or error → role stored in request context via `context.WithValue`. Handlers check role via `auth.RoleFromContext(ctx)`.

**JWKS endpoint:** `GET /.well-known/jwks.json` (unauthenticated, on `rootMux`) → `jwtProvider.PublicKeyJWKS()` → returns EC P-256 public key as standard JWKS document. Clients use this to validate tokens locally without calling KektorDB on every request.

**Three roles:**
| Role | Permissions |
|------|------------|
| `admin` | Full access: CRUD on all indexes, KV store, system endpoints, auth management |
| `write` | Write access: VAdd, VLink, VSetMetadata. Read on own namespaces |
| `read` | Read-only: VSearch, VGet, GET endpoints. No mutations |

**Namespace isolation:** Tokens can be scoped to specific namespaces (e.g., `["tenant_a"]`). Read/write access is restricted to nodes within those namespaces via metadata prefix matching. A token with empty namespaces has unrestricted access (subject to role).

## Cross-Module Dependencies

**Depends on:**
- `github.com/golang-jwt/jwt/v5` — JWT parsing, signing, and validation with ES256 algorithm.
- `pkg/core/KVStore` — Persists the private key (`_sys_auth::ecdsa_private_key`) and revocation markers (`_sys_auth::revoked::*`).
- `crypto/ecdsa`, `crypto/elliptic` — ECDSA P-256 key generation and signing (Go stdlib).

**Used by:**
- `internal/server/server.go` — Creates `auth.NewJWTProvider(eng.DB.GetKVStore())` on startup. Assigns to both `authService TokenVerifier` and `keyManager KeyManager` fields.
- `internal/server/middleware.go` — Calls `s.authService.VerifyToken(token)` on every authenticated request.
- `internal/server/http_handlers.go` — `handleCreateAPIKey` calls `s.keyManager.GenerateKey()`. `handleListAPIKeys` calls `s.keyManager.ListKeys()`. `handleRevokeAPIKey` calls `s.keyManager.RevokeKey()`. `handleJWKS` calls `s.keyManager.(*auth.JWTProvider).PublicKeyJWKS()`.

**Used via tests:**
- `internal/server/rbac_test.go` — `TestRBACAndNamespaces` exercises the full token lifecycle: create → use → revoke → verify denied. `TestJWKSEndpoint` validates JWKS format, EC key type, and unauthenticated access.

## Concurrency & Locking Rules

**Thread-safe by design:** `JWTProvider` has no mutable state after construction. The signer's private key is loaded once at startup and never changes. Token verification is a pure computation (no side effects, no shared state) after resolving the public key. Unlimited concurrent `VerifyToken` calls are safe.

**KV store provides atomicity:** The private key and revocation markers are stored in `core.KVStore`, which is internally protected by a `sync.RWMutex`. `RevokeKey` does an atomic KV write; `VerifyToken` does an atomic KV read for denylist check. No race between token verification and revocation.

**Key rotation is manual and offline:** There is no in-process key rotation. To rotate keys, delete `_sys_auth::ecdsa_private_key` from the KV store and restart — the provider generates a fresh key on next boot. All previously issued tokens become invalid because the new key won't match their signature.

## Design Trade-offs & Known Pitfalls

**Self-contained vs opaque tokens:** JWT is self-contained — the token carries the role and namespaces directly. This eliminates KV lookups for authorization (only denylist checks are O(1) KV reads). The trade-off: tokens cannot be revoked server-side after they're minted — the caller has the secret and can verify locally. Revocation works via a denylist that `VerifyToken` checks; a token is valid until explicitly revoked AND the server sees the revocation.

**Key persistence:** The private key lives in the KV store at `_sys_auth::ecdsa_private_key` as PEM-encoded ASN.1 DER bytes. This means anyone with `read` access to the KV store (via KV GET endpoint) can read the private key and forge tokens. Acceptable for the self-hosted/localhost threat model (explicitly accepted per v0.6.0 scope decision). For production, restrict KV access or use external OIDC.

**Immutable key, offline rotation:** The key is generated once and never rotated automatically. The `RevokeKey` method only adds a jti to the denylist — it does not invalidate the signature itself. For true key rotation, delete the private key from KV and restart.

**No OIDC/OAuth2:** KektorDB does not implement OIDC/OAuth2 discovery or token exchange. The JWKS endpoint exists for third-party signature verification, not for federated identity. For OIDC-based auth, run KektorDB behind an OAuth proxy.
