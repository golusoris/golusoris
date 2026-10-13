// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	// vaultHost is under the challenge resource's domain, so the SDK's
	// challenge resource verification stays on.
	vaultHost        = "signing.vault.example.com"
	vaultURL         = "https://" + vaultHost
	entraHost        = "login.example.com"
	defaultChallenge = `Bearer authorization="https://login.example.com/tenant", resource="https://vault.example.com"`
	fakeToken        = "eyJ0eXAiOiJKV1QifQ.fake.token" // #nosec G101 -- a fake access token.
	wantedScope      = "https://vault.example.com/.default"
)

// fakeVersion is one key version with its private key.
type fakeVersion struct {
	id      string
	priv    crypto.Signer
	kty     string
	ops     []string
	enabled bool
}

// fakeVault serves the Key Vault keys data plane (get key, sign) behind the
// bearer challenge, with real keys, so signatures verify.
type fakeVault struct {
	t *testing.T

	mu       sync.Mutex
	keys     map[string][]*fakeVersion // name -> versions, last = current
	requests int
	signs    int
	signBody map[string]any
	signPath string
	// challenge is the WWW-Authenticate answer to an unauthorized request.
	challenge string
	// entra answers requests for other hosts (Microsoft Entra ID).
	entra http.HandlerFunc
	// hook answers an authorized request itself when it returns true.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

// newFake starts the vault over TLS for vaultHost and returns it with a
// transport that trusts it and dials it for any address.
func newFake(t *testing.T) (*fakeVault, http.RoundTripper) {
	t.Helper()
	f := &fakeVault{t: t, keys: map[string][]*fakeVersion{}, challenge: defaultChallenge}
	srv := httptest.NewUnstartedServer(f)
	cert, pool := selfSigned(t)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	rt := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(rt.CloseIdleConnections)
	return f, rt
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: vaultHost}, DNSNames: []string{vaultHost, entraHost, "login.microsoftonline.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, k.Public(), k)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, pool
}

func genKey(t *testing.T, kty string) crypto.Signer {
	t.Helper()
	var (
		k   crypto.Signer
		err error
	)
	switch kty {
	case "P-256":
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "P-384":
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "P-521":
		k, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "RSA":
		k, err = rsa.GenerateKey(rand.Reader, 2048)
	default:
		t.Fatalf("genKey: %q", kty)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// addKey creates name with one key per version; kind is "P-256", "P-384",
// "P-521" or "RSA".
func (f *fakeVault) addKey(name, kind string, versions int) []*fakeVersion {
	f.t.Helper()
	kty := "EC-HSM"
	if kind == "RSA" {
		kty = "RSA-HSM"
	}
	out := make([]*fakeVersion, 0, versions)
	for v := range versions {
		out = append(out, &fakeVersion{
			id: strings.Repeat(string(rune('a'+v)), 32), priv: genKey(f.t, kind), kty: kty,
			ops: []string{"sign", "verify"}, enabled: true,
		})
	}
	f.mu.Lock()
	f.keys[name] = out
	f.mu.Unlock()
	return out
}

func (f *fakeVault) set(fn func(*fakeVault)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// stall holds every request until the client hangs up, at most 10s, so only the client deadline ends it.
func (f *fakeVault) stall(t *testing.T) {
	t.Helper()
	f.set(func(f *fakeVault) {
		f.hook = func(_ http.ResponseWriter, r *http.Request) bool {
			// The server notices the client hanging up only after the body is read.
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("drain body: %v", err)
			}
			f.mu.Unlock()
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			f.mu.Lock()
			return true
		}
	})
}

// snapshot is what a test reads back from the fake.
type snapshot struct {
	requests, signs int
	signBody        map[string]any
	signPath        string
}

func (f *fakeVault) get() snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return snapshot{requests: f.requests, signs: f.signs, signBody: f.signBody, signPath: f.signPath}
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("fake: write answer: %v", err)
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *fakeVault) fail(w http.ResponseWriter, status int, code, msg string) {
	reply(f.t, w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (f *fakeVault) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entra != nil && r.Host != vaultHost {
		f.entra(w, r)
		return
	}
	f.requests++
	if r.Host != vaultHost || r.URL.Query().Get("api-version") == "" {
		f.fail(w, http.StatusBadRequest, "BadParameter", "host or api-version")
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		w.Header().Set("WWW-Authenticate", f.challenge)
		f.fail(w, http.StatusUnauthorized, "Unauthorized", "AKV10000: Request is missing a Bearer or PoP token.")
		return
	}
	if f.hook != nil && f.hook(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && len(parts) >= 2 && len(parts) <= 3 && parts[0] == "keys":
		f.getKey(w, parts[1], strings.Join(parts[2:], ""))
	case r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "keys" && parts[3] == "sign":
		f.sign(w, r, parts[1], parts[2])
	default:
		f.fail(w, http.StatusNotFound, "NotFound", r.URL.Path)
	}
}

// version finds a version of name; "" is the current one.
func (f *fakeVault) version(name, version string) *fakeVersion {
	vs := f.keys[name]
	if len(vs) == 0 {
		return nil
	}
	if version == "" {
		return vs[len(vs)-1]
	}
	for _, v := range vs {
		if v.id == version {
			return v
		}
	}
	return nil
}

func kid(name, version string) string { return vaultURL + "/keys/" + name + "/" + version }

func (f *fakeVault) getKey(w http.ResponseWriter, name, version string) {
	v := f.version(name, version)
	if v == nil {
		f.fail(w, http.StatusNotFound, "KeyNotFound", "A key with (name/id) "+name+" was not found in this key vault.")
		return
	}
	reply(f.t, w, http.StatusOK, map[string]any{
		"key":        jwk(f.t, kid(name, v.id), v),
		"attributes": map[string]any{"enabled": v.enabled, "recoveryLevel": "Recoverable"},
	})
}

func jwk(t *testing.T, id string, v *fakeVersion) map[string]any {
	t.Helper()
	k := map[string]any{"kid": id, "kty": v.kty, "key_ops": v.ops}
	switch pub := v.priv.Public().(type) {
	case *ecdsa.PublicKey:
		size := (pub.Curve.Params().BitSize + 7) / 8
		point, err := pub.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		k["crv"], k["x"], k["y"] = pub.Curve.Params().Name, b64(point[1:1+size]), b64(point[1+size:])
	case *rsa.PublicKey:
		k["n"], k["e"] = b64(pub.N.Bytes()), b64(big.NewInt(int64(pub.E)).Bytes())
	}
	return k
}

var fakeAlgs = map[string]crypto.SignerOpts{
	"ES256": crypto.SHA256, "ES384": crypto.SHA384, "ES512": crypto.SHA512,
	"RS256": crypto.SHA256, "RS384": crypto.SHA384, "RS512": crypto.SHA512,
	"PS256": &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: rsa.PSSSaltLengthEqualsHash},
	"PS384": &rsa.PSSOptions{Hash: crypto.SHA384, SaltLength: rsa.PSSSaltLengthEqualsHash},
	"PS512": &rsa.PSSOptions{Hash: crypto.SHA512, SaltLength: rsa.PSSSaltLengthEqualsHash},
}

func (f *fakeVault) sign(w http.ResponseWriter, r *http.Request, name, version string) {
	v := f.version(name, version)
	var body map[string]any
	if v == nil || json.NewDecoder(r.Body).Decode(&body) != nil {
		f.fail(w, http.StatusBadRequest, "BadParameter", "unknown key version or bad body")
		return
	}
	f.signs++
	f.signBody, f.signPath = body, r.URL.Path
	alg, _ := body["alg"].(string)
	value, _ := body["value"].(string)
	digest, err := base64.RawURLEncoding.DecodeString(value)
	opts, ok := fakeAlgs[alg]
	if err != nil || !ok || len(digest) != opts.HashFunc().Size() {
		f.fail(w, http.StatusBadRequest, "BadParameter", "bad algorithm or digest")
		return
	}
	sig, err := fakeSign(v.priv, digest, opts)
	if err != nil {
		f.fail(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}
	reply(f.t, w, http.StatusOK, map[string]any{"kid": kid(name, v.id), "value": b64(sig)})
}

// fakeSign signs as Key Vault does: ECDSA in the JOSE r||s form.
func fakeSign(priv crypto.Signer, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	sig, err := priv.Sign(rand.Reader, digest, opts)
	if err != nil {
		return nil, err
	}
	k, ok := priv.(*ecdsa.PrivateKey)
	if !ok {
		return sig, nil
	}
	return joseSignature(k.Curve, sig)
}

func joseSignature(curve elliptic.Curve, der []byte) ([]byte, error) {
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		return nil, err
	}
	size := (curve.Params().BitSize + 7) / 8
	out := make([]byte, 2*size)
	rs.R.FillBytes(out[:size])
	rs.S.FillBytes(out[size:])
	return out, nil
}

// fakeCredential hands out fakeToken and records the scopes asked for.
type fakeCredential struct {
	mu     sync.Mutex
	scopes []string
	err    error
}

func (c *fakeCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scopes = append(c.scopes, opts.Scopes...)
	if c.err != nil {
		return azcore.AccessToken{}, c.err
	}
	return azcore.AccessToken{Token: fakeToken, ExpiresOn: time.Now().Add(time.Hour)}, nil
}
