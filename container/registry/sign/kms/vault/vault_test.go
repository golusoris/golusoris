// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vault_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign/kms/vault"
)

func digestOf(h crypto.Hash, msg string) []byte {
	if h == crypto.Hash(0) {
		return []byte(msg)
	}
	d := h.New()
	d.Write([]byte(msg))
	return d.Sum(nil)
}

func newSigner(t *testing.T, cfg vault.Config) *vault.Signer {
	t.Helper()
	s, err := vault.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func tokenConfig(addr string) vault.Config {
	return vault.Config{Address: addr, Key: "k", Token: "s.root"}
}

func TestNew_InvalidConfigBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	base := tokenConfig(addr)
	for _, tc := range []struct {
		name string
		edit func(*vault.Config)
	}{
		{"no address", func(c *vault.Config) { c.Address = "" }},
		{"scheme", func(c *vault.Config) { c.Address = "ftp://vault:8200" }},
		{"no host", func(c *vault.Config) { c.Address = "https://" }},
		{"query", func(c *vault.Config) { c.Address = addr + "?x=1" }},
		{"fragment", func(c *vault.Config) { c.Address = addr + "#x" }},
		{"userinfo", func(c *vault.Config) { c.Address = "http://u:p@vault:8200" }},
		{"unparsable", func(c *vault.Config) { c.Address = "http://[::1" }},
		{"no credentials", func(c *vault.Config) { c.Token = "" }},
		{"token and role", func(c *vault.Config) { c.Role = "signer" }},
		{"auth mount without role", func(c *vault.Config) { c.AuthMount = "jwt" }},
		{"jwt file without role", func(c *vault.Config) { c.JWTFile = "/tmp/jwt" }},
		{"negative version", func(c *vault.Config) { c.KeyVersion = -1 }},
		{"negative timeout", func(c *vault.Config) { c.Timeout = -time.Second }},
		{"timeout -1ns", func(c *vault.Config) { c.Timeout = -1 }},
		{"empty key", func(c *vault.Config) { c.Key = "" }},
		{"key leading dash", func(c *vault.Config) { c.Key = "-k" }},
		{"key trailing dot", func(c *vault.Config) { c.Key = "k." }},
		{"key slash", func(c *vault.Config) { c.Key = "k/x" }},
		{"key space", func(c *vault.Config) { c.Key = "k x" }},
		{"key leading at", func(c *vault.Config) { c.Key = "@k" }},
		{"key colon", func(c *vault.Config) { c.Key = "k:k" }},
		{"key bracket", func(c *vault.Config) { c.Key = "k[k" }},
		{"key brace", func(c *vault.Config) { c.Key = "k{k" }},
		{"key backtick", func(c *vault.Config) { c.Key = "k`k" }},
		{"mount leading slash", func(c *vault.Config) { c.Mount = "/transit" }},
		{"mount trailing slash", func(c *vault.Config) { c.Mount = "transit/" }},
		{"mount empty segment", func(c *vault.Config) { c.Mount = "a//b" }},
		{"mount dotdot", func(c *vault.Config) { c.Mount = ".." }},
		{"mount escape", func(c *vault.Config) { c.Mount = "a/../sys" }},
		{"auth mount escape", func(c *vault.Config) {
			c.Token, c.Role, c.AuthMount = "", "signer", "../sys"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.edit(&cfg)
			if _, err := vault.New(t.Context(), cfg); !errors.Is(err, vault.ErrInvalidConfig) {
				t.Fatalf("New error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	t.Cleanup(func() {
		if n := f.get().requests; n != 0 {
			t.Errorf("invalid configs sent %d requests", n)
		}
	})
}

func TestNew_NameBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ key, mount string }{
		{"k", "t"},
		{"k_1-2.3@x", "secret/transit"},
		{"_", "a.b/c-d/e_f"},
		{"09azAZ", "t"},
	} {
		t.Run(tc.key+"@"+tc.mount, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey(tc.key, "ecdsa-p256", 1)
			cfg := tokenConfig(addr)
			cfg.Key, cfg.Mount = tc.key, tc.mount
			s := newSigner(t, cfg)
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if want := "v1/" + tc.mount + "/sign/" + tc.key + "/sha2-256"; "v1/"+f.get().signPath != want {
				t.Fatalf("sign path = %q, want %q", f.get().signPath, want)
			}
		})
	}
}

