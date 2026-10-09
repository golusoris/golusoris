// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vault_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign/kms/vault"
	"github.com/golusoris/golusoris/container/registry/sign/kms/vault/internal/vaultdev"
)

const (
	saNamespace = "exports"
	saName      = "signer"
	saSubject   = "system:serviceaccount:" + saNamespace + ":" + saName
	saUID       = "6f1c1a6e-9d0b-4c47-9d3e-6d3c1e6b2a10"
	saIssuer    = "https://kubernetes.default.svc"
	// tokenTTL is the Vault token lifetime of the JWT role; the test waits it
	// out to prove re-login.
	tokenTTL = 2 * time.Second
	// signerPolicy grants what the signer needs and nothing else.
	signerPolicy = `path "transit/keys/*" { capabilities = ["read"] }
path "transit/sign/*" { capabilities = ["update"] }`
)

var serverKeyTypes = []string{"ecdsa-p256", "ecdsa-p384", "rsa-2048", "ed25519"}

// setupTransit mounts transit with one key per type (named after the type)
// and the signer policy.
func setupTransit(t *testing.T, srv vaultdev.Server) {
	t.Helper()
	srv.Write(t, "sys/mounts/transit", map[string]any{"type": "transit"})
	for _, typ := range serverKeyTypes {
		srv.Write(t, "transit/keys/"+typ, map[string]any{"type": typ})
	}
	srv.Write(t, "sys/policies/acl/signer", map[string]any{"policy": signerPolicy})
}

func serverConfig(srv vaultdev.Server, key string) vault.Config {
	return vault.Config{Address: srv.Address, Key: key}
}

func TestServer(t *testing.T) {
	t.Parallel()
	for _, f := range []vaultdev.Flavor{vaultdev.Vault, vaultdev.OpenBao} {
		t.Run(string(f), func(t *testing.T) {
			t.Parallel()
			srv := vaultdev.Start(t, f)
			setupTransit(t, srv)
			t.Run("token", func(t *testing.T) {
				t.Parallel()
				testTokenAuth(t, srv)
			})
			t.Run("jwt", func(t *testing.T) {
				t.Parallel()
				testJWTAuth(t, srv)
			})
		})
	}
}

// testTokenAuth signs an image with every key type under a policy-scoped
// token, and checks a rotated key keeps the pinned version.
func testTokenAuth(t *testing.T, srv vaultdev.Server) {
	t.Helper()
	tok := srv.Write(t, "auth/token/create", map[string]any{"policies": []string{"signer"}, "ttl": "1h"}).Auth["client_token"].(string)
	for _, typ := range serverKeyTypes {
		cfg := serverConfig(srv, typ)
		cfg.Token = tok
		imageRoundTrip(t, newSigner(t, cfg))
	}
	cfg := serverConfig(srv, "ecdsa-p256")
	cfg.Token = tok
	pinned := newSigner(t, cfg)
	srv.Write(t, "transit/keys/ecdsa-p256/rotate", nil)
	imageRoundTrip(t, pinned)
	if latest := newSigner(t, cfg); latest.Public().(*ecdsa.PublicKey).Equal(pinned.Public()) {
		t.Fatal("rotated key: latest version has the pinned public key")
	}
	cfg.Token = "s.invalid"
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("invalid token error = %v", err)
	}
}

