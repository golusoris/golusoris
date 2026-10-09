// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign/kms/gcp"
)

const (
	saGrant       = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	exchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"
	k8sToken      = "eyJhbGciOiJSUzI1NiJ9.k8s-sa.sig" // #nosec G101 -- a fake projected token.
)

// fakeTokenEndpoint issues fakeToken for a service account assertion or a
// token exchange of k8sToken, and counts the grants.
func fakeTokenEndpoint(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	grants := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch r.PostForm.Get("grant_type") {
		case saGrant:
			if strings.Count(r.PostForm.Get("assertion"), ".") != 2 {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
		case exchangeGrant:
			if r.PostForm.Get("subject_token") != k8sToken {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
			return
		}
		grants.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": fakeToken, "token_type": "Bearer", "expires_in": 3600,
			"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
		}); err != nil {
			t.Errorf("fake token endpoint: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/token", grants
}

func writeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, b)
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func serviceAccountFile(t *testing.T, tokenURL string) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return writeJSON(t, map[string]string{
		"type": "service_account", "project_id": "p", "private_key_id": "1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "signer@p.iam.gserviceaccount.com", "client_id": "1", "token_uri": tokenURL,
	})
}

// federationFile is a workload identity federation config reading a
// projected Kubernetes service account token.
func federationFile(t *testing.T, tokenURL string) string {
	t.Helper()
	return writeJSON(t, map[string]any{
		"type":               "external_account",
		"audience":           "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/pool/providers/k8s",
		"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
		"token_url":          tokenURL,
		"credential_source":  map[string]string{"file": writeTemp(t, []byte(k8sToken))},
	})
}

// TestNew_ApplicationDefaultCredentials finds credentials through
// GOOGLE_APPLICATION_CREDENTIALS: a service account key and a workload
// identity federation config. Not parallel: ADC reads process environment.
func TestNew_ApplicationDefaultCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(t *testing.T, tokenURL string) string
	}{
		{"service account", serviceAccountFile},
		{"workload identity federation", federationFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenURL, grants := fakeTokenEndpoint(t)
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tc.file(t, tokenURL))
			f, addr := newFake(t)
			f.addKey("k", "EC_SIGN_P256_SHA256")
			s := newSigner(t, gcp.Config{Key: versionName("k"), Endpoint: addr})
			for range 2 {
				if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
					t.Fatalf("Sign: %v", err)
				}
			}
			if n := grants.Load(); n != 1 {
				t.Fatalf("token grants = %d, want 1 (token reused)", n)
			}
		})
	}
}

func TestNew_ApplicationDefaultCredentialsFailures(t *testing.T) {
	_, addr := newFake(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "absent.json"))
	if _, err := gcp.New(t.Context(), gcp.Config{Key: versionName("k"), Endpoint: addr}); err == nil || !strings.Contains(err.Error(), "application default credentials") {
		t.Fatalf("missing credentials file: New error = %v", err)
	}
	tokenURL, _ := fakeTokenEndpoint(t)
	federation := federationFile(t, tokenURL)
	b, err := os.ReadFile(federation)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["credential_source"] = map[string]string{"file": writeTemp(t, []byte("other-token"))}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeJSON(t, cfg))
	if _, err := gcp.New(t.Context(), gcp.Config{Key: versionName("k"), Endpoint: addr}); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("rejected subject token: New error = %v", err)
	}
}
