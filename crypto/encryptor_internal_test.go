package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/config"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

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

	e, err := newEncryptor(cfgFromEnv(t), discard())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}
	if !bytes.Equal(e.key, key) {
		t.Error("encryptor did not use the configured hex key")
	}
}

func TestNewEncryptorDerivesFromJWTSecret(t *testing.T) {
	t.Setenv("APP_AUTH_JWT_SECRET", "a-real-jwt-signing-secret")

	e, err := newEncryptor(cfgFromEnv(t), discard())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}
	want := sha256.Sum256([]byte("a-real-jwt-signing-secret"))
	if !bytes.Equal(e.key, want[:]) {
		t.Error("encryptor key is not SHA-256 of the JWT secret")
	}
}

func TestNewEncryptorFallsBackToDevKey(t *testing.T) {
	t.Parallel() // runs after the serial t.Setenv tests restore the env
	// No APP_CRYPTO_KEY / APP_AUTH_JWT_SECRET set.
	e, err := newEncryptor(cfgFromEnv(t), discard())
	if err != nil {
		t.Fatalf("newEncryptor: %v", err)
	}
	// The dev key must still produce a working (if insecure) encryptor.
	sealed, err := e.Seal([]byte("hi"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	out, err := e.Open(sealed)
	if err != nil || string(out) != "hi" {
		t.Fatalf("dev-key round trip: out=%q err=%v", out, err)
	}
}

func TestNewEncryptorRejectsBadHexKey(t *testing.T) {
	t.Setenv("APP_CRYPTO_KEY", "not-hex")
	if _, err := newEncryptor(cfgFromEnv(t), discard()); err == nil {
		t.Fatal("want error for non-hex crypto.key")
	}
}
