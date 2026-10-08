// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/golusoris/golusoris/core/config"
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
	return &Encryptor{key: append([]byte(nil), key...)}, nil
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

// newEncryptor is the fx provider. It requires crypto.key to contain a
// dedicated hex-encoded 16, 24, or 32-byte AES key.
func newEncryptor(cfg *config.Config) (*Encryptor, error) {
	if hexKey := cfg.String("crypto.key"); hexKey != "" {
		key, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("crypto: decode crypto.key: %w", err)
		}
		return NewEncryptor(key)
	}
	return nil, errors.New("crypto: crypto.key is required for Encryptor")
}
