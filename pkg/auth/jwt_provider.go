package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sanonone/kektordb/pkg/core"
)

// KektorClaims are the JWT claims embedded in every token issued by JWTProvider.
// RegisteredClaims provides sub, iat, nbf, exp, jti automatically.
type KektorClaims struct {
	jwt.RegisteredClaims
	Role        string   `json:"role"`
	Namespaces  []string `json:"namespaces"`
	Description string   `json:"description"`
}

// JWTProvider container for Signer & KVStore to revoke & verify key
type JWTProvider struct {
	signer  *Signer
	kvStore *core.KVStore
}

// NewJWTProvider returns an instance of JWTProvider
func NewJWTProvider(kv *core.KVStore) (*JWTProvider, error) {
	signer, err := loadOrCreateSigner(kv)
	if err != nil {
		return nil, err
	}
	return &JWTProvider{signer: signer, kvStore: kv}, nil
}

// GenerateKey mints a signed JWT with the given role and namespace constraints.
// Returns the compact token string (given to the caller once, never stored)
// and a policy struct for display purposes.
func (j *JWTProvider) GenerateKey(description, role string, namespaces []string) (string, *APIKeyPolicy, error) {
	if role != RoleAdmin && role != RoleWrite && role != RoleRead {
		return "", nil, fmt.Errorf("invalid role: must be admin, write, or read")
	}

	now := time.Now()
	jti := uuid.New().String()

	claims := KektorClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(90 * 24 * time.Hour)),
		},
		Role:        role,
		Namespaces:  namespaces,
		Description: description,
	}

	tokenStr, err := j.signer.SignToken(claims)
	if err != nil {
		return "", nil, fmt.Errorf("failed to sign token: %w", err)
	}

	policy := &APIKeyPolicy{
		ID:          jti,
		Description: description,
		Role:        role,
		Namespaces:  namespaces,
		CreatedAt:   now.Unix(),
	}

	return tokenStr, policy, nil
}

// PublicKeyJWKS returns the public key as a JWKS JSON document.
// Serve this at GET /.well-known/jwks.json so clients can verify tokens
// without calling KektorDB on every request.
func (j *JWTProvider) PublicKeyJWKS() ([]byte, error) {
	pub := j.signer.PublicKey()
	if pub == nil {
		return nil, fmt.Errorf("auth: signer has no public key")
	}

	// Convert to ecdh.PublicKey; .Bytes() returns uncompressed 04 || X || Y.
	// For P-256 that is always exactly 1 + 32 + 32 = 65 bytes.
	ecdhPub, err := pub.ECDH()
	if err != nil {
		return nil, fmt.Errorf("auth: failed to convert public key to ECDH: %w", err)
	}
	raw := ecdhPub.Bytes() // [04, x0..x31, y0..y31]
	x := raw[1:33]
	y := raw[33:65]

	jwks := map[string]any{
		"keys": []map[string]string{
			{
				"kty": "EC",
				"crv": "P-256",
				"use": "sig",
				"alg": "ES256",
				"x":   base64.RawURLEncoding.EncodeToString(x),
				"y":   base64.RawURLEncoding.EncodeToString(y),
			},
		},
	}
	return json.Marshal(jwks)
}

func (j *JWTProvider) VerifyToken(token string) (*APIKeyPolicy, error) {

	const kvKey = "_sys_auth::ecdsa_private_key"
}

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