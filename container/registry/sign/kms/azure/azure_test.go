// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign/kms/azure"
)

func digestOf(h crypto.Hash, msg string) []byte {
	d := h.New()
	d.Write([]byte(msg))
	return d.Sum(nil)
}

func newSigner(t *testing.T, cfg azure.Config) *azure.Signer {
	t.Helper()
	s, err := azure.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func fakeConfig(rt http.RoundTripper, key string) azure.Config {
	return azure.Config{Vault: vaultURL, Key: key, Credential: &fakeCredential{}, Transport: rt}
}

func TestNew_InvalidConfigBeforeIO(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	cred := &fakeCredential{}
	base := azure.Config{Vault: vaultURL, Key: "k", Credential: cred, Transport: rt}
	for _, tc := range []struct {
		name string
		edit func(*azure.Config)
	}{
		{"no vault", func(c *azure.Config) { c.Vault = "" }},
		{"http vault", func(c *azure.Config) { c.Vault = "http://" + vaultHost }},
		{"no host", func(c *azure.Config) { c.Vault = "https://" }},
		{"userinfo", func(c *azure.Config) { c.Vault = "https://u:p@" + vaultHost }},
		{"query", func(c *azure.Config) { c.Vault = vaultURL + "?x=1" }},
		{"fragment", func(c *azure.Config) { c.Vault = vaultURL + "#x" }},
		{"path", func(c *azure.Config) { c.Vault = vaultURL + "/keys" }},
		{"unparsable", func(c *azure.Config) { c.Vault = "https://[::1" }},
		{"empty key", func(c *azure.Config) { c.Key = "" }},
		{"key 128", func(c *azure.Config) { c.Key = strings.Repeat("k", 128) }},
		{"key underscore", func(c *azure.Config) { c.Key = "k_1" }},
		{"key slash", func(c *azure.Config) { c.Key = "k/1" }},
		{"key dot", func(c *azure.Config) { c.Key = "k.1" }},
		{"key brace", func(c *azure.Config) { c.Key = "k{" }},
		{"key bracket", func(c *azure.Config) { c.Key = "k[" }},
		{"key backtick", func(c *azure.Config) { c.Key = "k`" }},
		{"version dash", func(c *azure.Config) { c.Version = "a-b" }},
		{"version 65", func(c *azure.Config) { c.Version = strings.Repeat("a", 65) }},
		{"version slash", func(c *azure.Config) { c.Version = "a/sign" }},
		{"negative timeout", func(c *azure.Config) { c.Timeout = -time.Second }},
		{"timeout -1ns", func(c *azure.Config) { c.Timeout = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.edit(&cfg)
			if _, err := azure.New(t.Context(), cfg); !errors.Is(err, azure.ErrInvalidConfig) {
				t.Fatalf("New error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	t.Cleanup(func() {
		cred.mu.Lock()
		defer cred.mu.Unlock()
		if n := f.get().requests; n != 0 || len(cred.scopes) != 0 {
			t.Errorf("invalid configs sent %d requests and %d token requests", n, len(cred.scopes))
		}
	})
}

func TestNew_NameBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ key, version string }{
		{strings.Repeat("k", 127), ""},
		{"a-b-1", ""},
		{"K", strings.Repeat("a", 32)},
		{"09AZaz", strings.Repeat("9", 64)},
	} {
		t.Run(tc.key[:1]+tc.version, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey(tc.key, "P-256", 1)[0]
			if tc.version != "" {
				f.set(func(*fakeVault) { v.id = tc.version })
			}
			cfg := fakeConfig(rt, tc.key)
			cfg.Version = tc.version
			s := newSigner(t, cfg)
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
		})
	}
}

type signCase struct {
	name, kind string
	opts       crypto.SignerOpts
	alg        string
}

func signCases() []signCase {
	return []signCase{
		{"p256", "P-256", crypto.SHA256, "ES256"},
		{"p384", "P-384", crypto.SHA384, "ES384"},
		{"p521", "P-521", crypto.SHA512, "ES512"},
		{"rs256", "RSA", crypto.SHA256, "RS256"},
		{"rs384", "RSA", crypto.SHA384, "RS384"},
		{"rs512", "RSA", crypto.SHA512, "RS512"},
		{"ps256 auto", "RSA", &rsa.PSSOptions{Hash: crypto.SHA256}, "PS256"},
		{"ps384 hash", "RSA", &rsa.PSSOptions{Hash: crypto.SHA384, SaltLength: rsa.PSSSaltLengthEqualsHash}, "PS384"},
		{"ps512 64", "RSA", &rsa.PSSOptions{Hash: crypto.SHA512, SaltLength: 64}, "PS512"},
	}
}

func TestSign_KeyTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range signCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey("k", tc.kind, 1)[0]
			s := newSigner(t, fakeConfig(rt, "k"))
			digest := digestOf(tc.opts.HashFunc(), "payload")
			sig, err := s.Sign(rand.Reader, digest, tc.opts)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !verifies(v.priv.Public(), digest, sig, tc.opts) {
				t.Fatal("signature does not verify under the key's public half")
			}
			got := f.get()
			if got.signBody["alg"] != tc.alg || got.signBody["value"] != b64(digest) || got.signPath != "/keys/k/"+v.id+"/sign" {
				t.Fatalf("request %s %v, want %s on version %s", got.signPath, got.signBody, tc.alg, v.id)
			}
		})
	}
}

