// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package jwt provides HMAC JWT signing and verification using
// [golang-jwt/jwt/v5]. It wraps the library with the framework's error
// conventions and clock.
//
// The package is not an fx module — it's a pure utility layer.
// Consumers that need a shared key provider should wire it themselves
// via fx.Provide.
//
// Supported algorithms are HS256, HS384, and HS512. A signer has one secret;
// changing it invalidates outstanding tokens. Overlapping key rotation is not
// provided by this package.
package jwt

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
)

// Algorithm is a re-export of the signing method type for callers that
// don't want to import golang-jwt directly.
type Algorithm = jwt.SigningMethod

// HMAC signing methods accepted by [NewHMACSigner].
var (
	HS256 Algorithm = jwt.SigningMethodHS256
	HS384 Algorithm = jwt.SigningMethodHS384
	HS512 Algorithm = jwt.SigningMethodHS512
)

// Legacy non-HMAC aliases remain for source compatibility. NewHMACSigner
// rejects them.
//
// Deprecated: import the upstream package when constructing an RSA signer.
var (
	RS256 Algorithm = jwt.SigningMethodRS256
	RS384 Algorithm = jwt.SigningMethodRS384
	RS512 Algorithm = jwt.SigningMethodRS512
)

// Claims is the set of registered + custom claims in a token.
// Embed [jwt.RegisteredClaims] and add your own fields.
//
//	type AppClaims struct {
//	    jwt.RegisteredClaims
//	    UserID string `json:"uid"`
//	    Roles  []string `json:"roles"`
//	}
type Claims = jwt.Claims

// RegisteredClaims re-exports jwt.RegisteredClaims for convenience.
type RegisteredClaims = jwt.RegisteredClaims

// Signer signs and verifies JWTs with one HMAC secret.
type Signer struct {
	alg *jwt.SigningMethodHMAC
	key *signingKey
	ttl time.Duration
	clk *signingClock
}

type signingKey struct {
	bytes []byte
}

type signingClock struct {
	clockwork.Clock
}

// NewHMACSigner returns a Signer using the given HMAC algorithm and
// secret. ttl must be positive. The real clock is used.
func NewHMACSigner(alg jwt.SigningMethod, secret []byte, ttl time.Duration) (*Signer, error) {
	return NewHMACSignerWithClock(alg, secret, ttl, clockwork.NewRealClock())
}

// NewHMACSignerWithClock returns an HMAC Signer with an injected clock.
func NewHMACSignerWithClock(
	alg jwt.SigningMethod,
	secret []byte,
	ttl time.Duration,
	clk clockwork.Clock,
) (*Signer, error) {
	if len(secret) == 0 {
		return nil, errors.New("jwt: HMAC secret must not be empty")
	}
	hmacAlg, ok := alg.(*jwt.SigningMethodHMAC)
	if !ok {
		return nil, errors.New("jwt: signing method must be HMAC")
	}
	switch hmacAlg {
	case jwt.SigningMethodHS256, jwt.SigningMethodHS384, jwt.SigningMethodHS512:
	default:
		return nil, errors.New("jwt: signing method must be HS256, HS384, or HS512")
	}
	if len(secret) < hmacAlg.Hash.Size() {
		return nil, fmt.Errorf("jwt: HMAC secret must be at least %d bytes for %s", hmacAlg.Hash.Size(), hmacAlg.Alg())
	}
	if ttl < time.Second {
		return nil, errors.New("jwt: TTL must be at least one second")
	}
	if validate.IsNil(clk) {
		return nil, errors.New("jwt: clock must not be nil")
	}
	key := &signingKey{bytes: append([]byte(nil), secret...)}
	return &Signer{alg: hmacAlg, key: key, ttl: ttl, clk: &signingClock{Clock: clk}}, nil
}

// Sign creates a signed JWT string for the given claims. If claims
// embed [RegisteredClaims] and ExpiresAt is zero, it is set to
// now + Signer.ttl.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	if validate.IsNil(claims) {
		return "", errors.New("jwt: claims must not be nil")
	}
	expiresAt, err := claims.GetExpirationTime()
	if err != nil {
		return "", fmt.Errorf("jwt: read expiration: %w", err)
	}
	if expiresAt == nil {
		claims = claimsWithExpiry{
			Claims:    claims,
			expiresAt: jwt.NewNumericDate(s.clk.Now().Add(s.ttl)),
		}
	}

	tok := jwt.NewWithClaims(s.alg, claims)
	str, err := tok.SignedString(s.key.bytes)
	if err != nil {
		return "", fmt.Errorf("jwt: sign: %w", err)
	}
	return str, nil
}

// Parse validates tokenStr and populates claims. Returns a wrapped
// gerr.CodeUnauthorized on invalid/expired tokens.
func (s *Signer) Parse(tokenStr string, claims jwt.Claims) error {
	if validate.IsNil(claims) {
		return fmt.Errorf("%w: claims must not be nil", gerr.Unauthorized("token invalid"))
	}
	_, err := jwt.ParseWithClaims(
		tokenStr, claims, func(tok *jwt.Token) (any, error) {
			if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("jwt: token signing method must be HMAC")
			}
			return s.key.bytes, nil
		},
		jwt.WithValidMethods([]string{s.alg.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.clk.Now),
	)
	if err != nil {
		return fmt.Errorf("%w: %w", gerr.Unauthorized("token invalid"), err)
	}
	return nil
}

type claimsWithExpiry struct {
	jwt.Claims
	expiresAt *jwt.NumericDate
}

func (c claimsWithExpiry) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(c.Claims)
	if err != nil {
		return nil, fmt.Errorf("marshal claims: %w", err)
	}
	fields := make(map[string]json.RawMessage)
	if decodeErr := json.Unmarshal(raw, &fields); decodeErr != nil {
		return nil, fmt.Errorf("decode claims object: %w", decodeErr)
	}
	if fields == nil {
		return nil, errors.New("claims must encode as a JSON object")
	}
	expiresAt, err := json.Marshal(c.expiresAt)
	if err != nil {
		return nil, fmt.Errorf("marshal expiration: %w", err)
	}
	fields["exp"] = expiresAt
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal claims with expiration: %w", err)
	}
	return out, nil
}

func (c claimsWithExpiry) GetExpirationTime() (*jwt.NumericDate, error) {
	return c.expiresAt, nil
}

// ErrExpired is true when err wraps a jwt.ErrTokenExpired.
func ErrExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// ErrInvalid is true when err wraps any jwt validation failure.
func ErrInvalid(err error) bool {
	return errors.Is(err, jwt.ErrTokenSignatureInvalid) ||
		errors.Is(err, jwt.ErrTokenMalformed) ||
		errors.Is(err, jwt.ErrTokenRequiredClaimMissing) ||
		errors.Is(err, jwt.ErrTokenNotValidYet)
}
