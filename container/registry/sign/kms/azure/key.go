// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
)

const (
	// maxKeyName is Key Vault's key name limit.
	maxKeyName = 127
	// maxVersion bounds a key version; Key Vault versions are 32 hex digits.
	maxVersion = 64
)

// normalize checks cfg before any I/O, fills the timeout and returns the
// vault host.
func normalize(cfg Config) (Config, string, error) {
	host, err := vaultHost(cfg.Vault)
	if err != nil {
		return cfg, "", err
	}
	switch {
	case !validName(cfg.Key, maxKeyName, true):
		return cfg, "", fmt.Errorf("%w: key name %q: want 1..%d of [0-9A-Za-z-]", ErrInvalidConfig, cfg.Key, maxKeyName)
	case cfg.Version != "" && !validName(cfg.Version, maxVersion, false):
		return cfg, "", fmt.Errorf("%w: key version %q: want 1..%d of [0-9A-Za-z]", ErrInvalidConfig, cfg.Version, maxVersion)
	case cfg.Timeout < 0:
		return cfg, "", fmt.Errorf("%w: timeout %v", ErrInvalidConfig, cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	return cfg, host, nil
}

// vaultHost accepts https://host[:port]: azcore sends bearer tokens over
// TLS only.
func vaultHost(vault string) (string, error) {
	u, err := url.Parse(vault)
	if err != nil {
		return "", fmt.Errorf("%w: vault: %w", ErrInvalidConfig, err)
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%w: vault %q: want https://host[:port]", ErrInvalidConfig, vault)
	}
	return u.Host, nil
}

// validName accepts 1..maxLen letters and digits, and dashes when dash is
// set.
func validName(s string, maxLen int, dash bool) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !isAlnum(c) && (!dash || c != '-') {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// keyVersion checks that kid names this vault and key, and want when set,
// and returns its version.
func (s *Signer) keyVersion(kid *azkeys.ID, want string) (string, error) {
	if kid == nil {
		return "", errors.New("answer has no key ID")
	}
	u, err := url.Parse(string(*kid))
	switch {
	case err != nil:
		return "", fmt.Errorf("answer key ID: %w", err)
	case u.Host != s.vault || kid.Name() != s.name || kid.Version() == "" || want != "" && kid.Version() != want:
		return "", fmt.Errorf("answer names key %q, want %s/keys/%s/%s", string(*kid), s.vault, s.name, want)
	}
	return kid.Version(), nil
}

// signable rejects disabled keys and keys without the sign operation.
func signable(b azkeys.KeyBundle) error {
	if b.Attributes != nil && b.Attributes.Enabled != nil && !*b.Attributes.Enabled {
		return errors.New("key disabled")
	}
	if !slices.ContainsFunc(b.Key.KeyOps, func(op *azkeys.KeyOperation) bool { return op != nil && *op == azkeys.KeyOperationSign }) {
		return fmt.Errorf("key operations %v lack sign", opNames(b.Key.KeyOps))
	}
	return nil
}

func opNames(ops []*azkeys.KeyOperation) []string {
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		if op != nil {
			names = append(names, string(*op))
		}
	}
	return names
}

var curves = map[azkeys.CurveName]elliptic.Curve{
	azkeys.CurveNameP256: elliptic.P256(),
	azkeys.CurveNameP384: elliptic.P384(),
	azkeys.CurveNameP521: elliptic.P521(),
}

// publicKey reads the JSON Web Key of an EC or RSA key.
func publicKey(k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	if k.Kty == nil {
		return nil, errors.New("no key type")
	}
	switch *k.Kty {
	case azkeys.KeyTypeEC, azkeys.KeyTypeECHSM:
		return ecPublicKey(k)
	case azkeys.KeyTypeRSA, azkeys.KeyTypeRSAHSM:
		return rsaPublicKey(k)
	case azkeys.KeyTypeOct, azkeys.KeyTypeOctHSM:
		return nil, fmt.Errorf("symmetric key type %q", *k.Kty)
	default:
		return nil, fmt.Errorf("key type %q", *k.Kty)
	}
}

func ecPublicKey(k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	if k.Crv == nil || curves[*k.Crv] == nil {
		return nil, fmt.Errorf("curve %v", k.Crv)
	}
	c := curves[*k.Crv]
	size := (c.Params().BitSize + 7) / 8
	if len(k.X) != size || len(k.Y) != size {
		return nil, fmt.Errorf("curve %s: %d- and %d-byte coordinates, want %d", *k.Crv, len(k.X), len(k.Y), size)
	}
	point := append(append([]byte{4}, k.X...), k.Y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(c, point)
	if err != nil {
		return nil, fmt.Errorf("curve %s: %w", *k.Crv, err)
	}
	return pub, nil
}

func rsaPublicKey(k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	e := new(big.Int).SetBytes(k.E)
	if len(k.N) == 0 || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || e.Bit(0) == 0 {
		return nil, fmt.Errorf("RSA key: %d-byte modulus, exponent %v", len(k.N), e)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(k.N), E: int(e.Int64())}, nil
}