func verifies(pub crypto.PublicKey, digest, sig []byte, opts crypto.SignerOpts) bool {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, digest, sig)
	case *rsa.PublicKey:
		if pss, ok := opts.(*rsa.PSSOptions); ok {
			return rsa.VerifyPSS(k, opts.HashFunc(), digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: pss.Hash}) == nil
		}
		return rsa.VerifyPKCS1v15(k, opts.HashFunc(), digest, sig) == nil
	}
	return false
}

func TestNew_PinsKeyVersion(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	vs := f.addKey("k", "P-256", 2)
	for _, tc := range []struct {
		asked  string
		pinned *fakeVersion
	}{{"", vs[1]}, {vs[0].id, vs[0]}, {vs[1].id, vs[1]}} {
		cfg := fakeConfig(rt, "k")
		cfg.Version = tc.asked
		s := newSigner(t, cfg)
		if !tc.pinned.priv.Public().(*ecdsa.PublicKey).Equal(s.Public()) {
			t.Fatalf("Version %q: Public is not version %s", tc.asked, tc.pinned.id)
		}
		// A rotation after New must not move the signer.
		f.set(func(f *fakeVault) { f.keys["k"] = append(f.keys["k"], f.keys["k"][0]) })
		if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
			t.Fatalf("Version %q: Sign: %v", tc.asked, err)
		}
		if got := f.get().signPath; got != "/keys/k/"+tc.pinned.id+"/sign" {
			t.Fatalf("Version %q: signed at %s, want version %s", tc.asked, got, tc.pinned.id)
		}
		f.set(func(f *fakeVault) { f.keys["k"] = vs })
	}
	cfg := fakeConfig(rt, "k")
	cfg.Version = strings.Repeat("z", 32)
	if _, err := azure.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "KeyNotFound") {
		t.Fatalf("missing version error = %v", err)
	}
}

func TestSign_RejectsOptionsBeforeIO(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("ec", "P-256", 1)
	f.addKey("p384", "P-384", 1)
	f.addKey("rsa", "RSA", 1)
	ec := newSigner(t, fakeConfig(rt, "ec"))
	p384 := newSigner(t, fakeConfig(rt, "p384"))
	rs := newSigner(t, fakeConfig(rt, "rsa"))
	before := f.get().requests
	for _, tc := range []struct {
		name   string
		s      *azure.Signer
		digest []byte
		opts   crypto.SignerOpts
	}{
		{"nil opts", ec, digestOf(crypto.SHA256, "m"), nil},
		{"no hash", ec, []byte("m"), crypto.Hash(0)},
		{"p256 with sha384", ec, digestOf(crypto.SHA384, "m"), crypto.SHA384},
		{"p384 with sha256", p384, digestOf(crypto.SHA256, "m"), crypto.SHA256},
		{"pss for ecdsa", ec, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256}},
		{"rsa sha224", rs, digestOf(crypto.SHA224, "m"), crypto.SHA224},
		{"rsa sha1", rs, digestOf(crypto.SHA1, "m"), crypto.SHA1},
		{"rsa no hash", rs, []byte("m"), crypto.Hash(0)},
		{"pss salt 20", rs, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 20}},
		{"pss salt 33", rs, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 33}},
		{"short digest", ec, make([]byte, sha256.Size-1), crypto.SHA256},
		{"long digest", rs, make([]byte, sha256.Size+1), crypto.SHA256},
	} {
		if _, err := tc.s.Sign(rand.Reader, tc.digest, tc.opts); !errors.Is(err, azure.ErrUnsupportedHash) {
			t.Errorf("%s: error = %v, want ErrUnsupportedHash", tc.name, err)
		}
	}
	if after := f.get().requests; after != before {
		t.Fatalf("rejected options sent %d requests", after-before)
	}
}

