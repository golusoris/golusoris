// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vault_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeKey is one transit key: a private key per version.
type fakeKey struct {
	typ      string
	versions map[int]crypto.Signer
	derived  bool
}

// fakeTransit serves the transit and login endpoints the signer calls, with
// real keys, so signatures verify.
type fakeTransit struct {
	t *testing.T

	mu        sync.Mutex
	keys      map[string]*fakeKey
	token     string
	role      string
	jwt       string
	logins    int
	requests  int
	deny      int
	signBody  map[string]any
	signPath  string
	loginPath string
	// hook answers a request itself when it returns true.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

func newFake(t *testing.T) (*fakeTransit, string) {
	t.Helper()
	f := &fakeTransit{t: t, keys: map[string]*fakeKey{}, token: "s.root"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func genKey(t *testing.T, typ string) crypto.Signer {
	t.Helper()
	var (
		k   crypto.Signer
		err error
	)
	switch typ {
	case "ecdsa-p256":
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa-p384":
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "ecdsa-p521":
		k, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "rsa-2048":
		k, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ed25519":
		_, k, err = ed25519.GenerateKey(rand.Reader)
	default:
		t.Fatalf("genKey: type %q", typ)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// addKey creates name with one fresh key per version 1..versions.
func (f *fakeTransit) addKey(name, typ string, versions int) *fakeKey {
	f.t.Helper()
	k := &fakeKey{typ: typ, versions: map[int]crypto.Signer{}}
	for v := 1; v <= versions; v++ {
		k.versions[v] = genKey(f.t, typ)
	}
	f.mu.Lock()
	f.keys[name] = k
	f.mu.Unlock()
	return k
}

func (f *fakeTransit) set(fn func(*fakeTransit)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// snapshot is what a test reads back from the fake.
type snapshot struct {
	logins, requests           int
	signBody                   map[string]any
	signPath, loginPath, token string
}

func (f *fakeTransit) get() snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return snapshot{logins: f.logins, requests: f.requests, signBody: f.signBody, signPath: f.signPath, loginPath: f.loginPath, token: f.token}
}

func reply(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("fake: write answer: %v", err)
	}
}

// raw answers with a verbatim body.
func raw(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("fake: write answer: %v", err)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func quote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fakeTransit) fail(w http.ResponseWriter, status int, msg string) {
	reply(f.t, w, status, map[string]any{"errors": []string{msg}})
}

func (f *fakeTransit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.hook != nil && f.hook(w, r) {
		return
	}
	if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
		f.fail(w, http.StatusBadRequest, "want a JSON body")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	if strings.HasPrefix(path, "auth/") && strings.HasSuffix(path, "/login") {
		f.login(w, r, path)
		return
	}
	if r.Header.Get("X-Vault-Token") != f.token || f.token == "" {
		f.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	if mount, name, ok := strings.Cut(path, "/keys/"); ok && r.Method == http.MethodGet {
		f.readKey(w, mount, name)
		return
	}
	if _, rest, ok := strings.Cut(path, "/sign/"); ok && r.Method == http.MethodPost {
		f.sign(w, r, path, rest)
		return
	}
	f.fail(w, http.StatusNotFound, "no handler for "+path)
}

func (f *fakeTransit) login(w http.ResponseWriter, r *http.Request, path string) {
	var body struct {
		Role string `json:"role"`
		JWT  string `json:"jwt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Role != f.role || body.JWT != f.jwt || f.role == "" {
		f.fail(w, http.StatusBadRequest, "invalid role or JWT")
		return
	}
	f.logins++
	f.loginPath = path
	f.token = "s.login-" + strconv.Itoa(f.logins)
	reply(f.t, w, http.StatusOK, map[string]any{"auth": map[string]any{"client_token": f.token, "lease_duration": 60}})
}

func (f *fakeTransit) readKey(w http.ResponseWriter, mount, name string) {
	k, ok := f.keys[name]
	if !ok || mount == "" {
		reply(f.t, w, http.StatusNotFound, map[string]any{"errors": []string{}})
		return
	}
	keys := map[string]any{}
	for v, priv := range k.versions {
		keys[strconv.Itoa(v)] = map[string]any{"public_key": encodePublic(f.t, priv.Public()), "name": k.typ}
	}
	reply(f.t, w, http.StatusOK, map[string]any{"data": map[string]any{
		"type": k.typ, "supports_signing": true, "derived": k.derived,
		"latest_version": len(k.versions), "keys": keys,
	}})
}

func encodePublic(t *testing.T, pub crypto.PublicKey) string {
	t.Helper()
	if ed, ok := pub.(ed25519.PublicKey); ok {
		return base64.StdEncoding.EncodeToString(ed)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

var fakeHashes = map[string]crypto.Hash{
	"sha2-224": crypto.SHA224, "sha2-256": crypto.SHA256,
	"sha2-384": crypto.SHA384, "sha2-512": crypto.SHA512,
}

func (f *fakeTransit) sign(w http.ResponseWriter, r *http.Request, path, rest string) {
	if f.deny > 0 {
		f.deny--
		f.fail(w, http.StatusForbidden, "permission denied")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	f.signBody, f.signPath = body, path
	name, hashName, _ := strings.Cut(rest, "/")
	k, ok := f.keys[name]
	if !ok {
		f.fail(w, http.StatusBadRequest, "no key")
		return
	}
	version := int(body["key_version"].(float64))
	input, err := base64.StdEncoding.DecodeString(body["input"].(string))
	if err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	sig, err := fakeSign(k.versions[version], input, fakeHashes[hashName], body)
	if err != nil {
		f.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	reply(f.t, w, http.StatusOK, map[string]any{"data": map[string]any{
		"signature":   "vault:v" + strconv.Itoa(version) + ":" + base64.StdEncoding.EncodeToString(sig),
		"key_version": version,
	}})
}

func fakeSign(priv crypto.Signer, input []byte, h crypto.Hash, body map[string]any) ([]byte, error) {
	var opts crypto.SignerOpts = h
	if _, ok := priv.(ed25519.PrivateKey); ok {
		opts = crypto.Hash(0)
	}
	if body["signature_algorithm"] == "pss" {
		salt := rsa.PSSSaltLengthAuto
		switch s := body["salt_length"].(string); s {
		case "hash":
			salt = rsa.PSSSaltLengthEqualsHash
		case "auto":
		default:
			n, err := strconv.Atoi(s)
			if err != nil {
				return nil, err
			}
			salt = n
		}
		opts = &rsa.PSSOptions{SaltLength: salt, Hash: h}
	}
	return priv.Sign(rand.Reader, input, opts)
}