func TestNew_AddressPathPrefix(t *testing.T) {
	t.Parallel()
	f, _ := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	srv := httptest.NewServer(http.StripPrefix("/vault", f))
	t.Cleanup(srv.Close)
	s := newSigner(t, tokenConfig(srv.URL+"/vault"))
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

type signCase struct {
	name, typ string
	opts      crypto.SignerOpts
	path      string
	want      map[string]any
}

func signCases() []signCase {
	return []signCase{
		{"p256", "ecdsa-p256", crypto.SHA256, "sha2-256", map[string]any{"prehashed": true}},
		{"p384", "ecdsa-p384", crypto.SHA384, "sha2-384", map[string]any{"prehashed": true}},
		{"p521", "ecdsa-p521", crypto.SHA512, "sha2-512", map[string]any{"prehashed": true}},
		{"p256 sha224", "ecdsa-p256", crypto.SHA224, "sha2-224", map[string]any{"prehashed": true}},
		{"rsa pkcs1v15", "rsa-2048", crypto.SHA256, "sha2-256", map[string]any{"signature_algorithm": "pkcs1v15"}},
		{"rsa pss auto", "rsa-2048", &rsa.PSSOptions{Hash: crypto.SHA256}, "sha2-256", map[string]any{"signature_algorithm": "pss", "salt_length": "auto"}},
		{"rsa pss hash", "rsa-2048", &rsa.PSSOptions{Hash: crypto.SHA384, SaltLength: rsa.PSSSaltLengthEqualsHash}, "sha2-384", map[string]any{"salt_length": "hash"}},
		{"rsa pss 20", "rsa-2048", &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 20}, "sha2-256", map[string]any{"salt_length": "20"}},
		{"ed25519", "ed25519", crypto.Hash(0), "", map[string]any{"prehashed": false}},
	}
}

func TestSign_KeyTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range signCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			k := f.addKey("k", tc.typ, 1)
			s := newSigner(t, tokenConfig(addr))
			digest := digestOf(tc.opts.HashFunc(), "payload")
			sig, err := s.Sign(rand.Reader, digest, tc.opts)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !verifies(k.versions[1].Public(), digest, sig, tc.opts) {
				t.Fatal("signature does not verify under the key's public half")
			}
			got := f.get()
			if wantPath := strings.TrimSuffix("transit/sign/k/"+tc.path, "/"); got.signPath != wantPath {
				t.Errorf("path = %q, want %q", got.signPath, wantPath)
			}
			for key, v := range tc.want {
				if got.signBody[key] != v {
					t.Errorf("body[%s] = %v, want %v", key, got.signBody[key], v)
				}
			}
		})
	}
}

func verifies(pub crypto.PublicKey, digest, sig []byte, opts crypto.SignerOpts) bool {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, digest, sig)
	case ed25519.PublicKey:
		return ed25519.Verify(k, digest, sig)
	case *rsa.PublicKey:
		if pss, ok := opts.(*rsa.PSSOptions); ok {
			return rsa.VerifyPSS(k, opts.HashFunc(), digest, sig, pss) == nil
		}
		return rsa.VerifyPKCS1v15(k, opts.HashFunc(), digest, sig) == nil
	}
	return false
}

