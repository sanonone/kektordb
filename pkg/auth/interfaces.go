// Package auth provides Role-Based Access Control (RBAC) and API Key management for KektorDB.
package auth

// TokenVerifier is the only contract the middleware needs.
type TokenVerifier interface {
	VerifyToken(token string) (*APIKeyPolicy, error)
}

// KeyManager extends TokenVerifier for backends that own key lifecycle.
type KeyManager interface {
	TokenVerifier
	GenerateKey(description, role string, namespaces []string) (string, *APIKeyPolicy, error)
	RevokeKey(tokenID string) error
	PublicKeyJWKS() ([]byte, error) // serves /.well-known/jwks.json
}

// Backend is what the Server holds. Mode tells you which path is active.
type Backend struct {
	Mode     string // JWT | OIC
	Verifier TokenVerifier
}
