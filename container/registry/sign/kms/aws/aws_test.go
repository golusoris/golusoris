// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/sign/kms/aws"
)

func digestOf(h crypto.Hash, msg string) []byte {
	if h == crypto.Hash(0) {
		return []byte(msg)
	}
	d := h.New()
	d.Write([]byte(msg))
	return d.Sum(nil)
}

func newSigner(t *testing.T, cfg aws.Config) *aws.Signer {
	t.Helper()
	s, err := aws.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// fakeConfig names key k through the fake with unsigned requests.
func fakeConfig(addr, key string) aws.Config {
	return aws.Config{Key: key, Endpoint: addr, AWS: anonymous()}
}

func TestNew_InvalidConfigBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	base := fakeConfig(addr, "k")
	for _, tc := range []struct {
		name string
		edit func(*aws.Config)
	}{
		{"empty key", func(c *aws.Config) { c.Key = "" }},
		{"key 2049 bytes", func(c *aws.Config) { c.Key = "alias/" + strings.Repeat("a", 2049-6) }},
		{"key space", func(c *aws.Config) { c.Key = "alias/a b" }},
		{"key dot", func(c *aws.Config) { c.Key = "alias/a.b" }},
		{"key query", func(c *aws.Config) { c.Key = "k?x" }},
		{"key bracket", func(c *aws.Config) { c.Key = "alias/a[b" }},
		{"key brace", func(c *aws.Config) { c.Key = "alias/a{b" }},
		{"key backtick", func(c *aws.Config) { c.Key = "alias/a`b" }},
		{"key at", func(c *aws.Config) { c.Key = "alias/a@b" }},
		{"arn colon in alias", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1:111122223333:alias/a:b" }},
		{"arn service", func(c *aws.Config) { c.Key = "arn:aws:s3:us-east-1:111122223333:key/k" }},
		{"arn resource", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1:111122223333:secret/k" }},
		{"arn empty key id", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1:111122223333:key/" }},
		{"arn empty alias", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1:111122223333:alias/" }},
		{"arn no region", func(c *aws.Config) { c.Key = "arn:aws:kms::111122223333:key/k" }},
		{"arn no account", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1::key/k" }},
		{"arn no partition", func(c *aws.Config) { c.Key = "arn::kms:us-east-1:111122223333:key/k" }},
		{"arn short", func(c *aws.Config) { c.Key = "arn:aws:kms:us-east-1" }},
		{"arn region case", func(c *aws.Config) { c.Key = "arn:aws:kms:US-EAST-1:111122223333:key/k" }},
		{"region differs from arn", func(c *aws.Config) {
			c.Key, c.Region = "arn:aws:kms:eu-west-1:111122223333:key/k", "us-east-1"
		}},
		{"region underscore", func(c *aws.Config) { c.Region = "us_east_1" }},
		{"region leading dash", func(c *aws.Config) { c.Region = "-us-east-1" }},
		{"region trailing dash", func(c *aws.Config) { c.Region = "us-east-1-" }},
		{"region brace", func(c *aws.Config) { c.Region = "us-east{" }},
		{"region dot", func(c *aws.Config) { c.Region = "us.east-1" }},
		{"region upper", func(c *aws.Config) { c.Region = "Us-east-1" }},
		{"no region", func(c *aws.Config) { c.AWS = anonymous(); c.AWS.Region = "" }},
		{"endpoint scheme", func(c *aws.Config) { c.Endpoint = "ftp://kms" }},
		{"endpoint no host", func(c *aws.Config) { c.Endpoint = "https://" }},
		{"endpoint userinfo", func(c *aws.Config) { c.Endpoint = "https://u:p@kms" }},
		{"endpoint query", func(c *aws.Config) { c.Endpoint = addr + "?x=1" }},
		{"endpoint fragment", func(c *aws.Config) { c.Endpoint = addr + "#x" }},
		{"endpoint unparsable", func(c *aws.Config) { c.Endpoint = "http://[::1" }},
		{"negative timeout", func(c *aws.Config) { c.Timeout = -time.Second }},
		{"timeout -1ns", func(c *aws.Config) { c.Timeout = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			tc.edit(&cfg)
			if _, err := aws.New(t.Context(), cfg); !errors.Is(err, aws.ErrInvalidConfig) {
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

func TestNew_KeyForms(t *testing.T) {
	t.Parallel()
	long := "alias/" + strings.Repeat("a", 2048-6)
	for _, tc := range []struct{ name, key string }{
		{"key id", "1234abcd-12ab-34cd-56ef-1234567890ab"},
		{"key arn", "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"},
		{"alias", "alias/image-signing"},
		{"alias arn", "arn:aws:kms:us-east-1:111122223333:alias/image-signing"},
		{"alias 2048 bytes", long},
		{"alias boundary characters", "alias/AZaz09_-/x"},
		{"multi-region key id", "mrk-1234abcd12ab34cd56ef1234567890ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			id := "1234abcd-12ab-34cd-56ef-1234567890ab"
			if strings.HasPrefix(tc.key, "mrk-") {
				id = tc.key
			}
			k := f.addKey(id, "ECC_NIST_P256")
			f.alias("alias/image-signing", k.arn)
			f.alias(long, k.arn)
			f.alias("alias/AZaz09_-/x", k.arn)
			s := newSigner(t, fakeConfig(addr, tc.key))
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if got := f.get().signBody["KeyId"]; got != k.arn {
				t.Fatalf("Sign named key %v, want the pinned ARN %s", got, k.arn)
			}
		})
	}
}

func TestNew_RegionResolution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, key, region, sdkRegion, want string }{
		{"key arn region", "arn:aws:kms:eu-west-1:111122223333:key/k", "", "us-east-1", "eu-west-1/kms"},
		{"key arn and same region", "arn:aws:kms:eu-west-1:111122223333:key/k", "eu-west-1", "us-east-1", "eu-west-1/kms"},
		{"config region", "k", "ap-south-2", "us-east-1", "ap-south-2/kms"},
		{"sdk region", "k", "", "ca-central-1", "ca-central-1/kms"},
		{"region boundary characters", "k", "z0-a9", "us-east-1", "z0-a9/kms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKeyIn(strings.TrimSuffix(tc.want, "/kms"), "k", "ECC_NIST_P256")
			cfg := aws.Config{Key: tc.key, Region: tc.region, Endpoint: addr, AWS: static()}
			cfg.AWS.Region = tc.sdkRegion
			s := newSigner(t, cfg)
			if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if got := scope(f.get().auth); got != tc.want {
				t.Fatalf("SigV4 scope = %q, want %q", got, tc.want)
			}
		})
	}
}

type signCase struct {
	name, spec string
	opts       crypto.SignerOpts
	alg, mt    string
}

func signCases() []signCase {
	return []signCase{
		{"p256", "ECC_NIST_P256", crypto.SHA256, "ECDSA_SHA_256", "DIGEST"},
		{"p384", "ECC_NIST_P384", crypto.SHA384, "ECDSA_SHA_384", "DIGEST"},
		{"p521", "ECC_NIST_P521", crypto.SHA512, "ECDSA_SHA_512", "DIGEST"},
		{"rsa pkcs1 sha256", "RSA_2048", crypto.SHA256, "RSASSA_PKCS1_V1_5_SHA_256", "DIGEST"},
		{"rsa pkcs1 sha384", "RSA_2048", crypto.SHA384, "RSASSA_PKCS1_V1_5_SHA_384", "DIGEST"},
		{"rsa pkcs1 sha512", "RSA_2048", crypto.SHA512, "RSASSA_PKCS1_V1_5_SHA_512", "DIGEST"},
		{"rsa pss auto", "RSA_2048", &rsa.PSSOptions{Hash: crypto.SHA256}, "RSASSA_PSS_SHA_256", "DIGEST"},
		{"rsa pss hash", "RSA_2048", &rsa.PSSOptions{Hash: crypto.SHA384, SaltLength: rsa.PSSSaltLengthEqualsHash}, "RSASSA_PSS_SHA_384", "DIGEST"},
		{"rsa pss 64", "RSA_2048", &rsa.PSSOptions{Hash: crypto.SHA512, SaltLength: 64}, "RSASSA_PSS_SHA_512", "DIGEST"},
		{"ed25519", "ECC_NIST_EDWARDS25519", crypto.Hash(0), "ED25519_SHA_512", "RAW"},
		{"ed25519 options", "ECC_NIST_EDWARDS25519", &ed25519.Options{}, "ED25519_SHA_512", "RAW"},
	}
}

func TestSign_KeyTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range signCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			k := f.addKey("k", tc.spec)
			s := newSigner(t, fakeConfig(addr, "k"))
			digest := digestOf(tc.opts.HashFunc(), "payload")
			sig, err := s.Sign(rand.Reader, digest, tc.opts)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if !verifies(k.priv.Public(), digest, sig, tc.opts) {
				t.Fatal("signature does not verify under the key's public half")
			}
			got := f.get().signBody
			if got["SigningAlgorithm"] != tc.alg || got["MessageType"] != tc.mt || got["KeyId"] != k.arn {
				t.Fatalf("request = %v, want %s %s for %s", got, tc.alg, tc.mt, k.arn)
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

func TestSign_Ed25519MessageBoundary(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ECC_NIST_EDWARDS25519")
	s := newSigner(t, fakeConfig(addr, "k"))
	for _, n := range []int{0, 1, 4096} {
		if _, err := s.Sign(rand.Reader, make([]byte, n), crypto.Hash(0)); err != nil {
			t.Fatalf("%d-byte message: Sign: %v", n, err)
		}
	}
	before := f.get().requests
	if _, err := s.Sign(rand.Reader, make([]byte, 4097), crypto.Hash(0)); !errors.Is(err, aws.ErrUnsupportedHash) {
		t.Fatalf("4097-byte message: error = %v, want ErrUnsupportedHash", err)
	}
	if after := f.get().requests; after != before {
		t.Fatal("over-long message sent a request")
	}
}

func TestSign_RejectsOptionsBeforeIO(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("ec", "ECC_NIST_P256")
	f.addKey("rsa", "RSA_2048")
	f.addKey("ed", "ECC_NIST_EDWARDS25519")
	ec := newSigner(t, fakeConfig(addr, "ec"))
	rs := newSigner(t, fakeConfig(addr, "rsa"))
	ed := newSigner(t, fakeConfig(addr, "ed"))
	before := f.get().requests
	for _, tc := range []struct {
		name, want string
		s          *aws.Signer
		digest     []byte
		opts       crypto.SignerOpts
	}{
		{"nil opts", "nil signer options", ec, digestOf(crypto.SHA256, "m"), nil},
		{"ecdsa without hash", "unknown hash value 0", ec, []byte("m"), crypto.Hash(0)},
		{"sha224", "SHA-224", ec, digestOf(crypto.SHA224, "m"), crypto.SHA224},
		{"sha1", "SHA-1", rs, digestOf(crypto.SHA1, "m"), crypto.SHA1},
		{"p256 with sha384", "not ECDSA_SHA_384", ec, digestOf(crypto.SHA384, "m"), crypto.SHA384},
		{"short digest", "31-byte digest", ec, make([]byte, sha256.Size-1), crypto.SHA256},
		{"long digest", "33-byte digest", ec, make([]byte, sha256.Size+1), crypto.SHA256},
		{"pss for ecdsa", "PSS options", ec, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256}},
		{"pss salt 20", "PSS salt length 20", rs, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 20}},
		{"pss salt 33", "PSS salt length 33", rs, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: 33}},
		{"pss without hash", "unknown hash value 0", rs, []byte("m"), &rsa.PSSOptions{}},
		{"ed25519 prehashed", "Ed25519 signs the message", ed, digestOf(crypto.SHA512, "m"), crypto.SHA512},
		{"ed25519ph options", "Ed25519 signs the message", ed, digestOf(crypto.SHA512, "m"), &ed25519.Options{Hash: crypto.SHA512}},
		{"ed25519ctx", "Ed25519ctx", ed, []byte("m"), &ed25519.Options{Context: "ctx"}},
	} {
		if _, err := tc.s.Sign(rand.Reader, tc.digest, tc.opts); !errors.Is(err, aws.ErrUnsupportedHash) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want ErrUnsupportedHash %q", tc.name, err, tc.want)
		}
	}
	if after := f.get().requests; after != before {
		t.Fatalf("rejected options sent %d requests", after-before)
	}
}

func TestNew_RSASizes(t *testing.T) {
	t.Parallel()
	for _, spec := range []string{"RSA_2048", "RSA_3072", "RSA_4096"} {
		t.Run(spec, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			k := f.addKey("k", spec)
			s := newSigner(t, fakeConfig(addr, "k"))
			if !k.priv.Public().(*rsa.PublicKey).Equal(s.Public()) {
				t.Fatal("Public is not the KMS key")
			}
		})
	}
}

// recordingClient fails every request and records its URL and deadline.
type recordingClient struct {
	mu       sync.Mutex
	url      string
	deadline time.Duration
}

func (r *recordingClient) Do(req *http.Request) (*http.Response, error) {
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
	for _, tc := range []struct{ endpoint, want string }{
		{"", "https://kms.eu-west-1.amazonaws.com/"},
		{"https://vpce-1.kms.eu-west-1.vpce.amazonaws.com", "https://vpce-1.kms.eu-west-1.vpce.amazonaws.com/"},
	} {
		rec := &recordingClient{}
		sdk := static()
		sdk.HTTPClient = rec
		cfg := aws.Config{Key: "arn:aws:kms:eu-west-1:111122223333:key/k", Endpoint: tc.endpoint, AWS: sdk}
		if _, err := aws.New(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("endpoint %q: New error = %v, want the transport error", tc.endpoint, err)
		}
		rec.mu.Lock()
		if rec.url != tc.want || rec.deadline <= 29*time.Second || rec.deadline > 30*time.Second {
			t.Errorf("endpoint %q: request %s with deadline in %v, want %s within the 30s default", tc.endpoint, rec.url, rec.deadline, tc.want)
		}
		rec.mu.Unlock()
	}
}

func TestSign_KeyAlgorithmList(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	k := f.addKey("k", "RSA_2048")
	// A key policy cannot narrow the list, but GetPublicKey is authoritative.
	k.algs = []string{"RSASSA_PSS_SHA_256"}
	s := newSigner(t, fakeConfig(addr, "k"))
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), &rsa.PSSOptions{Hash: crypto.SHA256}); err != nil {
		t.Fatalf("listed algorithm: %v", err)
	}
	before := f.get().requests
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); !errors.Is(err, aws.ErrUnsupportedHash) || !strings.Contains(err.Error(), "RSASSA_PKCS1_V1_5_SHA_256") {
		t.Fatalf("unlisted algorithm: error = %v", err)
	}
	if f.get().requests != before {
		t.Fatal("unlisted algorithm sent a request")
	}
}