func TestSign_ServerAnswers(t *testing.T) {
	t.Parallel()
	other := genKey(t, "P-256")
	derSig, err := other.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := joseSignature(other.Public().(*ecdsa.PublicKey).Curve, derSig)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		status     int
		body       func(version string) string
	}{
		{"denied", "Forbidden", 403, func(string) string { return `{"error":{"code":"Forbidden","message":"no sign permission"}}` }},
		{"not json", "unmarshalling", 200, func(string) string { return `<html>` }},
		{"other version", "answer names key", 200, func(string) string {
			return `{"kid":"` + kid("k", strings.Repeat("f", 32)) + `","value":"` + b64(foreign) + `"}`
		}},
		{"other key", "answer names key", 200, func(v string) string { return `{"kid":"` + kid("x", v) + `","value":"` + b64(foreign) + `"}` }},
		{"other vault", "answer names key", 200, func(v string) string {
			return `{"kid":"https://other.vault.example.com/keys/k/` + v + `","value":"` + b64(foreign) + `"}`
		}},
		{"no kid", "no key ID", 200, func(string) string { return `{"value":"` + b64(foreign) + `"}` }},
		{"short signature", "63-byte ECDSA signature, want 64", 200, func(v string) string { return `{"kid":"` + kid("k", v) + `","value":"` + b64(foreign[1:]) + `"}` }},
		{"der signature", "ECDSA signature, want 64", 200, func(v string) string { return `{"kid":"` + kid("k", v) + `","value":"` + b64(derSig) + `"}` }},
		{"foreign signature", "does not verify", 200, func(v string) string { return `{"kid":"` + kid("k", v) + `","value":"` + b64(foreign) + `"}` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey("k", "P-256", 1)[0]
			s := newSigner(t, fakeConfig(rt, "k"))
			f.set(func(f *fakeVault) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					raw(t, w, tc.status, tc.body(v.id))
					return true
				}
			})
			_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Sign error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNew_UnsupportedKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		edit       func(k map[string]any, attrs map[string]any)
	}{
		{"disabled", "key disabled", func(_, a map[string]any) { a["enabled"] = false }},
		{"no sign op", "[verify wrapKey] lack sign", func(k, _ map[string]any) { k["key_ops"] = []string{"verify", "wrapKey"} }},
		{"null op", "[verify] lack sign", func(k, _ map[string]any) { k["key_ops"] = []any{nil, "verify"} }},
		{"no ops", "lack sign", func(k, _ map[string]any) { delete(k, "key_ops") }},
		{"symmetric", `key type "oct-HSM"`, func(k, _ map[string]any) { k["kty"] = "oct-HSM" }},
		{"no kty", "no key type", func(k, _ map[string]any) { delete(k, "kty") }},
		{"secp256k1", "curve", func(k, _ map[string]any) { k["crv"] = "P-256K" }},
		{"no curve", "curve <nil>", func(k, _ map[string]any) { delete(k, "crv") }},
		{"short x", "31- and 32-byte coordinates", func(k, _ map[string]any) { k["x"] = b64(make([]byte, 31)) }},
		{"short y", "32- and 31-byte coordinates", func(k, _ map[string]any) { k["y"] = b64(make([]byte, 31)) }},
		{"off curve", "not on curve", func(k, _ map[string]any) { k["x"] = b64(make([]byte, 32)) }},
		{"curve mismatch", "coordinates, want 48", func(k, _ map[string]any) { k["crv"] = "P-384" }},
		{"rsa no modulus", "0-byte modulus", func(k, _ map[string]any) { k["kty"], k["e"] = "RSA", b64([]byte{1, 0, 1}) }},
		{"rsa exponent 1", "exponent 1", func(k, _ map[string]any) { k["kty"], k["n"], k["e"] = "RSA", b64(make([]byte, 256)), b64([]byte{1}) }},
		{"rsa even exponent", "exponent 65536", func(k, _ map[string]any) {
			k["kty"], k["n"], k["e"] = "RSA", b64(make([]byte, 256)), b64([]byte{1, 0, 0})
		}},
		{"rsa exponent 2^64+3", "exponent 18446744073709551619", func(k, _ map[string]any) {
			k["kty"], k["n"], k["e"] = "RSA", b64(make([]byte, 256)), b64([]byte{1, 0, 0, 0, 0, 0, 0, 0, 3})
		}},
		{"rsa exponent 2^31+1", "exponent 2147483649", func(k, _ map[string]any) {
			k["kty"], k["n"], k["e"] = "RSA", b64(make([]byte, 256)), b64([]byte{0x80, 0, 0, 1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey("k", "P-256", 1)[0]
			f.set(func(f *fakeVault) {
				f.hook = func(w http.ResponseWriter, r *http.Request) bool {
					k := jwk(t, kid("k", v.id), v)
					attrs := map[string]any{"enabled": true}
					tc.edit(k, attrs)
					reply(t, w, http.StatusOK, map[string]any{"key": k, "attributes": attrs})
					return true
				}
			})
			_, err := azure.New(t.Context(), fakeConfig(rt, "k"))
			if !errors.Is(err, azure.ErrUnsupportedKey) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want ErrUnsupportedKey %q", err, tc.want)
			}
		})
	}
}

