package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/golusoris/golusoris/config"
)

// Encryptor seals/opens with a fixed key, so callers don't thread a raw key
// through every call. Build one with [NewEncryptor] or let the fx [Module]
// resolve the key from config.
type Encryptor struct {
	key []byte
}

// NewEncryptor validates the key (16/24/32 bytes for AES-128/192/256) and
// returns an Encryptor bound to it.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if _, err := newGCM(key); err != nil {
		return nil, err
	}
	return &Encryptor{key: key}, nil
}

// Seal encrypts plaintext under the bound key (see [Seal]).
func (e *Encryptor) Seal(plaintext []byte) ([]byte, error) { return Seal(e.key, plaintext) }

// Open decrypts sealed under the bound key (see [Open]).
func (e *Encryptor) Open(sealed []byte) ([]byte, error) { return Open(e.key, sealed) }

// SecureToken returns a cryptographically-random token of nBytes encoded as hex
// (2*nBytes chars). Use >= 16 bytes for unguessable tokens (session IDs, reset
// tokens, API keys).
func SecureToken(nBytes int) (string, error) {
	b, err := RandomBytes(nBytes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newEncryptor is the fx provider. It resolves the AES key from config:
//  1. crypto.key (hex-encoded 16/24/32 bytes) — preferred, a dedicated key;
//  2. else SHA-256(auth.jwt.secret) — reuse the app's JWT secret;
//  3. else an INSECURE built-in dev key (logged loudly — never for production).
func newEncryptor(cfg *config.Config, logger *slog.Logger) (*Encryptor, error) {
	if hexKey := cfg.String("crypto.key"); hexKey != "" {
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("crypto: decode crypto.key: %w", err)
		}
		return NewEncryptor(key)
	}
	if secret := cfg.String("auth.jwt.secret"); secret != "" {
		logger.Warn("crypto: deriving the encryption key from auth.jwt.secret; set crypto.key for a dedicated key")
		sum := sha256.Sum256([]byte(secret))
		return NewEncryptor(sum[:])
	}
	logger.Error("crypto: no crypto.key or auth.jwt.secret set — using an INSECURE built-in dev key; DO NOT run this in production")
	sum := sha256.Sum256([]byte("golusoris-insecure-dev-key"))
	return NewEncryptor(sum[:])
}
