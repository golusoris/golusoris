// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package crypto

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/config"
)

func cfgFromEnv(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

func TestNewEncryptorPrefersHexKey(t *testing.T) {
	key := bytes.Repeat([]byte{0xAB}, 32)
	t.Setenv("APP_CRYPTO_KEY", hex.EncodeToString(key))

	e, err := newEncryptor(cfgFromEnv(t))
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}
	if !bytes.Equal(e.key, key) {
		t.Error("encryptor did not use the configured hex key")
	}
}

func TestNewEncryptorDoesNotReuseJWTSecret(t *testing.T) {
	t.Setenv("APP_AUTH_JWT_SECRET", "a-real-jwt-signing-secret")

	_, err := newEncryptor(cfgFromEnv(t))
	if err == nil || !strings.Contains(err.Error(), "crypto.key") {
		t.Fatalf("newEncryptor with only JWT secret = %v, want missing crypto.key error", err)
	}
}

func TestNewEncryptorRejectsMissingKey(t *testing.T) {
	t.Parallel() // runs after the serial t.Setenv tests restore the env
	_, err := newEncryptor(cfgFromEnv(t))
	if err == nil || !strings.Contains(err.Error(), "crypto.key") {
		t.Fatalf("newEncryptor without key = %v, want missing crypto.key error", err)
	}
}

func TestNewEncryptorRejectsBadHexKey(t *testing.T) {
	t.Setenv("APP_CRYPTO_KEY", "not-hex")
	if _, err := newEncryptor(cfgFromEnv(t)); err == nil {
		t.Fatal("want error for non-hex crypto.key")
	}
}

func TestNewEncryptorClonesKey(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0xAB}, 32)
	encryptor, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := encryptor.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 0xff
	plaintext, err := encryptor.Open(sealed)
	if err != nil {
		t.Fatalf("Open after caller key mutation: %v", err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
