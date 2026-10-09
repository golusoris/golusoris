// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package aws signs with an AWS KMS asymmetric key through [crypto.Signer],
// so a key that never leaves KMS can sign OCI images with sign.Image.
//
// [New] loads credentials through the AWS default chain (environment,
// shared files, IRSA web identity, EKS Pod Identity, IMDS) unless
// Config.AWS carries a loaded configuration, reads the key's public half
// with GetPublicKey and pins the key ARN, so an alias moved later does not
// change [Signer.Public]. [Signer.Sign] sends the digest with MessageType
// DIGEST and checks the signature against the pinned public key before it
// returns it.
//
// ECDSA (P-256, P-384, P-521) and RSA keys sign a SHA-2 digest, RSA with
// PKCS #1 v1.5 or, for [rsa.PSSOptions], PSS; Ed25519 keys sign the message
// itself (pure Ed25519, at most 4096 bytes).
package aws

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

const (
	// DefaultTimeout bounds each step of [New] and each [Signer.Sign] call
	// when Config.Timeout is zero.
	DefaultTimeout = 30 * time.Second
	// maxRawMessage is the largest message KMS signs with MessageType RAW.
	maxRawMessage = 4096
)

// Sentinel errors; match with [errors.Is].
var (
	// ErrInvalidConfig reports an unusable [Config]. [New] checks it before
	// any I/O.
	ErrInvalidConfig = errors.New("kms/aws: invalid config")
	// ErrUnsupportedKey reports a KMS key that cannot sign through this
	// package: not SIGN_VERIFY, or of a key spec Go cannot verify.
	ErrUnsupportedKey = errors.New("kms/aws: unsupported key")
	// ErrUnsupportedHash reports signer options or a digest the key cannot
	// sign with.
	ErrUnsupportedHash = errors.New("kms/aws: unsupported hash")
)

// Config selects the key, region and credentials.
type Config struct {
	// Key is the key ID, key ARN, alias name ("alias/<name>") or alias ARN
	// of an asymmetric SIGN_VERIFY key.
	Key string `koanf:"key"`
	// Region is the KMS region. Empty uses the region of a key or alias
	// ARN, else the default chain's region (AWS_REGION, shared config).
	Region string `koanf:"region"`
	// Endpoint overrides the KMS endpoint URL, e.g. a VPC interface
	// endpoint. Empty resolves the regional endpoint.
	Endpoint string `koanf:"endpoint"`
	// Timeout bounds each step of [New] and each [Signer.Sign] call,
	// including retries and credential refresh. Zero uses [DefaultTimeout].
	// crypto.Signer.Sign takes no context, so this is its only bound.
	Timeout time.Duration `koanf:"timeout"`
	// AWS is a loaded SDK configuration (credentials, retryer, HTTP
	// client). Nil loads the default chain with config.LoadDefaultConfig in
	// [New].
	AWS *aws.Config `koanf:"-"`
}

// Signer is a [crypto.Signer] backed by one KMS key. It is safe for
// concurrent use.
type Signer struct {
	client  *kms.Client
	arn     string
	pub     crypto.PublicKey
	algs    []types.SigningAlgorithmSpec
	timeout time.Duration
}

var _ crypto.Signer = (*Signer)(nil)