func TestSign_PinsAliasTarget(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	a := f.addKey("a", "ECC_NIST_P256")
	b := f.addKey("b", "ECC_NIST_P256")
	f.alias("alias/signing", a.arn)
	s := newSigner(t, fakeConfig(addr, "alias/signing"))
	f.alias("alias/signing", b.arn)
	sig, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign after the alias moved: %v", err)
	}
	if got := f.get().signBody["KeyId"]; got != a.arn || !a.priv.Public().(*ecdsa.PublicKey).Equal(s.Public()) {
		t.Fatalf("Sign named %v, want the pinned key %s", got, a.arn)
	}
	if !ecdsa.VerifyASN1(a.priv.Public().(*ecdsa.PublicKey), digestOf(crypto.SHA256, "m"), sig) {
		t.Fatal("signature is not from the pinned key")
	}
	if moved := newSigner(t, fakeConfig(addr, "alias/signing")); moved.Public().(*ecdsa.PublicKey).Equal(s.Public()) {
		t.Fatal("a new signer still sees the old alias target")
	}
}

func TestSign_ServerAnswers(t *testing.T) {
	t.Parallel()
	other := genKey(t, "ECC_NIST_P256")
	otherSig, err := other.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	const arn = "arn:aws:kms:us-east-1:111122223333:key/k"
	for _, tc := range []struct {
		name, want string
		status     int
		body       string
	}{
		{"denied", "AccessDeniedException", 400, `{"__type":"AccessDeniedException","message":"no"}`},
		{"server error", "StatusCode: 500", 500, `{"__type":"KMSInternalException","message":"boom"}`},
		{"other key", "answer from key", 200, `{"KeyId":"arn:aws:kms:us-east-1:111122223333:key/x","Signature":"` + b64(otherSig) + `","SigningAlgorithm":"ECDSA_SHA_256"}`},
		{"no key id", "answer from key", 200, `{"Signature":"` + b64(otherSig) + `","SigningAlgorithm":"ECDSA_SHA_256"}`},
		{"other algorithm", "answer from key", 200, `{"KeyId":"` + arn + `","Signature":"` + b64(otherSig) + `","SigningAlgorithm":"ECDSA_SHA_384"}`},
		{"foreign signature", "does not verify", 200, `{"KeyId":"` + arn + `","Signature":"` + b64(otherSig) + `","SigningAlgorithm":"ECDSA_SHA_256"}`},
		{"no signature", "does not verify", 200, `{"KeyId":"` + arn + `","SigningAlgorithm":"ECDSA_SHA_256"}`},
		{"not json", "deserialization failed", 200, `<html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "ECC_NIST_P256")
			s := newSigner(t, fakeConfig(addr, "k"))
			f.set(func(f *fakeKMS) {
				f.hook = func(w http.ResponseWriter, _ *http.Request, op string) bool {
					if op != "Sign" {
						return false
					}
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

// spki returns a SubjectPublicKeyInfo for a curve Go does not implement.
func spki(t *testing.T, curve asn1.ObjectIdentifier) []byte {
	t.Helper()
	params, err := asn1.Marshal(curve)
	if err != nil {
		t.Fatal(err)
	}
	der, err := asn1.Marshal(struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}{
		Algorithm: pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}, Parameters: asn1.RawValue{FullBytes: params}},
		PublicKey: asn1.BitString{Bytes: append([]byte{4}, make([]byte, 64)...), BitLength: 65 * 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestNew_UnsupportedKeys(t *testing.T) {
	t.Parallel()
	p256DER, err := x509.MarshalPKIXPublicKey(genKey(t, "ECC_NIST_P256").Public())
	if err != nil {
		t.Fatal(err)
	}
	secp256k1 := spki(t, asn1.ObjectIdentifier{1, 3, 132, 0, 10})
	for _, tc := range []struct {
		name, want string
		edit       func(k *fakeKey)
	}{
		{"encrypt usage", `key usage "ENCRYPT_DECRYPT"`, func(k *fakeKey) { k.usage = "ENCRYPT_DECRYPT" }},
		{"no algorithms", "signing algorithms []", func(k *fakeKey) { k.algs = nil }},
		{"secp256k1", "unsupported elliptic curve", func(k *fakeKey) { k.spec, k.der = "ECC_SECG_P256K1", secp256k1 }},
		{"p384 spec with p256 key", `key spec "ECC_NIST_P384": public key is *ecdsa.PublicKey`, func(k *fakeKey) { k.spec = "ECC_NIST_P384" }},
		{"rsa spec with ec key", `key spec "RSA_2048": public key is *ecdsa.PublicKey`, func(k *fakeKey) { k.spec = "RSA_2048" }},
		{"ed25519 spec with ec key", "public key is *ecdsa.PublicKey", func(k *fakeKey) { k.spec = "ECC_NIST_EDWARDS25519" }},
		{"unknown spec", `key spec "ML_DSA_65"`, func(k *fakeKey) { k.spec = "ML_DSA_65" }},
		{"bad der", "asn1", func(k *fakeKey) { k.der = []byte{0x30, 0x01} }},
		{"other p256 der", "", func(k *fakeKey) { k.der = p256DER }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			k := f.addKey("k", "ECC_NIST_P256")
			f.set(func(*fakeKMS) { tc.edit(k) })
			s, err := aws.New(t.Context(), fakeConfig(addr, "k"))
			if tc.want == "" {
				// A swapped public key passes New and fails at the first Sign.
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if _, serr := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); serr == nil || !strings.Contains(serr.Error(), "does not verify") {
					t.Fatalf("Sign with a swapped public key = %v, want does not verify", serr)
				}
				return
			}
			if !errors.Is(err, aws.ErrUnsupportedKey) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error = %v, want ErrUnsupportedKey %q", err, tc.want)
			}
		})
	}
}

func TestNew_ForeignAnswer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, key, answer string }{
		{"other arn", "arn:aws:kms:us-east-1:111122223333:key/k", "arn:aws:kms:us-east-1:111122223333:key/x"},
		{"other key id", "k", "arn:aws:kms:us-east-1:111122223333:key/xk"},
		{"key id not arn", "k", "k"},
		{"alias arn answer", "alias/a", "arn:aws:kms:us-east-1:111122223333:alias/a"},
		{"bad arn answer", "alias/a", "arn:aws:kms:us-east-1:111122223333:key/"},
		{"empty", "k", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", "ECC_NIST_P256")
			f.alias("alias/a", "arn:aws:kms:us-east-1:111122223333:key/k")
			der, err := x509.MarshalPKIXPublicKey(genKey(t, "ECC_NIST_P256").Public())
			if err != nil {
				t.Fatal(err)
			}
			f.set(func(f *fakeKMS) {
				f.hook = func(w http.ResponseWriter, _ *http.Request, _ string) bool {
					reply(t, w, http.StatusOK, map[string]any{
						"KeyId": tc.answer, "KeySpec": "ECC_NIST_P256", "KeyUsage": "SIGN_VERIFY",
						"PublicKey": b64(der), "SigningAlgorithms": []string{"ECDSA_SHA_256"},
					})
					return true
				}
			})
			if _, err := aws.New(t.Context(), fakeConfig(addr, tc.key)); err == nil || !strings.Contains(err.Error(), "answer names key") {
				t.Fatalf("New error = %v, want the foreign answer rejected", err)
			}
		})
	}
}

func TestNew_KeyNotFoundOrDenied(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	if _, err := aws.New(t.Context(), fakeConfig(addr, "missing")); err == nil || !strings.Contains(err.Error(), "get public key missing") || !strings.Contains(err.Error(), "NotFoundException") {
		t.Fatalf("missing key error = %v", err)
	}
	f.addKey("k", "ECC_NIST_P256")
	f.set(func(f *fakeKMS) {
		f.hook = func(w http.ResponseWriter, _ *http.Request, _ string) bool {
			f.fail(w, "AccessDeniedException", "not authorized")
			return true
		}
	})
	if _, err := aws.New(t.Context(), fakeConfig(addr, "k")); err == nil || !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Fatalf("denied error = %v", err)
	}
}

func TestSign_SignsWithCredentials(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ECC_NIST_P256")
	cfg := fakeConfig(addr, "k")
	cfg.AWS = static()
	s := newSigner(t, cfg)
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if auth := f.get().auth; !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+fakeAKID+"/") {
		t.Fatalf("Authorization = %q, want SigV4 with %s", auth, fakeAKID)
	}
}

func TestSign_Concurrent(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	k := f.addKey("k", "ECC_NIST_P256")
	s := newSigner(t, fakeConfig(addr, "k"))
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			d := digestOf(crypto.SHA256, "m"+string(rune('0'+i)))
			sig, err := s.Sign(rand.Reader, d, crypto.SHA256)
			if err == nil && !ecdsa.VerifyASN1(k.priv.Public().(*ecdsa.PublicKey), d, sig) {
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
	f, addr := newFake(t)
	f.addKey("k", "ECC_NIST_P256")
	cfg := fakeConfig(addr, "k")
	cfg.Timeout = time.Nanosecond
	f.stall(t) // a fast fake could answer before a coarse clock (Windows) sees the 1ns deadline pass
	if _, err := aws.New(t.Context(), cfg); errors.Is(err, aws.ErrInvalidConfig) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("1ns timeout: New error = %v, want a valid config that times out", err)
	}
}

func TestTimeout_BoundsEachCall(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	f.addKey("k", "ECC_NIST_P256")
	cfg := fakeConfig(addr, "k")
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
	if _, err := aws.New(t.Context(), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New error = %v, want deadline exceeded", err)
	}
}
