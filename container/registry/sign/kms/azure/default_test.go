// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"crypto"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign/kms/azure"
)

const (
	tenant        = "00000000-0000-0000-0000-00000000000a"
	clientID      = "00000000-0000-0000-0000-00000000000b"
	assertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	saToken       = "eyJhbGciOiJSUzI1NiJ9.aks-sa.sig" // #nosec G101 -- a fake projected token.
)

// fakeEntra answers MSAL's instance and tenant discovery and issues
// fakeToken for a client assertion equal to saToken.
func fakeEntra(t *testing.T, grants *atomic.Int32) http.HandlerFunc {
	t.Helper()
	authority := "https://" + entraHost + "/" + tenant
	return func(w http.ResponseWriter, r *http.Request) {
		var answer map[string]any
		switch {
		case strings.HasSuffix(r.URL.Path, "/discovery/instance"):
			answer = map[string]any{
				"tenant_discovery_endpoint": authority + "/v2.0/.well-known/openid-configuration", "api-version": "1.1",
				"metadata": []map[string]any{{"preferred_network": entraHost, "preferred_cache": entraHost, "aliases": []string{entraHost}}},
			}
		case strings.HasSuffix(r.URL.Path, "/openid-configuration"):
			answer = map[string]any{
				"authorization_endpoint": authority + "/oauth2/v2.0/authorize", "token_endpoint": authority + "/oauth2/v2.0/token",
				"issuer": authority + "/v2.0",
			}
		case strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
			if err := r.ParseForm(); err != nil || r.PostForm.Get("client_assertion_type") != assertionType ||
				r.PostForm.Get("client_assertion") != saToken || r.PostForm.Get("client_id") != clientID {
				http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
				return
			}
			grants.Add(1)
			answer = map[string]any{"token_type": "Bearer", "expires_in": 3600, "access_token": fakeToken}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(answer); err != nil {
			t.Errorf("fake entra: %v", err)
		}
	}
}

// TestNew_WorkloadIdentity authenticates through
// azidentity.NewDefaultAzureCredential with the variables the AKS workload
// identity webhook injects. Not parallel: the chain reads process
// environment.
func TestNew_WorkloadIdentity(t *testing.T) {
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	var grants atomic.Int32
	f.set(func(f *fakeVault) {
		f.entra = fakeEntra(t, &grants)
		f.challenge = `Bearer authorization="https://` + entraHost + `/` + tenant + `", resource="https://vault.example.com"`
	})
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(saToken), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "WorkloadIdentityCredential")
	t.Setenv("AZURE_AUTHORITY_HOST", "https://"+entraHost)
	t.Setenv("AZURE_TENANT_ID", tenant)
	t.Setenv("AZURE_CLIENT_ID", clientID)
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", tokenFile)
	s := newSigner(t, azure.Config{Vault: vaultURL, Key: "k", Transport: rt})
	for range 2 {
		if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
			t.Fatalf("Sign: %v", err)
		}
	}
	if n := grants.Load(); n != 1 {
		t.Fatalf("token grants = %d, want 1 (token cached)", n)
	}
	if err := os.WriteFile(tokenFile, []byte("other-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := azure.New(t.Context(), azure.Config{Vault: vaultURL, Key: "k", Transport: rt}); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("rejected assertion: New error = %v", err)
	}
	t.Setenv("AZURE_TOKEN_CREDENTIALS", "NoSuchCredential")
	if _, err := azure.New(t.Context(), azure.Config{Vault: vaultURL, Key: "k", Transport: rt}); err == nil || !strings.Contains(err.Error(), "default azure credential") {
		t.Fatalf("unknown AZURE_TOKEN_CREDENTIALS: New error = %v", err)
	}
}