// testJWTAuth logs in with a service account JWT through the JWT auth
// method, which validates it against the issuer's public key; the login
// request is the Kubernetes auth method's.
func testJWTAuth(t *testing.T, srv vaultdev.Server) {
	t.Helper()
	issuer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(issuer.Public())
	if err != nil {
		t.Fatal(err)
	}
	srv.Write(t, "sys/auth/jwt", map[string]any{"type": "jwt"})
	srv.Write(t, "auth/jwt/config", map[string]any{
		"jwt_validation_pubkeys": []string{string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))},
		"bound_issuer":           saIssuer,
	})
	srv.Write(t, "auth/jwt/role/signer", map[string]any{
		"role_type": "jwt", "user_claim": "sub", "bound_audiences": []string{"vault"},
		"bound_subject": saSubject, "token_policies": []string{"signer"},
		"token_ttl": tokenTTL.String(), "token_max_ttl": tokenTTL.String(),
	})
	cfg := serverConfig(srv, "ecdsa-p256")
	cfg.Role, cfg.AuthMount, cfg.JWTFile = "signer", "jwt", writeJWT(t, mintJWT(t, issuer, "vault"))
	s := newSigner(t, cfg)
	imageRoundTrip(t, s)
	// The Vault token expires; Sign must log in again on the 403.
	time.Sleep(tokenTTL + time.Second)
	imageRoundTrip(t, s)

	cfg.JWTFile = writeJWT(t, mintJWT(t, issuer, "other-audience"))
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "login: status 400") {
		t.Fatalf("foreign audience error = %v", err)
	}
}

// mintJWT returns a projected service account token signed with ES256.
func mintJWT(t *testing.T, key *ecdsa.PrivateKey, aud string) string {
	t.Helper()
	claims := map[string]any{
		"iss": saIssuer, "sub": saSubject, "aud": []string{aud},
		// Fixed past and far-future instants: no wall clock in the test.
		"iat": 1700000000, "nbf": 1700000000, "exp": 4102444800,
		"kubernetes.io": map[string]any{
			"namespace":      saNamespace,
			"serviceaccount": map[string]any{"name": saName, "uid": saUID},
		},
	}
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "ES256", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestServerKubernetesAuth(t *testing.T) {
	t.Parallel()
	for _, f := range []vaultdev.Flavor{vaultdev.Vault, vaultdev.OpenBao} {
		t.Run(string(f), func(t *testing.T) {
			t.Parallel()
			testKubernetesAuth(t, f)
		})
	}
}

// testKubernetesAuth runs the Kubernetes auth method against a fake API
// server whose TokenReview accepts one service account token.
func testKubernetesAuth(t *testing.T, f vaultdev.Flavor) {
	t.Helper()
	issuer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwt := mintJWT(t, issuer, "vault")
	api := httptest.NewTLSServer(tokenReview(t, jwt))
	t.Cleanup(api.Close)
	srv := vaultdev.StartHostNetwork(t, f)
	setupTransit(t, srv)
	srv.Write(t, "sys/auth/kubernetes", map[string]any{"type": "kubernetes"})
	srv.Write(t, "auth/kubernetes/config", map[string]any{
		"kubernetes_host":      api.URL,
		"kubernetes_ca_cert":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw})),
		"token_reviewer_jwt":   "reviewer",
		"disable_local_ca_jwt": true,
	})
	srv.Write(t, "auth/kubernetes/role/signer", map[string]any{
		"bound_service_account_names": []string{saName}, "bound_service_account_namespaces": []string{saNamespace},
		"audience": "vault", "token_policies": []string{"signer"}, "token_ttl": "1h",
	})
	cfg := serverConfig(srv, "ecdsa-p256")
	cfg.Role, cfg.JWTFile = "signer", writeJWT(t, jwt)
	imageRoundTrip(t, newSigner(t, cfg))

	cfg.JWTFile = writeJWT(t, mintJWT(t, issuer, "vault")+"x")
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "login: status 403") {
		t.Fatalf("token the API server rejects: error = %v", err)
	}
}

// tokenReview answers TokenReview for the service account token want.
func tokenReview(t *testing.T, want string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var review struct {
			Spec struct {
				Token     string   `json:"token"`
				Audiences []string `json:"audiences"`
			} `json:"spec"`
		}
		if r.URL.Path != "/apis/authentication.k8s.io/v1/tokenreviews" || json.NewDecoder(r.Body).Decode(&review) != nil {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		status := map[string]any{"authenticated": false, "error": "invalid bearer token"}
		if review.Spec.Token == want {
			status = map[string]any{
				"authenticated": true, "audiences": review.Spec.Audiences,
				"user": map[string]any{"username": saSubject, "uid": saUID},
			}
		}
		reply(t, w, http.StatusCreated, map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview", "status": status,
		})
	})
}

var _ crypto.Signer = (*vault.Signer)(nil)
