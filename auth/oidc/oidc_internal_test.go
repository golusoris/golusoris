package oidc

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/config"
)

func TestOptions_withDefaults_addsDefaultScopes(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	require.Contains(t, got.Scopes, "openid")
	require.Contains(t, got.Scopes, "email")
	require.Contains(t, got.Scopes, "profile")
}

func TestOptions_withDefaults_preservesExplicitScopes(t *testing.T) {
	t.Parallel()
	got := Options{Scopes: []string{"openid", "groups"}}.withDefaults()
	require.Equal(t, []string{"openid", "groups"}, got.Scopes)
}

func TestLoadOptions_appliesDefaultsOnEmpty(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	require.NoError(t, err)
	opts, err := loadOptions(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, opts.Scopes, "defaults should populate Scopes")
}

// TestPKCEVerifier_isRFC7636Compliant: RFC 7636 §4.1 specifies the
// verifier as 43-128 chars of [A-Z/a-z/0-9/-/./_/~]. base64.RawURLEncoding
// of 32 bytes yields 43 chars with an unreserved alphabet.
func TestPKCEVerifier_isRFC7636Compliant(t *testing.T) {
	t.Parallel()
	v := pkceVerifier()
	require.Len(t, v, 43, "RFC 7636: 43 chars from 32 random bytes")
	decoded, err := base64.RawURLEncoding.DecodeString(v)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
}

func TestPKCEVerifier_unique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 32)
	for range 32 {
		seen[pkceVerifier()] = struct{}{}
	}
	require.Len(t, seen, 32, "verifiers must be unique across 32 calls")
}

// TestPKCEChallenge_deterministic: RFC 7636 §4.2 S256 method — challenge
// is base64url(sha256(verifier)).
func TestPKCEChallenge_deterministic(t *testing.T) {
	t.Parallel()
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 Appendix B uses this verifier and expects this challenge.
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := pkceChallenge(verifier)
	require.Equal(t, want, got)
}