func TestNew_PinsKeyVersion(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	k := f.addKey("k", "ecdsa-p256", 2)
	for _, tc := range []struct{ asked, pinned int }{{0, 2}, {1, 1}, {2, 2}} {
		cfg := tokenConfig(addr)
		cfg.KeyVersion = tc.asked
		s := newSigner(t, cfg)
		if !k.versions[tc.pinned].Public().(*ecdsa.PublicKey).Equal(s.Public()) {
			t.Fatalf("KeyVersion %d: Public is not version %d", tc.asked, tc.pinned)
		}
		if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
			t.Fatalf("KeyVersion %d: Sign: %v", tc.asked, err)
		}
		if got := f.get().signBody["key_version"]; got != float64(tc.pinned) {
			t.Fatalf("KeyVersion %d: sent key_version %v, want %d", tc.asked, got, tc.pinned)
		}
	}
	cfg := tokenConfig(addr)
	cfg.KeyVersion = 3
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "version 3 not available") {
		t.Fatalf("KeyVersion 3 error = %v", err)
	}
}

func TestSign_RejectsOptionsBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.addKey("ed", "ed25519", 1)
	ec := newSigner(t, tokenConfig(addr))
	edCfg := tokenConfig(addr)
	edCfg.Key = "ed"
	ed := newSigner(t, edCfg)
	before := f.get().requests
	for _, tc := range []struct {
		name   string
		s      *vault.Signer
		digest []byte
		opts   crypto.SignerOpts
	}{
		{"nil opts", ec, digestOf(crypto.SHA256, "m"), nil},
		{"ecdsa without hash", ec, []byte("m"), crypto.Hash(0)},
		{"md5", ec, make([]byte, crypto.MD5.Size()), crypto.MD5},
		{"short digest", ec, make([]byte, sha256.Size-1), crypto.SHA256},
		{"long digest", ec, make([]byte, sha256.Size+1), crypto.SHA256},
		{"ed25519 prehashed", ed, digestOf(crypto.SHA512, "m"), crypto.SHA512},
	} {
		if _, err := tc.s.Sign(rand.Reader, tc.digest, tc.opts); !errors.Is(err, vault.ErrUnsupportedHash) {
			t.Errorf("%s: error = %v, want ErrUnsupportedHash", tc.name, err)
		}
	}
	if after := f.get().requests; after != before {
		t.Fatalf("rejected options sent %d requests", after-before)
	}
}

func TestSign_ServerAnswers(t *testing.T) {
	t.Parallel()
	other := genKey(t, "ecdsa-p256")
	otherSig, err := other.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		status     int
		body       string
	}{
		{"server error", "status 500: boom", 500, `{"errors":["boom"]}`},
		{"other version", "want version 1", 200, `{"data":{"signature":"vault:v2:AAAA","key_version":2}}`},
		{"version field", "want version 1", 200, `{"data":{"signature":"vault:v1:AAAA","key_version":2}}`},
		{"no prefix", "want version 1", 200, `{"data":{"signature":"AAAA","key_version":1}}`},
		{"bad base64", "decode signature", 200, `{"data":{"signature":"vault:v1:!!","key_version":1}}`},
		{"other key", "does not verify", 200, `{"data":{"signature":"vault:v1:` + b64(otherSig) + `","key_version":1}}`},
		{"no data", "no data", 200, `{"data":null}`},
		{"data absent", "no data in answer", 200, `{}`},
		{"no content", "status 204", 204, ``},
		{"not json", "decode answer", 200, `<html>`},
		{"bad data", "decode data", 200, `{"data":{"signature":1}}`},
		{"oversize", "over 1048576 bytes", 200, `{"data":"` + strings.Repeat("a", 1<<20) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "ecdsa-p256", 1)
			s := newSigner(t, tokenConfig(addr))
			f.set(func(f *fakeTransit) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					raw(t, w, tc.status, tc.body)
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

func writeJWT(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func roleConfig(addr, jwtFile string) vault.Config {
	return vault.Config{Address: addr, Key: "k", Role: "signer", JWTFile: jwtFile}
}

func TestRoleLogin_Mounts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ mount, path string }{
		{"", "auth/kubernetes/login"},
		{"jwt", "auth/jwt/login"},
		{"k8s/cluster-a", "auth/k8s/cluster-a/login"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "ecdsa-p256", 1)
			f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "sa.jwt", "" })
			cfg := roleConfig(addr, writeJWT(t, "sa.jwt\n"))
			cfg.AuthMount = tc.mount
			s := newSigner(t, cfg)
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if got := f.get(); got.loginPath != tc.path || got.logins != 1 {
				t.Fatalf("login path %q x%d, want %q x1", got.loginPath, got.logins, tc.path)
			}
		})
	}
}

