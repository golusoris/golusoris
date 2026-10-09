// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package azure signs with an Azure Key Vault or Managed HSM key through
// [crypto.Signer], so a key that never leaves the vault can sign OCI images
// with sign.Image.
//
// [New] authenticates with azidentity's default credential chain
// (workload identity, managed identity, environment, Azure CLI) unless
// Config.Credential is set, reads the key and pins its version, so a later
// rotation does not change [Signer.Public]. [Signer.Sign] sends the digest
// to the key version's sign operation, converts an ECDSA answer from the
// JOSE r||s form to the ASN.1 form crypto.Signer returns, and checks the
// signature against the pinned public key before it returns it.
//
// ECDSA keys sign the SHA-2 digest their curve pairs with (P-256 SHA-256,
// P-384 SHA-384, P-521 SHA-512); RSA keys sign a SHA-2 digest with PKCS #1
// v1.5 or, for [rsa.PSSOptions], PSS. Key Vault has no Ed25519 keys.
package azure

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

// DefaultTimeout bounds each request of [New] and each [Signer.Sign] call
// when Config.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// Sentinel errors; match with [errors.Is].
var (
	// ErrInvalidConfig reports an unusable [Config]. [New] checks it before
	// any I/O.
	ErrInvalidConfig = errors.New("kms/azure: invalid config")
	// ErrUnsupportedKey reports a key that cannot sign through this package:
	// disabled, without the sign operation, symmetric, or on secp256k1.
	ErrUnsupportedKey = errors.New("kms/azure: unsupported key")
	// ErrUnsupportedHash reports signer options or a digest the key cannot
	// sign with.
	ErrUnsupportedHash = errors.New("kms/azure: unsupported hash")
)

// Config selects the vault, key and credentials.
type Config struct {
	// Vault is the Key Vault or Managed HSM URL, e.g.
	// "https://<name>.vault.azure.net".
	Vault string `koanf:"vault"`
	// Key is the key name.
	Key string `koanf:"key"`
	// Version pins a key version. Empty pins the current version when [New]
	// runs, so a later rotation does not change [Signer.Public].
	Version string `koanf:"version"`
	// Timeout bounds each request of [New] and each [Signer.Sign] call,
	// including retries and token requests. Zero uses [DefaultTimeout].
	// crypto.Signer.Sign takes no context, so this is its only bound.
	Timeout time.Duration `koanf:"timeout"`
	// Credential authenticates requests. Nil uses
	// azidentity.NewDefaultAzureCredential.
	Credential azcore.TokenCredential `koanf:"-"`
	// Transport carries vault requests and, with a nil Credential, token
	// requests; set TLS roots for a private CA here. Nil uses azcore's
	// default transport.
	Transport http.RoundTripper `koanf:"-"`
}

// Signer is a [crypto.Signer] backed by one version of a Key Vault key. It
// is safe for concurrent use.
type Signer struct {
	client  *azkeys.Client
	vault   string
	name    string
	version string
	pub     crypto.PublicKey
	timeout time.Duration
}

var _ crypto.Signer = (*Signer)(nil)

// New checks cfg, reads the key and pins its version. ctx bounds New only;
// the key read is also bounded by cfg.Timeout.
func New(ctx context.Context, cfg Config) (*Signer, error) {
	cfg, host, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	var copts azcore.ClientOptions
	if cfg.Transport != nil {
		copts.Transport = &http.Client{Transport: cfg.Transport, Timeout: cfg.Timeout}
	}
	cred := cfg.Credential
	if cred == nil {
		dac, derr := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{ClientOptions: copts})
		if derr != nil {
			return nil, fmt.Errorf("kms/azure: default azure credential: %w", derr)
		}
		cred = dac
	}
	client, err := azkeys.NewClient(cfg.Vault, cred, &azkeys.ClientOptions{ClientOptions: copts})
	if err != nil {
		return nil, fmt.Errorf("kms/azure: client: %w", err)
	}
	s := &Signer{client: client, vault: host, name: cfg.Key, timeout: cfg.Timeout}
	if err := s.readKey(ctx, cfg.Version); err != nil {
		return nil, err
	}
	return s, nil
}

// readKey fetches version (empty = current), checks it can sign and pins it.
func (s *Signer) readKey(ctx context.Context, version string) error {
	rctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := s.client.GetKey(rctx, s.name, version, nil)
	if err != nil {
		return fmt.Errorf("kms/azure: get key %s: %w", s.name, err)
	}
	if resp.Key == nil {
		return fmt.Errorf("kms/azure: get key %s: no key in answer", s.name)
	}
	got, err := s.keyVersion(resp.Key.KID, version)
	if err != nil {
		return fmt.Errorf("kms/azure: get key %s: %w", s.name, err)
	}
	if serr := signable(resp.KeyBundle); serr != nil {
		return fmt.Errorf("%w: %s version %s: %w", ErrUnsupportedKey, s.name, got, serr)
	}
	pub, err := publicKey(resp.Key)
	if err != nil {
		return fmt.Errorf("%w: %s version %s: %w", ErrUnsupportedKey, s.name, got, err)
	}
	s.version, s.pub = got, pub
	return nil
}

// Public returns the public key of the pinned key version.
func (s *Signer) Public() crypto.PublicKey { return s.pub }

