// Package jwt provides helpers for signing and verifying JWTs using
// [golang-jwt/jwt/v5]. It wraps the library with the framework's
// error conventions and clock.
//
// The package is not an fx module — it's a pure utility layer.
// Consumers that need a shared key provider should wire it themselves
// via fx.Provide.
//
// Supported algorithms: HS256, HS384, HS512 (HMAC) and RS256, RS384,
// RS512 (RSA). EC algorithms (ES256 etc.) can be used by calling
// [jwt.NewSigner] with the appropriate [jwt.Algorithm].
package jwt

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	gerr "github.com/golusoris/golusoris/errors"
)

// Algorithm is a re-export of the signing method type for callers that
// don't want to import golang-jwt directly.
type Algorithm = jwt.SigningMethod

// Standard signing methods re-exported for convenience.
var (
	HS256 Algorithm = jwt.SigningMethodHS256
	HS384 Algorithm = jwt.SigningMethodHS384
	HS512 Algorithm = jwt.SigningMethodHS512
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

// Signer signs and verifies JWTs with a symmetric or asymmetric key.
type Signer struct {
	alg jwt.SigningMethod
	key any // signing key ([]byte for HMAC, *rsa.PrivateKey for RSA)
	pub any // verification key (same as key for HMAC, *rsa.PublicKey for RSA)
	ttl time.Duration
}

// NewHMACSigner returns a Signer using the given HMAC algorithm and
// secret. Panics if secret is empty.
func NewHMACSigner(alg jwt.SigningMethod, secret []byte, ttl time.Duration) *Signer {
	if len(secret) == 0 {
		panic("jwt: HMAC secret must not be empty")
	}
	return &Signer{alg: alg, key: secret, pub: secret, ttl: ttl}
}

// Sign creates a signed JWT string for the given claims. If claims
// embed [RegisteredClaims] and ExpiresAt is zero, it is set to
// now + Signer.ttl.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	tok := jwt.NewWithClaims(s.alg, claims)
	str, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("jwt: sign: %w", err)
	}
	return str, nil
}

// Parse validates tokenStr and populates claims. Returns a wrapped
// gerr.CodeUnauthorized on invalid/expired tokens.
func (s *Signer) Parse(tokenStr string, claims jwt.Claims) error {
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(_ *jwt.Token) (any, error) {
		return s.pub, nil
	}, jwt.WithValidMethods([]string{s.alg.Alg()}))
	if err != nil {
		return fmt.Errorf("%w: %w", gerr.Unauthorized("token invalid"), err)
	}
	return nil
}

// ErrExpired is true when err wraps a jwt.ErrTokenExpired.
func ErrExpired(err error) bool {
	return errors.Is(err, jwt.ErrTokenExpired)
}

// ErrInvalid is true when err wraps any jwt validation failure.
func ErrInvalid(err error) bool {
	return errors.Is(err, jwt.ErrTokenSignatureInvalid) ||
		errors.Is(err, jwt.ErrTokenMalformed) ||
		errors.Is(err, jwt.ErrTokenNotValidYet)
}