func TestNew_KeyAnswerBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(answer, k map[string]any)
	}{
		{"no attributes", func(a, _ map[string]any) { delete(a, "attributes") }},
		{"attributes without enabled", func(a, _ map[string]any) { a["attributes"] = map[string]any{} }},
		{"sign among null ops", func(_, k map[string]any) { k["key_ops"] = []any{nil, "sign"} }},
		{"rsa exponent 3", func(_, k map[string]any) { k["kty"], k["e"] = "RSA", b64([]byte{3}) }},
		{"rsa exponent 2^31-1", func(_, k map[string]any) { k["kty"], k["e"] = "RSA", b64([]byte{0x7f, 0xff, 0xff, 0xff}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey("k", "RSA", 1)[0]
			f.set(func(f *fakeVault) {
				f.hook = func(w http.ResponseWriter, r *http.Request) bool {
					if r.Method != http.MethodGet {
						return false
					}
					k := jwk(t, kid("k", v.id), v)
					answer := map[string]any{"key": k, "attributes": map[string]any{"enabled": true}}
					tc.edit(answer, k)
					reply(t, w, http.StatusOK, answer)
					return true
				}
			})
			s := newSigner(t, fakeConfig(rt, "k"))
			if _, ok := s.Public().(*rsa.PublicKey); !ok {
				t.Fatalf("Public = %T", s.Public())
			}
		})
	}
}

// recordingTransport answers every request with 400 and records the
// first URL and request deadline.
type recordingTransport struct {
	mu       sync.Mutex
	url      string
	deadline time.Duration
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.url == "" {
		r.url = req.URL.String()
		if dl, ok := req.Context().Deadline(); ok {
			r.deadline = time.Until(dl)
		}
	}
	return &http.Response{StatusCode: http.StatusBadRequest, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()
	rec := &recordingTransport{}
	cfg := azure.Config{Vault: vaultURL + "/", Key: "k", Credential: &fakeCredential{}, Transport: rec}
	if _, err := azure.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("New error = %v, want the 400 answer", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.HasPrefix(rec.url, vaultURL+"/keys/k/?api-version=") || rec.deadline <= 29*time.Second || rec.deadline > 30*time.Second {
		t.Fatalf("request %s with deadline in %v, want the key read within the 30s default", rec.url, rec.deadline)
	}
}

func TestNew_ForeignKeyAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, version, kid string }{
		{"other name", "", kid("x", strings.Repeat("a", 32))},
		{"no version", "", vaultURL + "/keys/k"},
		{"other vault", "", "https://other.vault.example.com/keys/k/" + strings.Repeat("a", 32)},
		{"other version", strings.Repeat("a", 32), kid("k", strings.Repeat("b", 32))},
		{"no kid", "", ""},
		{"bad kid", "", "https://[::1/keys/k/v"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			v := f.addKey("k", "P-256", 1)[0]
			f.set(func(f *fakeVault) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					k := jwk(t, tc.kid, v)
					if tc.kid == "" {
						delete(k, "kid")
					}
					reply(t, w, http.StatusOK, map[string]any{"key": k})
					return true
				}
			})
			cfg := fakeConfig(rt, "k")
			cfg.Version = tc.version
			if _, err := azure.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "get key k: answer") {
				t.Fatalf("New error = %v, want the foreign key ID rejected", err)
			}
		})
	}
	f, rt := newFake(t)
	f.set(func(f *fakeVault) {
		f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
			raw(t, w, http.StatusOK, `{"attributes":{"enabled":true}}`)
			return true
		}
	})
	if _, err := azure.New(t.Context(), fakeConfig(rt, "k")); err == nil || !strings.Contains(err.Error(), "no key in answer") {
		t.Fatalf("no key: New error = %v", err)
	}
}