// Sign signs digest, the opts.HashFunc() digest of the message, with the
// pinned key version. rand is unused: the vault supplies the randomness.
// The call is bounded by Config.Timeout.
func (s *Signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	alg, err := algorithm(s.pub, digest, opts)
	if err != nil {
		return nil, err
	}
	// crypto.Signer.Sign has no context: Config.Timeout is the bound.
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	resp, err := s.client.Sign(ctx, s.name, s.version, azkeys.SignParameters{Algorithm: &alg, Value: digest}, nil)
	if err != nil {
		return nil, fmt.Errorf("kms/azure: sign: %w", err)
	}
	if _, kerr := s.keyVersion(resp.KID, s.version); kerr != nil {
		return nil, fmt.Errorf("kms/azure: sign: %w", kerr)
	}
	sig := resp.Result
	if k, ok := s.pub.(*ecdsa.PublicKey); ok {
		if sig, err = asn1Signature(k.Curve, sig); err != nil {
			return nil, fmt.Errorf("kms/azure: sign: %w", err)
		}
	}
	if err := verify(s.pub, digest, sig, opts); err != nil {
		return nil, fmt.Errorf("kms/azure: sign: %s version %s: %w", s.name, s.version, err)
	}
	return sig, nil
}

var (
	ecdsaAlgs = map[elliptic.Curve]azkeys.SignatureAlgorithm{
		elliptic.P256(): azkeys.SignatureAlgorithmES256,
		elliptic.P384(): azkeys.SignatureAlgorithmES384,
		elliptic.P521(): azkeys.SignatureAlgorithmES512,
	}
	curveHashes = map[elliptic.Curve]crypto.Hash{
		elliptic.P256(): crypto.SHA256,
		elliptic.P384(): crypto.SHA384,
		elliptic.P521(): crypto.SHA512,
	}
	pkcs1Algs = map[crypto.Hash]azkeys.SignatureAlgorithm{
		crypto.SHA256: azkeys.SignatureAlgorithmRS256,
		crypto.SHA384: azkeys.SignatureAlgorithmRS384,
		crypto.SHA512: azkeys.SignatureAlgorithmRS512,
	}
	pssAlgs = map[crypto.Hash]azkeys.SignatureAlgorithm{
		crypto.SHA256: azkeys.SignatureAlgorithmPS256,
		crypto.SHA384: azkeys.SignatureAlgorithmPS384,
		crypto.SHA512: azkeys.SignatureAlgorithmPS512,
	}
)

// algorithm maps the key and opts to a Key Vault signature algorithm, or
// rejects opts the key cannot honour.
func algorithm(pub crypto.PublicKey, digest []byte, opts crypto.SignerOpts) (azkeys.SignatureAlgorithm, error) {
	if opts == nil {
		return "", fmt.Errorf("%w: nil signer options", ErrUnsupportedHash)
	}
	h := opts.HashFunc()
	pss, isPSS := opts.(*rsa.PSSOptions)
	alg, err := keyAlgorithm(pub, h, isPSS)
	switch {
	case err != nil:
		return "", err
	case len(digest) != h.Size():
		return "", fmt.Errorf("%w: %d-byte digest for %v", ErrUnsupportedHash, len(digest), h)
	case isPSS && pss.SaltLength != rsa.PSSSaltLengthAuto && pss.SaltLength != rsa.PSSSaltLengthEqualsHash && pss.SaltLength != h.Size():
		return "", fmt.Errorf("%w: PSS salt length %d: Key Vault salts with the hash length", ErrUnsupportedHash, pss.SaltLength)
	}
	return alg, nil
}

// keyAlgorithm picks the algorithm for the key family, hash and padding:
// an ECDSA key signs only its curve's hash, without PSS.
func keyAlgorithm(pub crypto.PublicKey, h crypto.Hash, isPSS bool) (azkeys.SignatureAlgorithm, error) {
	var alg azkeys.SignatureAlgorithm
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if isPSS || curveHashes[k.Curve] != h {
			return "", fmt.Errorf("%w: %s keys sign %v digests without PSS, got %v PSS %t", ErrUnsupportedHash, k.Curve.Params().Name, curveHashes[k.Curve], h, isPSS)
		}
		alg = ecdsaAlgs[k.Curve]
	case *rsa.PublicKey:
		table := pkcs1Algs
		if isPSS {
			table = pssAlgs
		}
		alg = table[h]
	}
	if alg == "" {
		return "", fmt.Errorf("%w: %v", ErrUnsupportedHash, h)
	}
	return alg, nil
}

// asn1Signature converts the JOSE r||s signature Key Vault returns to the
// ASN.1 DER form of crypto.Signer and ecdsa.VerifyASN1.
func asn1Signature(curve elliptic.Curve, sig []byte) ([]byte, error) {
	size := (curve.Params().BitSize + 7) / 8
	if len(sig) != 2*size {
		return nil, fmt.Errorf("%d-byte ECDSA signature, want %d", len(sig), 2*size)
	}
	der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])})
	if err != nil {
		return nil, fmt.Errorf("encode ECDSA signature: %w", err)
	}
	return der, nil
}

var errBadSignature = errors.New("signature does not verify")

// verify checks the vault's signature against the pinned public key, with
// the salt length the caller asked for, so a rotated or swapped key fails
// here instead of at the verifier.
func verify(pub crypto.PublicKey, digest, sig []byte, opts crypto.SignerOpts) error {
	ok := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, digest, sig)
	case *rsa.PublicKey:
		if pss, isPSS := opts.(*rsa.PSSOptions); isPSS {
			ok = rsa.VerifyPSS(k, opts.HashFunc(), digest, sig, &rsa.PSSOptions{SaltLength: pss.SaltLength}) == nil
		} else {
			ok = rsa.VerifyPKCS1v15(k, opts.HashFunc(), digest, sig) == nil
		}
	}
	if !ok {
		return errBadSignature
	}
	return nil
}