func TestRoleLogin_RetriesDeniedOnce(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "jwt-1", "" })
	jwtFile := writeJWT(t, "jwt-1")
	s := newSigner(t, roleConfig(addr, jwtFile))
	// kubelet rotated the token file; the server revoked the Vault token.
	if err := os.WriteFile(jwtFile, []byte("jwt-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeTransit) { f.jwt, f.deny = "jwt-2", 1 })
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign after denial: %v", err)
	}
	if got := f.get().logins; got != 2 {
		t.Fatalf("logins = %d, want 2", got)
	}
	f.set(func(f *fakeTransit) { f.deny = 2 })
	_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("Sign denied twice error = %v", err)
	}
	if got := f.get().logins; got != 3 {
		t.Fatalf("logins = %d, want 3 (one retry per Sign)", got)
	}
}

func TestRoleLogin_ReloginFailure(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "jwt-1", "" })
	s := newSigner(t, roleConfig(addr, writeJWT(t, "jwt-1")))
	f.set(func(f *fakeTransit) { f.jwt, f.deny = "jwt-other", 1 })
	_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err == nil || !strings.Contains(err.Error(), "status 403") || !strings.Contains(err.Error(), "login: status 400") {
		t.Fatalf("Sign error = %v, want denial joined with login failure", err)
	}
}

func TestTokenAuth_NoReloginOnDenied(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	s := newSigner(t, tokenConfig(addr))
	f.set(func(f *fakeTransit) { f.deny = 1 })
	_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err == nil || strings.Contains(err.Error(), "login") {
		t.Fatalf("Sign error = %v, want the denial without a login attempt", err)
	}
	if got := f.get(); got.logins != 0 {
		t.Fatalf("token auth logged in %d times", got.logins)
	}
}

func TestRoleLogin_Concurrent(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "jwt", "" })
	s := newSigner(t, roleConfig(addr, writeJWT(t, "jwt")))
	f.set(func(f *fakeTransit) { f.token = "s.revoked" })
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			_, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
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
	if got := f.get().logins; got != 2 {
		t.Fatalf("logins = %d, want 2: concurrent denials share one re-login", got)
	}
}

func TestLogin_Failures(t *testing.T) {
	t.Parallel()
	exact := strings.Repeat("j", 64<<10)
	for _, tc := range []struct {
		name, file, jwt, login, want string
		path                         func(t *testing.T) string
	}{
		{name: "missing file", path: func(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "absent") }, want: "login: open "},
		{name: "directory", path: func(t *testing.T) string { t.Helper(); return t.TempDir() }, want: "login: read "},
		{name: "empty file", file: " \n", want: "want a JWT of 1..65536 bytes"},
		{name: "oversize file", file: exact + "j", want: "want a JWT of 1..65536 bytes"},
		{name: "wrong jwt", file: "other", jwt: "jwt", want: "login: status 400: invalid role or JWT"},
		{name: "empty client token", file: "jwt", jwt: "jwt", login: `{"auth":{"client_token":""}}`, want: "no client token"},
		{name: "no auth", file: "jwt", jwt: "jwt", login: `{}`, want: "no client token"},
		{name: "max size", file: exact, jwt: exact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "ecdsa-p256", 1)
			f.set(func(f *fakeTransit) { f.role, f.jwt = "signer", tc.jwt })
			if tc.login != "" {
				f.set(func(f *fakeTransit) { f.hook = loginAnswer(t, tc.login) })
			}
			var path string
			if tc.path != nil {
				path = tc.path(t)
			} else {
				path = writeJWT(t, tc.file)
			}
			_, err := vault.New(t.Context(), roleConfig(addr, path))
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("New error = %v, want %q", err, tc.want)
			}
		})
	}
}

