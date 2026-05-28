package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"log"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sanonone/kektordb/pkg/core"
)

// Signer encapsulates the ECDSA private key and exposes only the operations
// the rest of the package needs. The raw key is never returned to callers.
type Signer struct {
	priv *ecdsa.PrivateKey
}

// SignToken signs a JWT claims set and returns the compact signed token string.
// This is the only path through which a token can be minted.
func (s *Signer) SignToken(claims jwt.Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(s.priv)
}

// PublicKey returns the public half of the keypair for token verification
// and JWKS endpoint serving. Read-only; cannot be used to sign.
func (s *Signer) PublicKey() *ecdsa.PublicKey {
	return &s.priv.PublicKey
}

// loadOrCreateSigner loads the persisted ECDSA P-256 private key from the KV store,
// or generates and persists a new one if none exists. Returns a Signer, so the
// private key is never accessible outside this package.
func loadOrCreateSigner(kv *core.KVStore) (*Signer, error) {
	const kvKey = "_sys_auth::ecdsa_private_key"

	if raw, ok := kv.Get(kvKey); ok {
		parsed, err := x509.ParsePKCS8PrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("auth: failed to parse stored private key: %w", err)
		}
		ecKey, ok := parsed.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("auth: stored key is not an ECDSA private key")
		}
		return &Signer{priv: ecKey}, nil
	}

	log.Println("auth: no signing key found — generating new ECDSA P-256 keypair")
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to generate ECDSA key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to marshal private key: %w", err)
	}
	kv.Set(kvKey, der)
	return &Signer{priv: priv}, nil
}
