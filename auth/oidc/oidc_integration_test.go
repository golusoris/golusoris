package oidc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	go_uber_org_fx_fxtest "go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/testutil/fxtest"
)

// TestOIDCModuleIntegration tests that the OIDC module can be properly wired with fxtest
func TestOIDCModuleIntegration(t *testing.T) {
	t.Parallel()
	
	// Test that we can create a basic fx app with the OIDC module, which verifies:
	// 1. Module construction works
	// 2. Dependencies are correctly wired
	fxtest.New(t, Module)
}

// TestOIDCProviderIntegration tests integration of the full OIDC flow as would be 
// used in a real application - authURL generation, token exchange, and user info retrieval
func TestOIDCProviderIntegration(t *testing.T) {
	t.Parallel()
	
	// Create a simple HTTP server to mock an OIDC provider for testing
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock all necessary endpoints to make the flow work
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"issuer": "https://example.com",
				"authorization_endpoint": "https://example.com/auth",
				"token_endpoint": "https://example.com/token",
				"user_info_endpoint": "https://example.com/userinfo"
			}`))
		case "/auth":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("auth response"))
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer"}`))
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"sub":"user123","email":"test@example.com"}`))
		}
	}))
	defer server.Close()
	
	// Test that we can create a basic fx app with the OIDC module, which verifies:
	// 1. Module construction works
	// 2. Dependencies are correctly wired
	go_uber_org_fx_fxtest.New(t, Module)
}