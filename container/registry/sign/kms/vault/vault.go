// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package vault signs with a HashiCorp Vault or OpenBao transit key through
// [crypto.Signer], so a key that never leaves the server can sign OCI images
// with sign.Image. Both servers expose the same transit HTTP API; the
// package speaks it over net/http and has no Vault SDK dependency.
//
// [New] authenticates with a token, or logs in with the workload's service
// account JWT through the Kubernetes auth method (or the JWT auth method,
// which takes the same login request), reads the key's public half and pins
// its version. [Signer.Sign] sends the digest to transit/sign, re-logs in
// once when the token has expired, and checks the signature against the
// pinned public key before it returns it.
//
// ECDSA (P-256, P-384, P-521) and RSA keys sign a SHA-2 digest, RSA with
// PKCS #1 v1.5 or, for [rsa.PSSOptions], PSS; Ed25519 keys sign the message
// itself (pure Ed25519).
package vault

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultMount is the transit secrets engine path when Config.Mount is
	// empty.
	DefaultMount = "transit"
	// DefaultAuthMount is the Kubernetes auth method path when
	// Config.AuthMount is empty.
	DefaultAuthMount = "kubernetes"
	// DefaultJWTFile is the projected service account token kubelet mounts
	// into every pod.
	DefaultJWTFile = "/var/run/secrets/kubernetes.io/serviceaccount/token" // #nosec G101 -- a file path, not a credential.
	// DefaultTimeout bounds each request of [New] and each [Signer.Sign]
	// call when Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
)

// Sentinel errors; match with [errors.Is].
var (
	// ErrInvalidConfig reports an unusable [Config]. [New] checks it before
	// any I/O.
	ErrInvalidConfig = errors.New("kms/vault: invalid config")
	// ErrUnsupportedKey reports a transit key that cannot sign through this
	// package: symmetric, derived, or of an unknown type.
	ErrUnsupportedKey = errors.New("kms/vault: unsupported key")
	// ErrUnsupportedHash reports signer options or a digest the key cannot
	// sign with.
	ErrUnsupportedHash = errors.New("kms/vault: unsupported hash")
)

// Config selects the server, key and credentials. Exactly one of Token and
// Role is set.
type Config struct {
	// Address is the server URL, e.g. "https://vault.vault.svc:8200".
	Address string `koanf:"address"`
	// Mount is the transit secrets engine path. Empty uses [DefaultMount].
	Mount string `koanf:"mount"`
	// Key is the transit key name.
	Key string `koanf:"key"`
	// KeyVersion pins a key version. Zero pins the latest version when [New]
	// runs, so a later rotation does not change [Signer.Public].
	KeyVersion int `koanf:"key_version"`
	// Token authenticates every request (token auth).
	Token string `koanf:"token"`
	// Role logs in with the service account JWT in JWTFile through the auth
	// method at AuthMount. The login is repeated once when a request is
	// denied, which covers token expiry and revocation.
	Role string `koanf:"role"`
	// AuthMount is the auth method path: the Kubernetes auth method by
	// default ([DefaultAuthMount]), or a JWT auth method such as "jwt" that
	// validates service account tokens. Only with Role.
	AuthMount string `koanf:"auth_mount"`
	// JWTFile holds the service account JWT, re-read on every login because
	// kubelet rotates it. Empty uses [DefaultJWTFile]. Only with Role.
	JWTFile string `koanf:"jwt_file"`
	// Timeout bounds each request of [New] and each [Signer.Sign] call,
	// including a re-login. Zero uses [DefaultTimeout]. crypto.Signer.Sign
	// takes no context, so this is its only bound.
	Timeout time.Duration `koanf:"timeout"`
	// Transport carries the requests; set TLS roots for a private CA here.
	// Nil uses a private clone of [http.DefaultTransport].
	Transport http.RoundTripper `koanf:"-"`
}

// Signer is a [crypto.Signer] backed by one version of a transit key. It is
// safe for concurrent use.
type Signer struct {
	c       *client
	path    string
	version int
	pub     crypto.PublicKey
	timeout time.Duration
}

