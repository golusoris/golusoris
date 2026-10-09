// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

const (
	fakeAccount = "111122223333"
	fakeAKID    = "AKIDFAKE"
)

// fakeKey is one KMS key with its private half.
type fakeKey struct {
	arn   string
	spec  string
	usage string
	algs  []string
	priv  crypto.Signer
	// der overrides the public key GetPublicKey answers with.
	der []byte
}

// fakeKMS serves GetPublicKey and Sign of the KMS JSON protocol with real
// keys, so signatures verify.
type fakeKMS struct {
	t *testing.T

	mu       sync.Mutex
	keys     map[string]*fakeKey
	aliases  map[string]string
	requests int
	signs    int
	signBody map[string]any
	// auth is the Authorization header of the last request.
	auth string
	// hook answers a request itself when it returns true.
	hook func(w http.ResponseWriter, r *http.Request, op string) bool
}

func newFake(t *testing.T) (*fakeKMS, string) {
	t.Helper()
	f := &fakeKMS{t: t, keys: map[string]*fakeKey{}, aliases: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

var rsaAlgs = []string{
	"RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384", "RSASSA_PSS_SHA_512",
	"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PKCS1_V1_5_SHA_512",
}

var (
	rsa3072 = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 3072) })
	rsa4096 = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 4096) })
)

var specAlgs = map[string][]string{
	"ECC_NIST_P256":         {"ECDSA_SHA_256"},
	"ECC_NIST_P384":         {"ECDSA_SHA_384"},
	"ECC_NIST_P521":         {"ECDSA_SHA_512"},
	"ECC_NIST_EDWARDS25519": {"ED25519_SHA_512", "ED25519_PH_SHA_512"},
	"RSA_2048":              rsaAlgs,
	"RSA_3072":              rsaAlgs,
	"RSA_4096":              rsaAlgs,
}

func genKey(t *testing.T, spec string) crypto.Signer {
	t.Helper()
	var (
		k   crypto.Signer
		err error
	)
	switch spec {
	case "ECC_NIST_P256":
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ECC_NIST_P384":
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "ECC_NIST_P521":
		k, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "RSA_2048":
		k, err = rsa.GenerateKey(rand.Reader, 2048)
	case "RSA_3072":
		k, err = rsa3072()
	case "RSA_4096":
		k, err = rsa4096()
	case "ECC_NIST_EDWARDS25519":
		_, k, err = ed25519.GenerateKey(rand.Reader)
	default:
		t.Fatalf("genKey: spec %q", spec)
	}
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// addKey creates a SIGN_VERIFY key in us-east-1 and returns it.
func (f *fakeKMS) addKey(id, spec string) *fakeKey {
	f.t.Helper()
	return f.addKeyIn("us-east-1", id, spec)
}

func (f *fakeKMS) addKeyIn(region, id, spec string) *fakeKey {
	f.t.Helper()
	k := &fakeKey{
		arn:   "arn:aws:kms:" + region + ":" + fakeAccount + ":key/" + id,
		spec:  spec,
		usage: "SIGN_VERIFY",
		algs:  specAlgs[spec],
		priv:  genKey(f.t, spec),
	}
	f.mu.Lock()
	f.keys[k.arn] = k
	f.mu.Unlock()
	return k
}

func (f *fakeKMS) alias(name, arn string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aliases[name] = arn
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
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("fake: write answer: %v", err)
	}
}

// raw answers with a verbatim body.
func raw(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("fake: write answer: %v", err)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// fail answers with a KMS client error.
func (f *fakeKMS) fail(w http.ResponseWriter, typ, msg string) {
	w.Header().Set("X-Amzn-Errortype", typ)
	reply(f.t, w, http.StatusBadRequest, map[string]any{"__type": typ, "message": msg})
}

func (f *fakeKMS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	f.auth = r.Header.Get("Authorization")
	op, _ := strings.CutPrefix(r.Header.Get("X-Amz-Target"), "TrentService.")
	if f.hook != nil && f.hook(w, r, op) {
		return
	}
	var body map[string]any
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-amz-json-1.1" || json.NewDecoder(r.Body).Decode(&body) != nil {
		f.fail(w, "SerializationException", "want a KMS JSON request")
		return
	}
	k := f.resolve(body["KeyId"])
	if k == nil {
		f.fail(w, "NotFoundException", "key not found")
		return
	}
	switch op {
	case "GetPublicKey":
		f.publicKey(w, k)
	case "Sign":
		f.sign(w, k, body)
	default:
		f.fail(w, "UnknownOperationException", op)
	}
}

