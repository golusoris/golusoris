package autocert_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/httpx/autotls/autocert"
)

func TestNewRequiresDomains(t *testing.T) {
	t.Parallel()
	_, err := autocert.New(autocert.Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Domains required") {
		t.Errorf("err = %q", err)
	}
}

func TestNewBuildsTLSConfig(t *testing.T) {
	t.Parallel()
	cfg, err := autocert.New(autocert.Options{
		Domains: []string{"api.example.test"},
		Cache:   t.TempDir(),
		Email:   "ops@example.test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cfg == nil {
		t.Fatal("nil TLS config")
	}
	if cfg.GetCertificate == nil {
		t.Error("TLS config has no GetCertificate callback")
	}
}
