package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestWithDefaults_zeroFillsTimeout(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, defaultTimeout)
	}
}

func TestWithDefaults_preservesTimeout(t *testing.T) {
	t.Parallel()
	c := Config{Timeout: 5 * time.Second}.withDefaults()
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v", c.Timeout)
	}
}

func TestNewClient_noEndpoint(t *testing.T) {
	t.Parallel()
	_, err := newClient(Config{})
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestNewClient_withEndpoint(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c, err := newClient(Config{Endpoint: srv.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if c == nil {
		t.Error("expected non-nil client")
	}
}

func TestRoundTrip_injectsAuth(t *testing.T) {
	t.Parallel()
	var gotAuth, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("X-Api-Key")
	}))
	t.Cleanup(srv.Close)

	tr := authTransport{
		base:        http.DefaultTransport,
		bearerToken: "tok",
		apiKey:      "key123",
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil) //nolint:noctx
	_, _ = tr.RoundTrip(req)
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotKey != "key123" {
		t.Errorf("X-Api-Key = %q", gotKey)
	}
}