// loginAnswer answers every login with body.
func loginAnswer(t *testing.T, body string) func(http.ResponseWriter, *http.Request) bool {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasSuffix(r.URL.Path, "/login") {
			return false
		}
		raw(t, w, http.StatusOK, body)
		return true
	}
}

func TestRoleLogin_DefaultJWTFile(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role = "signer" })
	_, err := vault.New(t.Context(), vault.Config{Address: addr, Key: "k", Role: "signer"})
	// Outside a pod the default token file is absent; inside one the fake refuses its JWT.
	if err == nil || !strings.Contains(err.Error(), vault.DefaultJWTFile) && !strings.Contains(err.Error(), "invalid role or JWT") {
		t.Fatalf("New error = %v, want the default JWT file read", err)
	}
}

func TestRoleLogin_NoReloginOnServerError(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "jwt", "" })
	s := newSigner(t, roleConfig(addr, writeJWT(t, "jwt")))
	f.set(func(f *fakeTransit) {
		f.hook = func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/login") {
				return false
			}
			raw(t, w, http.StatusInternalServerError, `{"errors":["boom"]}`)
			return true
		}
	})
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("Sign error = %v, want status 500", err)
	}
	if got := f.get().logins; got != 1 {
		t.Fatalf("logins = %d, want 1: only a denial re-logs in", got)
	}
}

func TestDo_TruncatedAnswer(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	s := newSigner(t, tokenConfig(addr))
	f.set(func(f *fakeTransit) {
		f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Length", "100")
			raw(t, w, http.StatusOK, `{"data":`)
			return true
		}
	})
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("Sign error = %v, want unexpected EOF", err)
	}
}

func TestDo_AnswerSizeBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		size int
		ok   bool
	}{{1 << 20, true}, {1<<20 + 1, false}} {
		f, addr := newFake(t)
		k := f.addKey("k", "ecdsa-p256", 1)
		body := padded(t, k.versions[1].Public(), tc.size)
		f.set(func(f *fakeTransit) {
			f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
				raw(t, w, http.StatusOK, body)
				return true
			}
		})
		_, err := vault.New(t.Context(), tokenConfig(addr))
		if (err == nil) != tc.ok {
			t.Fatalf("%d-byte answer: New error = %v, want ok %t", tc.size, err, tc.ok)
		}
	}
}

// padded returns a key-read answer for pub of exactly size bytes.
func padded(t *testing.T, pub crypto.PublicKey, size int) string {
	t.Helper()
	data := `{"type":"ecdsa-p256","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":` + quote(t, encodePublic(t, pub)) + `}}}`
	head := `{"data":` + data + `,"pad":"`
	return head + strings.Repeat("a", size-len(head)-2) + `"}`
}

func TestNew_TimeoutBoundary(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	cfg := tokenConfig(addr)
	cfg.Timeout = time.Nanosecond
	f.stall(t) // a fast fake could answer before a coarse clock (Windows) sees the 1ns deadline pass
	if _, err := vault.New(t.Context(), cfg); errors.Is(err, vault.ErrInvalidConfig) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("1ns timeout: New error = %v, want a valid config that times out", err)
	}
}

func TestNew_HTTPSTransport(t *testing.T) {
	t.Parallel()
	f, _ := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	cfg := tokenConfig(srv.URL)
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("New without the CA error = %v, want a certificate error", err)
	}
	cfg.Transport = srv.Client().Transport
	s := newSigner(t, cfg)
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign over https: %v", err)
	}
}

func TestTimeout_BoundsEachCall(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	cfg := tokenConfig(addr)
	cfg.Timeout = 200 * time.Millisecond
	s := newSigner(t, cfg)
	f.stall(t)
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
	if _, err := vault.New(t.Context(), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New error = %v, want deadline exceeded", err)
	}
}