// resolve finds a key by ARN, key ID, alias name or alias ARN.
func (f *fakeKMS) resolve(id any) *fakeKey {
	s, _ := id.(string)
	if k, ok := f.keys[s]; ok {
		return k
	}
	if _, name, ok := strings.Cut(s, ":alias/"); ok {
		s = "alias/" + name
	}
	if arn, ok := f.aliases[s]; ok {
		return f.keys[arn]
	}
	for arn, k := range f.keys {
		if strings.HasSuffix(arn, ":key/"+s) {
			return k
		}
	}
	return nil
}

func (f *fakeKMS) publicKey(w http.ResponseWriter, k *fakeKey) {
	der := k.der
	if der == nil {
		var err error
		if der, err = x509.MarshalPKIXPublicKey(k.priv.Public()); err != nil {
			f.t.Fatal(err)
		}
	}
	reply(f.t, w, http.StatusOK, map[string]any{
		"KeyId": k.arn, "KeySpec": k.spec, "KeyUsage": k.usage,
		"PublicKey": b64(der), "SigningAlgorithms": k.algs,
	})
}

var fakeHashes = map[string]crypto.Hash{"256": crypto.SHA256, "384": crypto.SHA384, "512": crypto.SHA512}

func (f *fakeKMS) sign(w http.ResponseWriter, k *fakeKey, body map[string]any) {
	f.signs++
	f.signBody = body
	alg, _ := body["SigningAlgorithm"].(string)
	msg, err := base64.StdEncoding.DecodeString(body["Message"].(string))
	if err != nil || !slices.Contains(k.algs, alg) {
		f.fail(w, "InvalidKeyUsageException", "bad message or algorithm "+alg)
		return
	}
	sig, err := fakeSign(k.priv, msg, alg, body["MessageType"])
	if err != nil {
		f.fail(w, "ValidationException", err.Error())
		return
	}
	reply(f.t, w, http.StatusOK, map[string]any{"KeyId": k.arn, "Signature": b64(sig), "SigningAlgorithm": alg})
}

// fakeSign signs as KMS does: RAW messages are hashed first, DIGEST
// messages must have the hash length.
func fakeSign(priv crypto.Signer, msg []byte, alg string, msgType any) ([]byte, error) {
	if alg == "ED25519_SHA_512" {
		if msgType != "RAW" {
			return nil, errMessageType
		}
		return priv.Sign(rand.Reader, msg, crypto.Hash(0))
	}
	h := fakeHashes[alg[len(alg)-3:]]
	if msgType == "RAW" {
		d := h.New()
		d.Write(msg)
		msg = d.Sum(nil)
	}
	if len(msg) != h.Size() {
		return nil, errDigestLength
	}
	var opts crypto.SignerOpts = h
	if strings.HasPrefix(alg, "RSASSA_PSS_") {
		opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
	}
	return priv.Sign(rand.Reader, msg, opts)
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

const (
	errMessageType  = fakeError("ED25519_SHA_512 requires MessageType RAW")
	errDigestLength = fakeError("digest length does not match the signing algorithm")
)

// anonymous is an SDK config that sends unsigned requests to the fake and
// does not retry.
func anonymous() *awssdk.Config {
	return &awssdk.Config{
		Region:      "us-east-1",
		Credentials: awssdk.AnonymousCredentials{},
		Retryer:     func() awssdk.Retryer { return awssdk.NopRetryer{} },
	}
}

// static is an SDK config with the fake's access key, so requests are
// SigV4-signed.
func static() *awssdk.Config {
	cfg := anonymous()
	cfg.Credentials = awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) {
		return awssdk.Credentials{AccessKeyID: fakeAKID, SecretAccessKey: "secret", Source: "test"}, nil
	})
	return cfg
}

// scope returns the "<region>/kms" part of a SigV4 Authorization header.
func scope(auth string) string {
	_, cred, _ := strings.Cut(auth, "Credential=")
	parts := strings.Split(cred, "/")
	if len(parts) < 4 {
		return ""
	}
	return parts[2] + "/" + parts[3]
}
