// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign/kms/gcp"
)

func digestOf(h crypto.Hash, msg string) []byte {
	if h == crypto.Hash(0) {
		return []byte(msg)
	}
	d := h.New()
	d.Write([]byte(msg))
	return d.Sum(nil)
}

func newSigner(t *testing.T, cfg gcp.Config) *gcp.Signer {
	t.Helper()
	s, err := gcp.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func fakeConfig(addr, key string) gcp.Config {
	return gcp.Config{Key: versionName(key), Endpoint: addr, TokenSource: &countingTokens{}}
}

func TestNew_InvalidConfigBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	tokens := &countingTokens{}
	base := gcp.Config{Key: versionName("k"), Endpoint: addr, TokenSource: tokens}
	const ring = "projects/p/locations/l/keyRings/r"
	for _, tc := range []struct {
		name string
		edit func(*gcp.Config)
	}{
		{"empty key", func(c *gcp.Config) { c.Key = "" }},
		{"crypto key only", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k" }},
		{"no version id", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k/cryptoKeyVersions/" }},
		{"version not digits", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k/cryptoKeyVersions/1a" }},
		{"extra segment", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k/cryptoKeyVersions/1/x" }},
		{"leading slash", func(c *gcp.Config) { c.Key = "/" + ring + "/cryptoKeys/k/cryptoKeyVersions/1" }},
		{"collection case", func(c *gcp.Config) { c.Key = "projects/p/locations/l/keyrings/r/cryptoKeys/k/cryptoKeyVersions/1" }},
		{"dotdot ring", func(c *gcp.Config) { c.Key = "projects/p/locations/l/keyRings/../cryptoKeys/k/cryptoKeyVersions/1" }},
		{"dotdot project", func(c *gcp.Config) { c.Key = "projects/../locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1" }},
		{"dash first", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/-k/cryptoKeyVersions/1" }},
		{"colon in key", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/a:b/cryptoKeyVersions/1" }},
		{"dot in ring", func(c *gcp.Config) { c.Key = "projects/p/locations/l/keyRings/a.b/cryptoKeys/k/cryptoKeyVersions/1" }},
		{"query in key", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k?x/cryptoKeyVersions/1" }},
		{"bracket in key", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k[/cryptoKeyVersions/1" }},
		{"brace in key", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k{/cryptoKeyVersions/1" }},
		{"tilde first", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/~k/cryptoKeyVersions/1" }},
		{"equals in project", func(c *gcp.Config) { c.Key = "projects/a=b/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1" }},
		{"version dot", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k/cryptoKeyVersions/1.0" }},
		{"version dash", func(c *gcp.Config) { c.Key = ring + "/cryptoKeys/k/cryptoKeyVersions/1-" }},
		{"endpoint scheme", func(c *gcp.Config) { c.Endpoint = "ftp://kms" }},
		{"endpoint no host", func(c *gcp.Config) { c.Endpoint = "https://" }},
		{"endpoint userinfo", func(c *gcp.Config) { c.Endpoint = "https://u:p@kms" }},
		{"endpoint query", func(c *gcp.Config) { c.Endpoint = addr + "?x=1" }},
		{"endpoint fragment", func(c *gcp.Config) { c.Endpoint = addr + "#x" }},
		{"endpoint unparsable", func(c *gcp.Config) { c.Endpoint = "http://[::1" }},
		{"negative timeout", func(c *gcp.Config) { c.Timeout = -time.Second }},
		{"timeout -1ns", func(c *gcp.Config) { c.Timeout = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.edit(&cfg)
			if _, err := gcp.New(t.Context(), cfg); !errors.Is(err, gcp.ErrInvalidConfig) {
				t.Fatalf("New error = %v, want ErrInvalidConfig", err)
			}
		})
	}
	t.Cleanup(func() {
		if n := f.get().requests; n != 0 || tokens.calls != 0 {
			t.Errorf("invalid configs sent %d requests and %d token calls", n, tokens.calls)
		}
	})
}

func TestNew_KeyNameBoundaries(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"projects/p/locations/global/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1",
		"projects/example.com:my-proj/locations/us-east1/keyRings/ring_1/cryptoKeys/Key-2/cryptoKeyVersions/1234567890",
		"projects/0/locations/l/keyRings/R/cryptoKeys/9/cryptoKeyVersions/0",
		"projects/z/locations/A/keyRings/Z/cryptoKeys/zZaA09_-/cryptoKeyVersions/9",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			v := &fakeVersion{name: key, alg: "EC_SIGN_P256_SHA256", priv: genKey(t, "EC_SIGN_P256_SHA256")}
			f.set(func(f *fakeKMS) { f.versions[key] = v })
			s := newSigner(t, gcp.Config{Key: key, Endpoint: addr, TokenSource: &countingTokens{}})
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
		})
	}
}

func TestNew_EndpointPathPrefix(t *testing.T) {
	t.Parallel()
	f, _ := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	srv := httptest.NewServer(http.StripPrefix("/kms", f))
	t.Cleanup(srv.Close)
	s := newSigner(t, fakeConfig(srv.URL+"/kms", "k"))
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

type signCase struct {
	alg   string
	opts  crypto.SignerOpts
	field string
}

func signCases() []signCase {
	return []signCase{
		{"EC_SIGN_P256_SHA256", crypto.SHA256, "sha256"},
		{"EC_SIGN_P384_SHA384", crypto.SHA384, "sha384"},
		{"RSA_SIGN_PKCS1_2048_SHA256", crypto.SHA256, "sha256"},
		{"RSA_SIGN_PKCS1_4096_SHA512", crypto.SHA512, "sha512"},
		{"RSA_SIGN_PSS_2048_SHA256", &rsa.PSSOptions{Hash: crypto.SHA256}, "sha256"},
		{"RSA_SIGN_PSS_2048_SHA256", &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: rsa.PSSSaltLengthEqualsHash}, "sha256"},
		{"RSA_SIGN_PSS_4096_SHA512", &rsa.PSSOptions{Hash: crypto.SHA512, SaltLength: 64}, "sha512"},
		{"EC_SIGN_ED25519", crypto.Hash(0), "data"},
		{"EC_SIGN_ED25519", &ed25519.Options{}, "data"},
	}
}

func TestSign_Algorithms(t *testing.T) {
	t.Parallel()
	for _, tc := range signCases() {
		t.Run(tc.alg, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			v := f.addKey("k", tc.alg)
			s := newSigner(t, fakeConfig(addr, "k"))
			digest := digestOf(tc.opts.HashFunc(), "payload")
			sig, err := s.Sign(rand.Reader, digest, tc.opts)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !verifies(v.priv.Public(), digest, sig, tc.opts) {
				t.Fatal("signature does not verify under the key's public half")
			}
			body := f.get().signBody
			if tc.field == "data" {
				if body["data"] != b64(digest) || body["dataCrc32c"] != crc(digest) {
					t.Fatalf("request = %v, want data with its CRC32C", body)
				}
				return
			}
			if body["digest"].(map[string]any)[tc.field] != b64(digest) || body["digestCrc32c"] != crc(digest) {
				t.Fatalf("request = %v, want digest.%s with its CRC32C", body, tc.field)
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
			return rsa.VerifyPSS(k, opts.HashFunc(), digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: pss.Hash}) == nil
		}
		return rsa.VerifyPKCS1v15(k, opts.HashFunc(), digest, sig) == nil
	}
	return false
}

func TestSign_RejectsOptionsBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("ec", "EC_SIGN_P256_SHA256")
	f.addKey("pkcs1", "RSA_SIGN_PKCS1_2048_SHA256")
	f.addKey("pss", "RSA_SIGN_PSS_2048_SHA256")
	f.addKey("ed", "EC_SIGN_ED25519")
	ec := newSigner(t, fakeConfig(addr, "ec"))
	pkcs1 := newSigner(t, fakeConfig(addr, "pkcs1"))
	pss := newSigner(t, fakeConfig(addr, "pss"))
	ed := newSigner(t, fakeConfig(addr, "ed"))
	before := f.get().requests
	for _, tc := range []struct {
		name   string
		s      *gcp.Signer
		digest []byte
		opts   crypto.SignerOpts
	}{
		{"nil opts", ec, digestOf(crypto.SHA256, "m"), nil},
		{"ecdsa without hash", ec, []byte("m"), crypto.Hash(0)},
		{"p256 with sha384", ec, digestOf(crypto.SHA384, "m"), crypto.SHA384},
		{"p256 with sha512", ec, digestOf(crypto.SHA512, "m"), crypto.SHA512},
		{"short digest", ec, make([]byte, sha256.Size-1), crypto.SHA256},
		{"long digest", ec, make([]byte, sha256.Size+1), crypto.SHA256},
		{"empty digest", ec, nil, crypto.SHA256},
		{"pss for ecdsa", ec, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256}},
		{"pss for pkcs1 key", pkcs1, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256}},
		{"pkcs1 for pss key", pss, digestOf(crypto.SHA256, "m"), crypto.SHA256},
		{"pss salt 20", pss, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 20}},
		{"pss salt 33", pss, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 33}},
		{"pss wrong hash", pss, digestOf(crypto.SHA512, "m"), &rsa.PSSOptions{Hash: crypto.SHA512}},
		{"ed25519 prehashed", ed, digestOf(crypto.SHA512, "m"), crypto.SHA512},
		{"ed25519ph", ed, digestOf(crypto.SHA512, "m"), &ed25519.Options{Hash: crypto.SHA512}},
		{"ed25519ctx", ed, []byte("m"), &ed25519.Options{Context: "ctx"}},
	} {
		if _, err := tc.s.Sign(rand.Reader, tc.digest, tc.opts); !errors.Is(err, gcp.ErrUnsupportedHash) {
			t.Errorf("%s: error = %v, want ErrUnsupportedHash", tc.name, err)
		}
	}
	if after := f.get().requests; after != before {
		t.Fatalf("rejected options sent %d requests", after-before)
	}
}

func TestSign_ServerAnswers(t *testing.T) {
	t.Parallel()
	other := genKey(t, "EC_SIGN_P256_SHA256")
	otherSig, err := other.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	name := versionName("k")
	ok := `"signature":"` + b64(otherSig) + `","signatureCrc32c":"` + crc(otherSig) + `"`
	for _, tc := range []struct {
		name, want string
		status     int
		body       string
	}{
		{"denied", "status 403 PERMISSION_DENIED: no", 403, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no"}}`},
		{"server error", "status 500 : undecodable error body", 500, `<html>`},
		{"not json", "decode answer", 200, `<html>`},
		{"other version", `answer names "` + strings.TrimSuffix(name, "1") + `2"`, 200, `{` + ok + `,"verifiedDigestCrc32c":true,"name":"` + strings.TrimSuffix(name, "1") + `2"}`},
		{"no name", `answer names ""`, 200, `{` + ok + `,"verifiedDigestCrc32c":true}`},
		{"digest crc unverified", "did not verify the request checksum", 200, `{` + ok + `,"name":"` + name + `"}`},
		{"data crc instead", "did not verify the request checksum", 200, `{` + ok + `,"verifiedDataCrc32c":true,"name":"` + name + `"}`},
		{"signature crc", "signatureCrc32c \"1\"", 200, `{"signature":"` + b64(otherSig) + `","signatureCrc32c":"1","verifiedDigestCrc32c":true,"name":"` + name + `"}`},
		{"no signature crc", `signatureCrc32c ""`, 200, `{"signature":"` + b64(otherSig) + `","verifiedDigestCrc32c":true,"name":"` + name + `"}`},
		{"bad base64", "decode signature", 200, `{"signature":"!!","verifiedDigestCrc32c":true,"name":"` + name + `"}`},
		{"foreign signature", "does not verify", 200, `{` + ok + `,"verifiedDigestCrc32c":true,"name":"` + name + `"}`},
		{"oversize", "over 1048576 bytes", 200, `{"pad":"` + strings.Repeat("a", 1<<20) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "EC_SIGN_P256_SHA256")
			s := newSigner(t, fakeConfig(addr, "k"))
			f.set(func(f *fakeKMS) {
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

func TestSign_DataChecksum(t *testing.T) {
	t.Parallel()
	name := versionName("k")
	for _, tc := range []struct{ name, flags string }{
		{"digest flag for data", `"verifiedDigestCrc32c":true`},
		{"no flag", ``},
		{"data flag false", `"verifiedDataCrc32c":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			v := f.addKey("k", "EC_SIGN_ED25519")
			s := newSigner(t, fakeConfig(addr, "k"))
			sig, err := v.priv.Sign(rand.Reader, []byte("m"), crypto.Hash(0))
			if err != nil {
				t.Fatal(err)
			}
			body := `{"signature":"` + b64(sig) + `","signatureCrc32c":"` + crc(sig) + `","name":"` + name + `"` + strings.TrimSuffix(","+tc.flags, ",") + `}`
			f.set(func(f *fakeKMS) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					raw(t, w, http.StatusOK, body)
					return true
				}
			})
			if _, err := s.Sign(rand.Reader, []byte("m"), crypto.Hash(0)); !errors.Is(err, gcp.ErrChecksum) {
				t.Fatalf("Sign error = %v, want ErrChecksum", err)
			}
		})
	}
}

func TestNew_EveryAlgorithm(t *testing.T) {
	t.Parallel()
	for _, alg := range []string{
		"EC_SIGN_P256_SHA256", "EC_SIGN_P384_SHA384", "EC_SIGN_ED25519",
		"RSA_SIGN_PSS_2048_SHA256", "RSA_SIGN_PSS_3072_SHA256", "RSA_SIGN_PSS_4096_SHA256", "RSA_SIGN_PSS_4096_SHA512",
		"RSA_SIGN_PKCS1_2048_SHA256", "RSA_SIGN_PKCS1_3072_SHA256", "RSA_SIGN_PKCS1_4096_SHA256", "RSA_SIGN_PKCS1_4096_SHA512",
	} {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			v := f.addKey("k", alg)
			s := newSigner(t, fakeConfig(addr, "k"))
			if pub, ok := v.priv.Public().(interface{ Equal(x crypto.PublicKey) bool }); !ok || !pub.Equal(s.Public()) {
				t.Fatal("Public is not the key version's public key")
			}
		})
	}
}

func TestDo_TruncatedAnswer(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	s := newSigner(t, fakeConfig(addr, "k"))
	f.set(func(f *fakeKMS) {
		f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
			w.Header().Set("Content-Length", "100")
			raw(t, w, http.StatusOK, `{"signature":`)
			return true
		}
	})
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("Sign error = %v, want unexpected EOF", err)
	}
}

// recordingTransport answers every request with an error and records the
// last URL and request deadline.
type recordingTransport struct {
	mu       sync.Mutex
	url      string
	deadline time.Duration
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.url = req.URL.String()
	if dl, ok := req.Context().Deadline(); ok {
		r.deadline = time.Until(dl)
	}
	return nil, errors.New("offline")
}

func TestNew_Defaults(t *testing.T) {
	t.Parallel()
	rec := &recordingTransport{}
	cfg := gcp.Config{Key: versionName("k"), TokenSource: &countingTokens{}, Transport: rec}
	if _, err := gcp.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("New error = %v, want the transport error", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if want := "https://cloudkms.googleapis.com/v1/" + versionName("k") + "/publicKey"; rec.url != want {
		t.Fatalf("request URL = %q, want %q", rec.url, want)
	}
	if rec.deadline <= 29*time.Second || rec.deadline > 30*time.Second {
		t.Fatalf("request deadline in %v, want the 30s default timeout", rec.deadline)
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
	f.addKey("k", "EC_SIGN_P256_SHA256")
	base := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(base.CloseIdleConnections)
	cc := &closeCounter{rt: base}
	cfg := fakeConfig(addr, "k")
	cfg.Transport = cc
	s := newSigner(t, cfg)
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.opened != 2 || cc.closed != cc.opened {
		t.Fatalf("bodies opened %d, closed %d; want 2 and all closed", cc.opened, cc.closed)
	}
}

func TestSign_ChecksumErrors(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	s := newSigner(t, fakeConfig(addr, "k"))
	f.set(func(f *fakeKMS) {
		f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
			raw(t, w, http.StatusOK, `{"signature":"AAAA","signatureCrc32c":"7","verifiedDigestCrc32c":true,"name":"`+versionName("k")+`"}`)
			return true
		}
	})
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); !errors.Is(err, gcp.ErrChecksum) {
		t.Fatalf("Sign error = %v, want ErrChecksum", err)
	}
}

func TestNew_PublicKeyAnswers(t *testing.T) {
	t.Parallel()
	ecPEM := encodePublic(t, genKey(t, "EC_SIGN_P256_SHA256").Public())
	rsaPEM := encodePublic(t, genKey(t, "RSA_SIGN_PKCS1_2048_SHA256").Public())
	edPEM := encodePublic(t, genKey(t, "EC_SIGN_ED25519").Public())
	name := versionName("k")
	answer := func(pem, alg, sum, n string) string {
		return `{"pem":` + quote(pem) + `,"algorithm":"` + alg + `","pemCrc32c":"` + sum + `","name":"` + n + `"}`
	}
	for _, tc := range []struct {
		name, body, want string
		sentinel         error
	}{
		{"other name", answer(ecPEM, "EC_SIGN_P256_SHA256", crc([]byte(ecPEM)), name+"0"), "answer names", nil},
		{"pem crc", answer(ecPEM, "EC_SIGN_P256_SHA256", "1", name), "pemCrc32c", gcp.ErrChecksum},
		{"no pem crc", answer(ecPEM, "EC_SIGN_P256_SHA256", "", name), "pemCrc32c", gcp.ErrChecksum},
		{"raw pkcs1", answer(rsaPEM, "RSA_SIGN_RAW_PKCS1_2048", crc([]byte(rsaPEM)), name), `algorithm "RSA_SIGN_RAW_PKCS1_2048"`, gcp.ErrUnsupportedKey},
		{"secp256k1", answer(ecPEM, "EC_SIGN_SECP256K1_SHA256", crc([]byte(ecPEM)), name), "SECP256K1", gcp.ErrUnsupportedKey},
		{"ml-dsa", answer("", "PQ_SIGN_ML_DSA_65", crc(nil), name), "PQ_SIGN_ML_DSA_65", gcp.ErrUnsupportedKey},
		{"p384 alg with p256 key", answer(ecPEM, "EC_SIGN_P384_SHA384", crc([]byte(ecPEM)), name), "public key is *ecdsa.PublicKey", gcp.ErrUnsupportedKey},
		{"ec alg with rsa key", answer(rsaPEM, "EC_SIGN_P256_SHA256", crc([]byte(rsaPEM)), name), "public key is *rsa.PublicKey", gcp.ErrUnsupportedKey},
		{"rsa 3072 alg with 2048 key", answer(rsaPEM, "RSA_SIGN_PKCS1_3072_SHA256", crc([]byte(rsaPEM)), name), "public key is *rsa.PublicKey", gcp.ErrUnsupportedKey},
		{"rsa alg with ed25519 key", answer(edPEM, "RSA_SIGN_PKCS1_2048_SHA256", crc([]byte(edPEM)), name), "public key is ed25519.PublicKey", gcp.ErrUnsupportedKey},
		{"ed25519 alg with ec key", answer(ecPEM, "EC_SIGN_ED25519", crc([]byte(ecPEM)), name), "public key is *ecdsa.PublicKey", gcp.ErrUnsupportedKey},
		{"unknown alg with ed25519 key", answer(edPEM, "EC_SIGN_ED448", crc([]byte(edPEM)), name), `algorithm "EC_SIGN_ED448"`, gcp.ErrUnsupportedKey},
		{"no pem", answer("junk", "EC_SIGN_P256_SHA256", crc([]byte("junk")), name), "no PEM public key", gcp.ErrUnsupportedKey},
		{"bad der", answer("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n", "EC_SIGN_P256_SHA256", crc([]byte("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")), name), "asn1", gcp.ErrUnsupportedKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.set(func(f *fakeKMS) {
				f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
					raw(t, w, http.StatusOK, tc.body)
					return true
				}
			})
			_, err := gcp.New(t.Context(), fakeConfig(addr, "k"))
			if err == nil || !strings.Contains(err.Error(), tc.want) || tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Fatalf("New error = %v, want %q (%v)", err, tc.want, tc.sentinel)
			}
		})
	}
}

func TestNew_NotFoundOrDenied(t *testing.T) {
	t.Parallel()
	_, addr := newFake(t)
	if _, err := gcp.New(t.Context(), fakeConfig(addr, "k")); err == nil || !strings.Contains(err.Error(), "status 404 NOT_FOUND") {
		t.Fatalf("missing key error = %v", err)
	}
	cfg := fakeConfig(addr, "k")
	cfg.TokenSource = &countingTokens{err: errors.New("metadata server unreachable")}
	if _, err := gcp.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "token: metadata server unreachable") {
		t.Fatalf("token error = %v", err)
	}
}

func TestSign_ReusesToken(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	tokens := &countingTokens{}
	cfg := fakeConfig(addr, "k")
	cfg.TokenSource = tokens
	s := newSigner(t, cfg)
	for range 3 {
		if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
			t.Fatalf("Sign: %v", err)
		}
	}
	if got := f.get().auth; got != "Bearer "+fakeToken {
		t.Fatalf("Authorization = %q", got)
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if tokens.calls != 1 {
		t.Fatalf("token source called %d times, want 1 (reused)", tokens.calls)
	}
}

func TestSign_Concurrent(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	v := f.addKey("k", "EC_SIGN_P256_SHA256")
	s := newSigner(t, fakeConfig(addr, "k"))
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

func TestDo_AnswerSizeBoundary(t *testing.T) {
	t.Parallel()
	pub := encodePublic(t, genKey(t, "EC_SIGN_P256_SHA256").Public())
	for _, tc := range []struct {
		size int
		ok   bool
	}{{1 << 20, true}, {1<<20 + 1, false}} {
		f, addr := newFake(t)
		head := `{"pem":` + quote(pub) + `,"algorithm":"EC_SIGN_P256_SHA256","pemCrc32c":"` + crc([]byte(pub)) + `","name":"` + versionName("k") + `","pad":"`
		body := head + strings.Repeat("a", tc.size-len(head)-2) + `"}`
		f.set(func(f *fakeKMS) {
			f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
				raw(t, w, http.StatusOK, body)
				return true
			}
		})
		if _, err := gcp.New(t.Context(), fakeConfig(addr, "k")); (err == nil) != tc.ok {
			t.Fatalf("%d-byte answer: New error = %v, want ok %t", tc.size, err, tc.ok)
		}
	}
}

func TestNew_TimeoutBoundary(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	cfg := fakeConfig(addr, "k")
	cfg.Timeout = time.Nanosecond
	if _, err := gcp.New(t.Context(), cfg); errors.Is(err, gcp.ErrInvalidConfig) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("1ns timeout: New error = %v, want a valid config that times out", err)
	}
}

func TestTimeout_BoundsEachCall(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	cfg := fakeConfig(addr, "k")
	cfg.Timeout = 200 * time.Millisecond
	s := newSigner(t, cfg)
	f.set(func(f *fakeKMS) {
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
	if _, err := gcp.New(t.Context(), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New error = %v, want deadline exceeded", err)
	}
}

func TestNew_HTTPSTransport(t *testing.T) {
	t.Parallel()
	f, _ := newFake(t)
	f.addKey("k", "EC_SIGN_P256_SHA256")
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	cfg := fakeConfig(srv.URL, "k")
	if _, err := gcp.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("New without the CA error = %v, want a certificate error", err)
	}
	cfg.Transport = srv.Client().Transport
	s := newSigner(t, cfg)
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign over https: %v", err)
	}
}