var _ crypto.Signer = (*Signer)(nil)

// New checks cfg, logs in when cfg.Role is set, and reads the public key of
// the pinned key version. ctx bounds New only; each request is also bounded
// by cfg.Timeout.
func New(ctx context.Context, cfg Config) (*Signer, error) {
	cfg, base, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	c := newClient(base, cfg)
	if cfg.Role != "" {
		if lerr := c.login(ctx); lerr != nil {
			return nil, lerr
		}
	}
	k, err := c.readKey(ctx, cfg.Mount, cfg.Key, cfg.KeyVersion)
	if err != nil {
		return nil, err
	}
	return &Signer{
		c:       c,
		path:    cfg.Mount + "/sign/" + cfg.Key,
		version: k.version,
		pub:     k.pub,
		timeout: cfg.Timeout,
	}, nil
}

// Public returns the public key of the pinned key version.
func (s *Signer) Public() crypto.PublicKey { return s.pub }

// Sign signs digest with the pinned key version. For ECDSA and RSA keys
// digest is the opts.HashFunc() digest of the message; for Ed25519 keys it
// is the message and opts.HashFunc() must be zero. rand is unused: the
// server supplies the randomness. The call is bounded by Config.Timeout.
func (s *Signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	path, body, err := s.request(digest, opts)
	if err != nil {
		return nil, err
	}
	// crypto.Signer.Sign has no context: Config.Timeout is the bound.
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	var out struct {
		Signature  string `json:"signature"`
		KeyVersion int    `json:"key_version"`
	}
	if werr := s.c.write(ctx, path, body, &out); werr != nil {
		return nil, fmt.Errorf("kms/vault: sign: %w", werr)
	}
	sig, err := s.decode(out.Signature, out.KeyVersion)
	if err != nil {
		return nil, err
	}
	if err := verify(s.pub, digest, sig, opts); err != nil {
		return nil, fmt.Errorf("kms/vault: sign: key version %d: %w", s.version, err)
	}
	return sig, nil
}

// request builds the transit/sign path and body for digest and opts.
func (s *Signer) request(digest []byte, opts crypto.SignerOpts) (string, map[string]any, error) {
	if opts == nil {
		return "", nil, fmt.Errorf("%w: nil signer options", ErrUnsupportedHash)
	}
	h := opts.HashFunc()
	body := map[string]any{
		"input":       base64.StdEncoding.EncodeToString(digest),
		"key_version": s.version,
	}
	if _, ok := s.pub.(ed25519.PublicKey); ok {
		if h != crypto.Hash(0) {
			return "", nil, fmt.Errorf("%w: Ed25519 signs the message, got %v", ErrUnsupportedHash, h)
		}
		body["prehashed"] = false
		return s.path, body, nil
	}
	name, ok := hashNames[h]
	if !ok {
		return "", nil, fmt.Errorf("%w: %v", ErrUnsupportedHash, h)
	}
	if len(digest) != h.Size() {
		return "", nil, fmt.Errorf("%w: %d-byte digest for %v", ErrUnsupportedHash, len(digest), h)
	}
	body["prehashed"] = true
	if _, ok := s.pub.(*rsa.PublicKey); ok {
		body["signature_algorithm"] = "pkcs1v15"
		if pss, ok := opts.(*rsa.PSSOptions); ok {
			body["signature_algorithm"] = "pss"
			body["salt_length"] = saltLength(pss.SaltLength)
		}
	}
	return s.path + "/" + name, body, nil
}

// decode parses "vault:v<version>:<base64>" and checks the version.
func (s *Signer) decode(sig string, version int) ([]byte, error) {
	prefix := "vault:v" + strconv.Itoa(s.version) + ":"
	raw, ok := strings.CutPrefix(sig, prefix)
	if !ok || version != s.version {
		return nil, fmt.Errorf("kms/vault: sign: signature %.16q, key version %d: want version %d", sig, version, s.version)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("kms/vault: sign: decode signature: %w", err)
	}
	return b, nil
}

