// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

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
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

const fakeToken = "ya29.fake-token" // #nosec G101 -- a fake access token.

// fakeVersion is one CryptoKeyVersion with its private key.
type fakeVersion struct {
	name string
	alg  string
	priv crypto.Signer
	// pem overrides the public key getPublicKey answers with.
	pem string
}

// fakeKMS serves getPublicKey and asymmetricSign of the Cloud KMS REST API
// with real keys and CRC32C checksums, so signatures verify.
type fakeKMS struct {
	t *testing.T

	mu       sync.Mutex
	versions map[string]*fakeVersion
	requests int
	signs    int
	signBody map[string]any
	auth     string
	// hook answers a request itself when it returns true.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

func newFake(t *testing.T) (*fakeKMS, string) {
	t.Helper()
	f := &fakeKMS{t: t, versions: map[string]*fakeVersion{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func versionName(key string) string {
	return "projects/p/locations/europe-west3/keyRings/r/cryptoKeys/" + key + "/cryptoKeyVersions/1"
}

var (
	rsa3072 = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 3072) })
	rsa4096 = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 4096) })
)

func genKey(t *testing.T, alg string) crypto.Signer {
	t.Helper()
	var (
		k   crypto.Signer
		err error
	)
	switch {
	case alg == "EC_SIGN_P256_SHA256":
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case alg == "EC_SIGN_P384_SHA384":
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case alg == "EC_SIGN_ED25519":
		_, k, err = ed25519.GenerateKey(rand.Reader)
	case strings.Contains(alg, "_2048_"):
		k, err = rsa.GenerateKey(rand.Reader, 2048)
	case strings.Contains(alg, "_3072_"):
		k, err = rsa3072()
	case strings.Contains(alg, "_4096_"):
		k, err = rsa4096()
	default:
		t.Fatalf("genKey: algorithm %q", alg)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// addKey creates version 1 of key with algorithm alg.
func (f *fakeKMS) addKey(key, alg string) *fakeVersion {
	f.t.Helper()
	v := &fakeVersion{name: versionName(key), alg: alg, priv: genKey(f.t, alg)}
	f.mu.Lock()
	f.versions[v.name] = v
	f.mu.Unlock()
	return v
}

func (f *fakeKMS) set(fn func(*fakeKMS)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// snapshot is what a test reads back from the fake.
type snapshot struct {
	requests, signs int
	signBody        map[string]any
	auth            string
}

func (f *fakeKMS) get() snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return snapshot{requests: f.requests, signs: f.signs, signBody: f.signBody, auth: f.auth}
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

func crc(b []byte) string {
	return strconv.FormatUint(uint64(crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli))), 10)
}

func (f *fakeKMS) fail(w http.ResponseWriter, status int, code, msg string) {
	reply(f.t, w, status, map[string]any{"error": map[string]any{"code": status, "status": code, "message": msg}})
}

func (f *fakeKMS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	f.auth = r.Header.Get("Authorization")
	if f.hook != nil && f.hook(w, r) {
		return
	}
	if f.auth != "Bearer "+fakeToken {
		f.fail(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid credentials")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	if name, ok := strings.CutSuffix(path, "/publicKey"); ok && r.Method == http.MethodGet {
		f.publicKey(w, name)
		return
	}
	if name, ok := strings.CutSuffix(path, ":asymmetricSign"); ok && r.Method == http.MethodPost && r.Header.Get("Content-Type") == "application/json" {
		f.sign(w, r, name)
		return
	}
	f.fail(w, http.StatusNotFound, "NOT_FOUND", "no handler for "+path)
}

func (f *fakeKMS) publicKey(w http.ResponseWriter, name string) {
	v, ok := f.versions[name]
	if !ok {
		f.fail(w, http.StatusNotFound, "NOT_FOUND", name+" not found")
		return
	}
	p := v.pem
	if p == "" {
		p = encodePublic(f.t, v.priv.Public())
	}
	reply(f.t, w, http.StatusOK, map[string]any{
		"pem": p, "pemCrc32c": crc([]byte(p)), "algorithm": v.alg, "name": v.name, "protectionLevel": "HSM",
	})
}

func encodePublic(t *testing.T, pub crypto.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

var fakeHashes = map[string]crypto.Hash{"sha256": crypto.SHA256, "sha384": crypto.SHA384, "sha512": crypto.SHA512}

func (f *fakeKMS) sign(w http.ResponseWriter, r *http.Request, name string) {
	v, ok := f.versions[name]
	var body map[string]any
	if !ok || json.NewDecoder(r.Body).Decode(&body) != nil {
		f.fail(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown version or bad body")
		return
	}
	f.signs++
	f.signBody = body
	in, opts, field := f.input(body, v.alg)
	if in == nil {
		f.fail(w, http.StatusBadRequest, "INVALID_ARGUMENT", "input does not match the algorithm or its checksum")
		return
	}
	sig, err := v.priv.Sign(rand.Reader, in, opts)
	if err != nil {
		f.fail(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	reply(f.t, w, http.StatusOK, map[string]any{
		"signature": b64(sig), "signatureCrc32c": crc(sig), field: true, "name": v.name, "protectionLevel": "HSM",
	})
}

// input returns the bytes to sign, the options and the verified-checksum
// field, or nil when the request does not fit the algorithm.
func (f *fakeKMS) input(body map[string]any, alg string) ([]byte, crypto.SignerOpts, string) {
	if alg == "EC_SIGN_ED25519" {
		data, err := base64.StdEncoding.DecodeString(body["data"].(string))
		if err != nil || body["dataCrc32c"] != crc(data) {
			return nil, nil, ""
		}
		return data, crypto.Hash(0), "verifiedDataCrc32c"
	}
	digest, _ := body["digest"].(map[string]any)
	if len(digest) != 1 {
		return nil, nil, ""
	}
	for field, enc := range digest {
		h := fakeHashes[field]
		d, err := base64.StdEncoding.DecodeString(enc.(string))
		if err != nil || h == 0 || !strings.HasSuffix(alg, strings.ToUpper(field)) || len(d) != h.Size() || body["digestCrc32c"] != crc(d) {
			return nil, nil, ""
		}
		var opts crypto.SignerOpts = h
		if strings.HasPrefix(alg, "RSA_SIGN_PSS_") {
			opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
		}
		return d, opts, "verifiedDigestCrc32c"
	}
	return nil, nil, ""
}

// countingTokens hands out fakeToken and counts the calls.
type countingTokens struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (c *countingTokens) Token() (*oauth2.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &oauth2.Token{AccessToken: fakeToken, TokenType: "Bearer"}, nil
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
