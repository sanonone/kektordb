// Package auth provides Role-Based Access Control (RBAC) and API Key management for KektorDB.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sanonone/kektordb/pkg/core"
)

// Role definitions
const (
	RoleAdmin = "admin" // Full access to everything
	RoleWrite = "write" // Can read and write data to specific namespaces
	RoleRead  = "read"  // Read-only access to specific namespaces
)

// APIKeyPolicy defines the permissions associated with a specific API Key.
// This struct is serialized and stored in the KV Store.
type APIKeyPolicy struct {
	ID          string   `json:"id"`          // Short identifier (e.g., first 8 chars of the token)
	Description string   `json:"description"` // Human-readable description
	Role        string   `json:"role"`        // admin, write, read
	Namespaces  []string `json:"namespaces"`  // Allowed indexes. ["*"] means all.
	CreatedAt   int64    `json:"created_at"`  // Unix timestamp
}

// AuthService manages the creation, verification, and revocation of API Keys.
type AuthService struct {
	kvStore *core.KVStore
}

// NewAuthService creates a new authentication manager linked to the core KV Store.
func NewAuthService(kvStore *core.KVStore) *AuthService {
	return &AuthService{
		kvStore: kvStore,
	}
}

// GenerateKey creates a new API key and stores its hashed policy in the KV store.
// Returns the clear-text token (to be given to the user) and the stored policy.
func (s *AuthService) GenerateKey(description, role string, namespaces []string) (string, *APIKeyPolicy, error) {
	if role != RoleAdmin && role != RoleWrite && role != RoleRead {
		return "", nil, fmt.Errorf("invalid role: must be admin, write, or read")
	}

	// 1. Generate secure random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate random token: %w", err)
	}
	clearToken := "kek_" + hex.EncodeToString(tokenBytes)

	// 2. Hash the token (We NEVER store the clear-text token)
	hash := sha256.Sum256([]byte(clearToken))
	hashedKey := hex.EncodeToString(hash[:])

	// 3. Create Policy
	policy := &APIKeyPolicy{
		ID:          hashedKey, // Save prefix to identify it in UI/Lists
		Description: description,
		Role:        role,
		Namespaces:  namespaces,
		CreatedAt:   time.Now().Unix(),
	}

	// 4. Save to KV Store using a reserved internal prefix
	policyBytes, err := json.Marshal(policy)
	if err != nil {
		return "", nil, err
	}

	// Key format: _sys_auth::<hashed_token>
	storageKey := "_sys_auth::" + hashedKey
	s.kvStore.Set(storageKey, policyBytes)

	return clearToken, policy, nil
}

// VerifyToken checks a token and returns its associated policy.
// Returns an error if the token is invalid or not found.
func (s *AuthService) VerifyToken(clearToken string) (*APIKeyPolicy, error) {
	// Hash the incoming token
	hash := sha256.Sum256([]byte(clearToken))
	hashedKey := hex.EncodeToString(hash[:])
	storageKey := "_sys_auth::" + hashedKey

	// Lookup in KV Store
	policyBytes, found := s.kvStore.Get(storageKey)
	if !found {
		return nil, fmt.Errorf("invalid or revoked API key")
	}

	var policy APIKeyPolicy
	if err := json.Unmarshal(policyBytes, &policy); err != nil {
		return nil, fmt.Errorf("corrupted API key policy")
	}

	return &policy, nil
}

// RevokeKey removes a key from the database given its clear-text token or hash.
func (s *AuthService) RevokeKey(hashedKey string) {
	storageKey := "_sys_auth::" + hashedKey
	s.kvStore.Delete(storageKey)
}

// HasAccess verifies if a policy permits a specific action on a namespace.
func (p *APIKeyPolicy) HasAccess(requiredRole, targetNamespace string) bool {
	// Admin can do anything anywhere
	if p.Role == RoleAdmin {
		return true
	}

	// Role Check
	// If the route requires "read", both "read" and "write" roles are allowed.
	// If the route requires "write", only "write" is allowed.
	if requiredRole == RoleWrite && p.Role != RoleWrite {
		return false
	}

	// Namespace Check
	hasNamespaceAccess := false
	for _, ns := range p.Namespaces {
		if ns == "*" || ns == targetNamespace {
			hasNamespaceAccess = true
			break
		}
	}

	return hasNamespaceAccess
}