var hashNames = map[crypto.Hash]string{
	crypto.SHA224: "sha2-224",
	crypto.SHA256: "sha2-256",
	crypto.SHA384: "sha2-384",
	crypto.SHA512: "sha2-512",
}

func saltLength(n int) string {
	switch n {
	case rsa.PSSSaltLengthAuto:
		return "auto"
	case rsa.PSSSaltLengthEqualsHash:
		return "hash"
	default:
		return strconv.Itoa(n)
	}
}

var errBadSignature = errors.New("signature does not verify")

// verify checks the server's signature against the pinned public key, so a
// rotated or swapped key fails here instead of at the verifier.
func verify(pub crypto.PublicKey, digest, sig []byte, opts crypto.SignerOpts) error {
	ok := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, digest, sig)
	case ed25519.PublicKey:
		ok = ed25519.Verify(k, digest, sig)
	case *rsa.PublicKey:
		if _, pss := opts.(*rsa.PSSOptions); pss {
			ok = rsa.VerifyPSS(k, opts.HashFunc(), digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}) == nil
		} else {
			ok = rsa.VerifyPKCS1v15(k, opts.HashFunc(), digest, sig) == nil
		}
	}
	if !ok {
		return errBadSignature
	}
	return nil
}

func normalize(cfg Config) (Config, *url.URL, error) {
	if cfg.Mount == "" {
		cfg.Mount = DefaultMount
	}
	if cfg.Role != "" {
		if cfg.AuthMount == "" {
			cfg.AuthMount = DefaultAuthMount
		}
		if cfg.JWTFile == "" {
			cfg.JWTFile = DefaultJWTFile
		}
	}
	base, err := parseAddress(cfg.Address)
	if err != nil {
		return cfg, nil, err
	}
	if err := checkConfig(cfg); err != nil {
		return cfg, nil, err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	return cfg, base, nil
}

func parseAddress(addr string) (*url.URL, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: address: %w", ErrInvalidConfig, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: address %q: want http(s)://host[:port][/path]", ErrInvalidConfig, addr)
	}
	return u, nil
}

func checkConfig(cfg Config) error {
	switch {
	case (cfg.Token == "") == (cfg.Role == ""):
		return fmt.Errorf("%w: set exactly one of token and role", ErrInvalidConfig)
	case cfg.Role == "" && (cfg.AuthMount != "" || cfg.JWTFile != ""):
		return fmt.Errorf("%w: auth mount and JWT file need a role", ErrInvalidConfig)
	case cfg.Role != "" && !validMount(cfg.AuthMount):
		return fmt.Errorf("%w: auth mount %q", ErrInvalidConfig, cfg.AuthMount)
	}
	return checkKey(cfg)
}

func checkKey(cfg Config) error {
	switch {
	case cfg.KeyVersion < 0:
		return fmt.Errorf("%w: key version %d", ErrInvalidConfig, cfg.KeyVersion)
	case cfg.Timeout < 0:
		return fmt.Errorf("%w: timeout %v", ErrInvalidConfig, cfg.Timeout)
	case !validName(cfg.Key):
		return fmt.Errorf("%w: key name %q", ErrInvalidConfig, cfg.Key)
	case !validMount(cfg.Mount):
		return fmt.Errorf("%w: mount %q", ErrInvalidConfig, cfg.Mount)
	}
	return nil
}

// validName mirrors Vault's key and role name pattern \w(([\w-.@]+)?\w)?.
func validName(s string) bool {
	if s == "" || !isWord(s[0]) || !isWord(s[len(s)-1]) {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !isWord(c) && c != '-' && c != '.' && c != '@' {
			return false
		}
	}
	return true
}

// validMount accepts slash-separated names; no "." or ".." segment can
// pass validName, so no path escapes /v1/.
func validMount(s string) bool {
	for seg := range strings.SplitSeq(s, "/") {
		if !validName(seg) {
			return false
		}
	}
	return true
}

func isWord(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