func TestCredential_Challenge(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	cred := &fakeCredential{}
	cfg := fakeConfig(rt, "k")
	cfg.Credential = cred
	s := newSigner(t, cfg)
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cred.mu.Lock()
	scopes := cred.scopes
	cred.mu.Unlock()
	if len(scopes) == 0 || scopes[0] != wantedScope {
		t.Fatalf("token scopes = %v, want %s from the challenge", scopes, wantedScope)
	}
	cfg.Credential = &fakeCredential{err: errors.New("no managed identity")}
	if _, err := azure.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "no managed identity") {
		t.Fatalf("credential error: New error = %v", err)
	}
}

func TestCredential_ChallengeResourceVerified(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	f.set(func(f *fakeVault) {
		f.challenge = `Bearer authorization="https://login.example.com/tenant", resource="https://attacker.example.net"`
	})
	cred := &fakeCredential{}
	cfg := fakeConfig(rt, "k")
	cfg.Credential = cred
	if _, err := azure.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "doesn't match the requested domain") {
		t.Fatalf("foreign challenge: New error = %v", err)
	}
	cred.mu.Lock()
	defer cred.mu.Unlock()
	if len(cred.scopes) != 0 {
		t.Fatalf("token requested for a foreign resource: %v", cred.scopes)
	}
}

func TestSign_RetriesServerError(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	s := newSigner(t, fakeConfig(rt, "k"))
	failures := 1
	f.set(func(f *fakeVault) {
		f.hook = func(w http.ResponseWriter, r *http.Request) bool {
			if failures == 0 || !strings.HasSuffix(r.URL.Path, "/sign") {
				return false
			}
			failures--
			f.fail(w, http.StatusServiceUnavailable, "ServiceUnavailable", "throttled")
			return true
		}
	})
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign after one 503: %v", err)
	}
	if got := f.get().signs; got != 1 {
		t.Fatalf("signs = %d, want 1 after the retry", got)
	}
}

func TestSign_Concurrent(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	v := f.addKey("k", "P-256", 1)[0]
	s := newSigner(t, fakeConfig(rt, "k"))
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			d := digestOf(crypto.SHA256, "m"+string(rune('0'+i)))
			sig, err := s.Sign(rand.Reader, d, crypto.SHA256)
			if err == nil && !ecdsa.VerifyASN1(v.priv.Public().(*ecdsa.PublicKey), d, sig) {
				err = errors.New("signature does not verify")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
	}
	if got := f.get().signs; got != workers {
		t.Fatalf("signs = %d, want %d", got, workers)
	}
}

func TestNew_TimeoutBoundary(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	cfg := fakeConfig(rt, "k")
	cfg.Timeout = time.Nanosecond
	if _, err := azure.New(t.Context(), cfg); errors.Is(err, azure.ErrInvalidConfig) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("1ns timeout: New error = %v, want a valid config that times out", err)
	}
}

func TestTimeout_BoundsEachCall(t *testing.T) {
	t.Parallel()
	f, rt := newFake(t)
	f.addKey("k", "P-256", 1)
	cfg := fakeConfig(rt, "k")
	cfg.Timeout = 300 * time.Millisecond
	s := newSigner(t, cfg)
	f.set(func(f *fakeVault) {
		f.hook = func(_ http.ResponseWriter, r *http.Request) bool {
			// The server notices the client hanging up only after the body is read.
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Errorf("drain body: %v", err)
			}
			f.mu.Unlock()
			<-r.Context().Done()
			f.mu.Lock()
			return true
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Sign error = %v, want deadline exceeded", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Sign not bounded by Config.Timeout")
	}
	if _, err := azure.New(t.Context(), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New error = %v, want deadline exceeded", err)
	}
}