func TestNew_UnsupportedKeys(t *testing.T) {
	t.Parallel()
	ecPEM := encodePublic(t, genKey(t, "ecdsa-p256").Public())
	edPub := encodePublic(t, genKey(t, "ed25519").Public())
	for _, tc := range []struct{ name, data, want string }{
		{"symmetric", `{"type":"aes256-gcm96","supports_signing":false,"latest_version":1,"keys":{"1":1700000000}}`, "is a aes256-gcm96 key (signing false"},
		{"derived", `{"type":"ed25519","supports_signing":true,"derived":true,"latest_version":1,"keys":{"1":{"public_key":` + quote(t, edPub) + `}}}`, "derived true"},
		{"version entry", `{"type":"ecdsa-p256","supports_signing":true,"latest_version":1,"keys":{"1":17}}`, "cannot unmarshal number"},
		{"type mismatch", `{"type":"rsa-2048","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":` + quote(t, ecPEM) + `}}}`, "public key is *ecdsa.PublicKey"},
		{"rsa key as ecdsa", `{"type":"ecdsa-p256","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":` + quote(t, encodePublic(t, genKey(t, "rsa-2048").Public())) + `}}}`, "public key is *rsa.PublicKey"},
		{"no pem", `{"type":"ecdsa-p256","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":"junk"}}}`, "no PEM public key"},
		{"bad der", `{"type":"ecdsa-p256","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":"-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"}}}`, "asn1: structure error"},
		{"ed25519 short", `{"type":"ed25519","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":"AAAA"}}}`, "ed25519 public key: 3 bytes"},
		{"ed25519 base64", `{"type":"ed25519","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":"!!"}}}`, "ed25519 public key"},
		{"ed25519 trailing junk", `{"type":"ed25519","supports_signing":true,"latest_version":1,"keys":{"1":{"public_key":` + quote(t, edPub+"!") + `}}}`, "illegal base64 data"},
		{"no data", ``, "no data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.set(func(f *fakeTransit) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					body := `{"data":null}`
					if tc.data != "" {
						body = `{"data":` + tc.data + `}`
					}
					raw(t, w, http.StatusOK, body)
					return true
				}
			})
			_, err := vault.New(t.Context(), tokenConfig(addr))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNew_KeyNotFoundOrDenied(t *testing.T) {
	t.Parallel()
	_, addr := newFake(t)
	if _, err := vault.New(t.Context(), tokenConfig(addr)); err == nil || !strings.Contains(err.Error(), "read key k: status 404") {
		t.Fatalf("missing key error = %v", err)
	}
	cfg := tokenConfig(addr)
	cfg.Token = "s.wrong"
	if _, err := vault.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "status 403: permission denied") {
		t.Fatalf("wrong token error = %v", err)
	}
}

// closeCounter counts response bodies opened and closed.
type closeCounter struct {
	rt             http.RoundTripper
	mu             sync.Mutex
	opened, closed int
}

func (c *closeCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.opened++
	c.mu.Unlock()
	resp.Body = &countedBody{ReadCloser: resp.Body, c: c}
	return resp, nil
}

type countedBody struct {
	io.ReadCloser
	c *closeCounter
}

func (b *countedBody) Close() error {
	b.c.mu.Lock()
	b.c.closed++
	b.c.mu.Unlock()
	return b.ReadCloser.Close()
}

func TestDo_ClosesEveryBody(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ecdsa-p256", 1)
	f.set(func(f *fakeTransit) { f.role, f.jwt, f.token = "signer", "jwt", "" })
	base := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(base.CloseIdleConnections)
	cc := &closeCounter{rt: base}
	cfg := roleConfig(addr, writeJWT(t, "jwt"))
	cfg.Transport = cc
	s := newSigner(t, cfg)
	f.set(func(f *fakeTransit) { f.deny = 1 })
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.opened != 5 || cc.closed != cc.opened {
		t.Fatalf("bodies opened %d, closed %d; want 5 and all closed", cc.opened, cc.closed)
	}
}
