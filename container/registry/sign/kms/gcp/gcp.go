// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gcp signs with a Google Cloud KMS asymmetric key version through
// [crypto.Signer], so a key that never leaves Cloud KMS can sign OCI images
// with sign.Image. It speaks the Cloud KMS REST API over net/http and
// authorizes with golang.org/x/oauth2; there is no Cloud KMS SDK
// dependency.
//
// [New] finds Application Default Credentials (GKE Workload Identity
// through the metadata server, workload identity federation and service
// account files, gcloud user credentials) unless Config.TokenSource is set,
// reads the public key of the configured key version and checks its
// algorithm. [Signer.Sign] sends the digest with its CRC32C checksum to
// asymmetricSign, checks the checksums and the key version Cloud KMS
// answers with, and checks the signature against the public key before it
// returns it.
//
// A key version has one algorithm, so the signer options must match it:
// ECDSA P-256 and P-384 with their SHA-2 digest, RSA PKCS #1 v1.5 or PSS
// ([rsa.PSSOptions]) with the algorithm's digest, and Ed25519 over the
// message itself (pure Ed25519).
package gcp

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// DefaultEndpoint is the Cloud KMS API when Config.Endpoint is empty.
	DefaultEndpoint = "https://cloudkms.googleapis.com"
	// Scope is the OAuth2 scope requested from Application Default
	// Credentials.
	Scope = "https://www.googleapis.com/auth/cloudkms"
	// DefaultTimeout bounds each request of [New] and [Signer.Sign] when
	// Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
)

// Sentinel errors; match with [errors.Is].
var (
	// ErrInvalidConfig reports an unusable [Config]. [New] checks it before
	// any I/O.
	ErrInvalidConfig = errors.New("kms/gcp: invalid config")
	// ErrUnsupportedKey reports a key version whose algorithm cannot sign
	// through this package: raw PKCS #1, secp256k1, post-quantum, or
	// unknown.
	ErrUnsupportedKey = errors.New("kms/gcp: unsupported key")
	// ErrUnsupportedHash reports signer options or a digest the key version
	// cannot sign with.
	ErrUnsupportedHash = errors.New("kms/gcp: unsupported hash")
	// ErrChecksum reports a CRC32C checksum Cloud KMS did not verify or
	// that does not match its answer: the request or answer was corrupted.
	ErrChecksum = errors.New("kms/gcp: checksum mismatch")
)

// Config selects the key version, endpoint and credentials.
type Config struct {
	// Key is the full key version name: "projects/<p>/locations/<l>/
	// keyRings/<r>/cryptoKeys/<k>/cryptoKeyVersions/<v>".
	Key string `koanf:"key"`
	// Endpoint is the Cloud KMS API base URL, e.g. a regional or Private
	// Service Connect endpoint. Empty uses [DefaultEndpoint].
	Endpoint string `koanf:"endpoint"`
	// Timeout bounds each request of [New] and of [Signer.Sign], token
	// requests included. Zero uses [DefaultTimeout].
	Timeout time.Duration `koanf:"timeout"`
	// TokenSource authorizes requests; it is wrapped in a reusing source.
	// Nil finds Application Default Credentials with [Scope].
	TokenSource oauth2.TokenSource `koanf:"-"`
	// Transport carries KMS and token requests. Nil uses a private clone of
	// [http.DefaultTransport].
	Transport http.RoundTripper `koanf:"-"`
}

// algorithm is what a CryptoKeyVersionAlgorithm needs from the signer.
type algorithm struct {
	hash  crypto.Hash // zero: pure Ed25519 over the message
	pss   bool
	curve elliptic.Curve
	bits  int
}

// algorithms are the Cloud KMS signing algorithms Go can verify.
var algorithms = map[string]algorithm{
	"EC_SIGN_P256_SHA256":        {hash: crypto.SHA256, curve: elliptic.P256()},
	"EC_SIGN_P384_SHA384":        {hash: crypto.SHA384, curve: elliptic.P384()},
	"EC_SIGN_ED25519":            {},
	"RSA_SIGN_PSS_2048_SHA256":   {hash: crypto.SHA256, pss: true, bits: 2048},
	"RSA_SIGN_PSS_3072_SHA256":   {hash: crypto.SHA256, pss: true, bits: 3072},
	"RSA_SIGN_PSS_4096_SHA256":   {hash: crypto.SHA256, pss: true, bits: 4096},
	"RSA_SIGN_PSS_4096_SHA512":   {hash: crypto.SHA512, pss: true, bits: 4096},
	"RSA_SIGN_PKCS1_2048_SHA256": {hash: crypto.SHA256, bits: 2048},
	"RSA_SIGN_PKCS1_3072_SHA256": {hash: crypto.SHA256, bits: 3072},
	"RSA_SIGN_PKCS1_4096_SHA256": {hash: crypto.SHA256, bits: 4096},
	"RSA_SIGN_PKCS1_4096_SHA512": {hash: crypto.SHA512, bits: 4096},
}

// digestFields names the asymmetricSign digest field per hash.
var digestFields = map[crypto.Hash]string{
	crypto.SHA256: "sha256",
	crypto.SHA384: "sha384",
	crypto.SHA512: "sha512",
}

// Signer is a [crypto.Signer] backed by one Cloud KMS key version. It is
// safe for concurrent use.
type Signer struct {
	c       *client
	key     string
	alg     algorithm
	pub     crypto.PublicKey
	timeout time.Duration
}

var _ crypto.Signer = (*Signer)(nil)

