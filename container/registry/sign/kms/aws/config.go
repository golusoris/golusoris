// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// maxKeyID is the KMS limit of the KeyId parameter.
const maxKeyID = 2048

// normalize checks cfg before any I/O and fills defaults: the region of a
// key ARN and the timeout.
func normalize(cfg Config) (Config, error) {
	if err := checkKeyID(cfg.Key); err != nil {
		return cfg, err
	}
	region, err := resolveRegion(cfg.Key, cfg.Region)
	if err != nil {
		return cfg, err
	}
	cfg.Region = region
	if err := checkEndpoint(cfg.Endpoint); err != nil {
		return cfg, err
	}
	if cfg.Timeout < 0 {
		return cfg, fmt.Errorf("%w: timeout %v", ErrInvalidConfig, cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	return cfg, nil
}

// resolveRegion prefers the region of a key ARN, which must agree with a
// configured region.
func resolveRegion(key, configured string) (string, error) {
	region, err := arnRegion(key)
	switch {
	case err != nil:
		return "", err
	case region != "" && configured != "" && region != configured:
		return "", fmt.Errorf("%w: region %q differs from the key ARN's %q", ErrInvalidConfig, configured, region)
	case region != "":
		return region, nil
	case configured != "" && !validRegion(configured):
		return "", fmt.Errorf("%w: region %q", ErrInvalidConfig, configured)
	}
	return configured, nil
}

// checkKeyID accepts the characters of key IDs, aliases and ARNs.
func checkKeyID(key string) error {
	if key == "" || len(key) > maxKeyID {
		return fmt.Errorf("%w: key %q: want 1..%d characters", ErrInvalidConfig, key, maxKeyID)
	}
	for i := range len(key) {
		if c := key[i]; !isAlnum(c) && !strings.ContainsRune(":/_-", rune(c)) {
			return fmt.Errorf("%w: key %q: character %q", ErrInvalidConfig, key, c)
		}
	}
	return nil
}

// arnRegion returns the region of a key or alias ARN, "" for key IDs and
// alias names.
func arnRegion(key string) (string, error) {
	if !strings.HasPrefix(key, "arn:") {
		return "", nil
	}
	// arn:<partition>:kms:<region>:<account>:key/<id> or :alias/<name>
	parts := strings.Split(key, ":")
	if len(parts) != 6 || parts[1] == "" || parts[2] != "kms" || !validRegion(parts[3]) || parts[4] == "" || !validResource(parts[5]) {
		return "", fmt.Errorf("%w: key ARN %q: want arn:<partition>:kms:<region>:<account>:key/<id> or alias/<name>", ErrInvalidConfig, key)
	}
	return parts[3], nil
}

func validResource(r string) bool {
	id, isKey := strings.CutPrefix(r, "key/")
	name, isAlias := strings.CutPrefix(r, "alias/")
	return isKey && id != "" || isAlias && name != ""
}

func validRegion(r string) bool {
	if r == "" || r[0] == '-' || r[len(r)-1] == '-' {
		return false
	}
	for i := range len(r) {
		if c := r[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func checkEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%w: endpoint: %w", ErrInvalidConfig, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%w: endpoint %q: want http(s)://host[:port][/path]", ErrInvalidConfig, endpoint)
	}
	return nil
}

// answerMatchesKey reports whether arn, the key ARN GetPublicKey answered
// with, can be the key cfg named: the same ARN, the ARN of the key ID, or
// any key behind an alias.
func answerMatchesKey(key, arn string) bool {
	if region, err := arnRegion(arn); err != nil || region == "" || !strings.Contains(arn, ":key/") {
		return false
	}
	switch {
	case strings.HasPrefix(key, "alias/") || strings.Contains(key, ":alias/"):
		return true
	case strings.HasPrefix(key, "arn:"):
		return key == arn
	default:
		return strings.HasSuffix(arn, ":key/"+key)
	}
}

var (
	ecCurves = map[types.KeySpec]elliptic.Curve{
		types.KeySpecEccNistP256: elliptic.P256(),
		types.KeySpecEccNistP384: elliptic.P384(),
		types.KeySpecEccNistP521: elliptic.P521(),
	}
	rsaBits = map[types.KeySpec]int{
		types.KeySpecRsa2048: 2048,
		types.KeySpecRsa3072: 3072,
		types.KeySpecRsa4096: 4096,
	}
)

// parsePublicKey reads the DER SubjectPublicKeyInfo of GetPublicKey and
// checks it against the key spec.
func parsePublicKey(spec types.KeySpec, der []byte) (crypto.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("key spec %q: %w", spec, err)
	}
	ok := false
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		ok = ecCurves[spec] == k.Curve
	case *rsa.PublicKey:
		ok = rsaBits[spec] == k.N.BitLen()
	case ed25519.PublicKey:
		ok = spec == types.KeySpecEccNistEdwards25519
	}
	if !ok {
		return nil, fmt.Errorf("key spec %q: public key is %T", spec, pub)
	}
	return pub, nil
}