// New checks cfg, loads the SDK configuration and reads the key's public
// half. ctx bounds New only; each step is also bounded by cfg.Timeout.
func New(ctx context.Context, cfg Config) (*Signer, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	sdk, err := loadSDK(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client := kms.NewFromConfig(sdk, func(o *kms.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return readKey(ctx, client, cfg)
}

// loadSDK copies cfg.AWS or loads the default chain, and sets the region.
func loadSDK(ctx context.Context, cfg Config) (aws.Config, error) {
	var sdk aws.Config
	if cfg.AWS != nil {
		sdk = cfg.AWS.Copy()
	} else {
		var opts []func(*config.LoadOptions) error
		if cfg.Region != "" {
			// Web identity and SSO providers build their STS client while loading.
			opts = append(opts, config.WithRegion(cfg.Region))
		}
		lctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
		loaded, err := config.LoadDefaultConfig(lctx, opts...)
		if err != nil {
			return aws.Config{}, fmt.Errorf("kms/aws: load AWS config: %w", err)
		}
		sdk = loaded
	}
	if cfg.Region != "" {
		sdk.Region = cfg.Region
	}
	if sdk.Region == "" {
		return aws.Config{}, fmt.Errorf("%w: no region: set Region, use a key ARN or configure AWS_REGION", ErrInvalidConfig)
	}
	return sdk, nil
}

// readKey fetches the public key and pins the key ARN KMS answers with.
func readKey(ctx context.Context, client *kms.Client, cfg Config) (*Signer, error) {
	rctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	out, err := client.GetPublicKey(rctx, &kms.GetPublicKeyInput{KeyId: aws.String(cfg.Key)})
	if err != nil {
		return nil, fmt.Errorf("kms/aws: get public key %s: %w", cfg.Key, err)
	}
	arn := aws.ToString(out.KeyId)
	if !answerMatchesKey(cfg.Key, arn) {
		return nil, fmt.Errorf("kms/aws: get public key %s: answer names key %q", cfg.Key, arn)
	}
	if out.KeyUsage != types.KeyUsageTypeSignVerify || len(out.SigningAlgorithms) == 0 {
		return nil, fmt.Errorf("%w: %s has key usage %q and signing algorithms %v", ErrUnsupportedKey, arn, out.KeyUsage, out.SigningAlgorithms)
	}
	pub, err := parsePublicKey(out.KeySpec, out.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnsupportedKey, arn, err)
	}
	return &Signer{client: client, arn: arn, pub: pub, algs: out.SigningAlgorithms, timeout: cfg.Timeout}, nil
}

// Public returns the public key of the pinned key.
func (s *Signer) Public() crypto.PublicKey { return s.pub }

// Sign signs digest with the pinned key. For ECDSA and RSA keys digest is
// the opts.HashFunc() digest of the message; for Ed25519 keys it is the
// message and opts.HashFunc() must be zero. rand is unused: KMS supplies
// the randomness. The call is bounded by Config.Timeout.
func (s *Signer) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	in, err := s.input(digest, opts)
	if err != nil {
		return nil, err
	}
	// crypto.Signer.Sign has no context: Config.Timeout is the bound.
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	out, err := s.client.Sign(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("kms/aws: sign: %w", err)
	}
	if got := aws.ToString(out.KeyId); got != s.arn || out.SigningAlgorithm != in.SigningAlgorithm {
		return nil, fmt.Errorf("kms/aws: sign: answer from key %q with %q, want %s with %s", got, out.SigningAlgorithm, s.arn, in.SigningAlgorithm)
	}
	if err := verify(s.pub, digest, out.Signature, opts); err != nil {
		return nil, fmt.Errorf("kms/aws: sign: key %s: %w", s.arn, err)
	}
	return out.Signature, nil
}

// input builds the Sign request for digest and opts, or rejects opts the
// key cannot honour.
func (s *Signer) input(digest []byte, opts crypto.SignerOpts) (*kms.SignInput, error) {
	if opts == nil {
		return nil, fmt.Errorf("%w: nil signer options", ErrUnsupportedHash)
	}
	alg, mt, err := algorithm(s.pub, digest, opts)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(s.algs, alg) {
		return nil, fmt.Errorf("%w: key %s signs with %v, not %s", ErrUnsupportedHash, s.arn, s.algs, alg)
	}
	return &kms.SignInput{KeyId: aws.String(s.arn), Message: digest, MessageType: mt, SigningAlgorithm: alg}, nil
}

var (
	ecdsaAlgs = map[crypto.Hash]types.SigningAlgorithmSpec{
		crypto.SHA256: types.SigningAlgorithmSpecEcdsaSha256,
		crypto.SHA384: types.SigningAlgorithmSpecEcdsaSha384,
		crypto.SHA512: types.SigningAlgorithmSpecEcdsaSha512,
	}
	pkcs1Algs = map[crypto.Hash]types.SigningAlgorithmSpec{
		crypto.SHA256: types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
		crypto.SHA384: types.SigningAlgorithmSpecRsassaPkcs1V15Sha384,
		crypto.SHA512: types.SigningAlgorithmSpecRsassaPkcs1V15Sha512,
	}
	pssAlgs = map[crypto.Hash]types.SigningAlgorithmSpec{
		crypto.SHA256: types.SigningAlgorithmSpecRsassaPssSha256,
		crypto.SHA384: types.SigningAlgorithmSpecRsassaPssSha384,
		crypto.SHA512: types.SigningAlgorithmSpecRsassaPssSha512,
	}
)

// algorithm maps the key family and opts to a KMS signing algorithm and
// message type.
func algorithm(pub crypto.PublicKey, digest []byte, opts crypto.SignerOpts) (types.SigningAlgorithmSpec, types.MessageType, error) {
	if _, ok := pub.(ed25519.PublicKey); ok {
		return ed25519Algorithm(digest, opts)
	}
	h := opts.HashFunc()
	pss, isPSS := opts.(*rsa.PSSOptions)
	table := ecdsaAlgs
	if _, ok := pub.(*rsa.PublicKey); ok {
		table = pkcs1Algs
		if isPSS {
			table = pssAlgs
		}
	} else if isPSS {
		return "", "", fmt.Errorf("%w: PSS options for a %T key", ErrUnsupportedHash, pub)
	}
	alg, ok := table[h]
	switch {
	case !ok:
		return "", "", fmt.Errorf("%w: %v", ErrUnsupportedHash, h)
	case len(digest) != h.Size():
		return "", "", fmt.Errorf("%w: %d-byte digest for %v", ErrUnsupportedHash, len(digest), h)
	case isPSS && !hashLengthSalt(pss.SaltLength, h):
		return "", "", fmt.Errorf("%w: PSS salt length %d: KMS salts with the hash length", ErrUnsupportedHash, pss.SaltLength)
	}
	return alg, types.MessageTypeDigest, nil
}

// ed25519Algorithm accepts pure Ed25519 only: KMS signs ED25519_SHA_512
// over the raw message.
func ed25519Algorithm(msg []byte, opts crypto.SignerOpts) (types.SigningAlgorithmSpec, types.MessageType, error) {
	if h := opts.HashFunc(); h != crypto.Hash(0) {
		return "", "", fmt.Errorf("%w: Ed25519 signs the message, got %v", ErrUnsupportedHash, h)
	}
	if o, ok := opts.(*ed25519.Options); ok && o.Context != "" {
		return "", "", fmt.Errorf("%w: Ed25519ctx", ErrUnsupportedHash)
	}
	if len(msg) > maxRawMessage {
		return "", "", fmt.Errorf("%w: %d-byte message, KMS signs at most %d", ErrUnsupportedHash, len(msg), maxRawMessage)
	}
	return types.SigningAlgorithmSpecEd25519Sha512, types.MessageTypeRaw, nil
}

// hashLengthSalt reports whether a PSS salt length request is met by the
// hash-length salt KMS uses.
func hashLengthSalt(n int, h crypto.Hash) bool {
	return n == rsa.PSSSaltLengthAuto || n == rsa.PSSSaltLengthEqualsHash || n == h.Size()
}

var errBadSignature = errors.New("signature does not verify")

// verify checks the KMS signature against the pinned public key, with the
// salt length the caller asked for, so a swapped key fails here instead of
// at the verifier.
func verify(pub crypto.PublicKey, digest, sig []byte, opts crypto.SignerOpts) error {
	ok := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, digest, sig)
	case ed25519.PublicKey:
		ok = ed25519.Verify(k, digest, sig)
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