// New checks cfg, finds credentials and reads the public key of the key
// version. ctx bounds New only; each request is also bounded by
// cfg.Timeout.
func New(ctx context.Context, cfg Config) (*Signer, error) {
	cfg, base, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	rt := cfg.Transport
	if rt == nil {
		rt = http.DefaultTransport.(*http.Transport).Clone()
	}
	hc := &http.Client{Transport: rt, Timeout: cfg.Timeout}
	c := &client{base: base, http: hc, token: tokens(cfg.TokenSource, hc), timeout: cfg.Timeout}
	name, pub, err := c.publicKey(ctx, cfg.Key)
	if err != nil {
		return nil, err
	}
	return &Signer{c: c, key: cfg.Key, alg: algorithms[name], pub: pub, timeout: cfg.Timeout}, nil
}

// tokens returns the token function of a request: ts wrapped in a reusing
// source, or Application Default Credentials found under the request's
// context.
func tokens(ts oauth2.TokenSource, hc *http.Client) func(context.Context) (*oauth2.Token, error) {
	if ts != nil {
		reuse := oauth2.ReuseTokenSource(nil, ts)
		return func(context.Context) (*oauth2.Token, error) { return reuse.Token() }
	}
	return (&adcTokens{http: hc}).token
}

// adcTokens caches one Application Default Credentials token. A refresh
// discovers the credentials again under the caller's context, because the
// oauth2 token sources keep the context they are created with.
type adcTokens struct {
	http *http.Client
	mu   sync.Mutex
	tok  *oauth2.Token
}

func (a *adcTokens) token(ctx context.Context) (*oauth2.Token, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok.Valid() {
		return a.tok, nil
	}
	creds, err := google.FindDefaultCredentials(context.WithValue(ctx, oauth2.HTTPClient, a.http), Scope)
	if err != nil {
		return nil, fmt.Errorf("application default credentials: %w", err)
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("application default credentials: %w", err)
	}
	a.tok = tok
	return tok, nil
}

// Public returns the public key of the key version.
func (s *Signer) Public() crypto.PublicKey { return s.pub }

// Sign signs digest with the key version. For ECDSA and RSA keys digest is
// the opts.HashFunc() digest of the message; for Ed25519 keys it is the
// message and opts.HashFunc() must be zero. rand is unused: Cloud KMS
// supplies the randomness. The call, an Application Default Credentials
// token refresh included, is bounded by Config.Timeout.
func (s *Signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	body, err := s.request(digest, opts)
	if err != nil {
		return nil, err
	}
	// crypto.Signer.Sign has no context: Config.Timeout is the bound.
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	sig, err := s.c.sign(ctx, s.key, body, s.alg.hash == crypto.Hash(0))
	if err != nil {
		return nil, err
	}
	if err := s.verify(digest, sig, opts); err != nil {
		return nil, fmt.Errorf("kms/gcp: sign: %s: %w", s.key, err)
	}
	return sig, nil
}

// request builds the asymmetricSign body for digest, or rejects opts the
// key version cannot honour.
func (s *Signer) request(digest []byte, opts crypto.SignerOpts) (map[string]any, error) {
	if opts == nil {
		return nil, fmt.Errorf("%w: nil signer options", ErrUnsupportedHash)
	}
	if err := s.alg.accepts(opts); err != nil {
		return nil, err
	}
	sum := checksum(digest)
	if s.alg.hash == crypto.Hash(0) {
		return map[string]any{"data": base64.StdEncoding.EncodeToString(digest), "dataCrc32c": sum}, nil
	}
	if len(digest) != s.alg.hash.Size() {
		return nil, fmt.Errorf("%w: %d-byte digest for %v", ErrUnsupportedHash, len(digest), s.alg.hash)
	}
	return map[string]any{
		"digest":       map[string]string{digestFields[s.alg.hash]: base64.StdEncoding.EncodeToString(digest)},
		"digestCrc32c": sum,
	}, nil
}

// accepts reports whether opts ask for exactly this algorithm.
func (a algorithm) accepts(opts crypto.SignerOpts) error {
	h := opts.HashFunc()
	pss, isPSS := opts.(*rsa.PSSOptions)
	switch {
	case h != a.hash:
		return fmt.Errorf("%w: key version signs %v digests, got %v", ErrUnsupportedHash, a.hash, h)
	case isPSS != a.pss:
		return fmt.Errorf("%w: key version PSS %t, options PSS %t", ErrUnsupportedHash, a.pss, isPSS)
	case isPSS && pss.SaltLength != rsa.PSSSaltLengthAuto && pss.SaltLength != rsa.PSSSaltLengthEqualsHash && pss.SaltLength != h.Size():
		return fmt.Errorf("%w: PSS salt length %d: Cloud KMS salts with the hash length", ErrUnsupportedHash, pss.SaltLength)
	}
	if o, ok := opts.(*ed25519.Options); ok && o.Context != "" {
		return fmt.Errorf("%w: Ed25519ctx", ErrUnsupportedHash)
	}
	return nil
}

var errBadSignature = errors.New("signature does not verify")

// verify checks the signature against the public key, with the salt length
// the caller asked for, so a swapped key fails here instead of at the
// verifier.
func (s *Signer) verify(digest, sig []byte, opts crypto.SignerOpts) error {
	ok := false
	switch k := s.pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, digest, sig)
	case ed25519.PublicKey:
		ok = ed25519.Verify(k, digest, sig)
	case *rsa.PublicKey:
		if s.alg.pss {
			ok = rsa.VerifyPSS(k, s.alg.hash, digest, sig, &rsa.PSSOptions{SaltLength: opts.(*rsa.PSSOptions).SaltLength}) == nil
		} else {
			ok = rsa.VerifyPKCS1v15(k, s.alg.hash, digest, sig) == nil
		}
	}
	if !ok {
		return errBadSignature
	}
	return nil
}
