// Package crypto bundles golusoris's small set of cryptographic primitives:
// argon2id password hashing, AES-GCM symmetric encryption, and secure-random
// helpers.
//
// Higher-level concerns (password policy / breach checks) live in
// auth/policy/. Field-level / column encryption helpers live in
// crypto/columnenc/ (planned).
package crypto

import (
	"github.com/alexedwards/argon2id"
)

// DefaultPasswordParams are sensible 2026 defaults for argon2id (~100ms on
// modern hardware). Apps can override via [HashPasswordWith].
var DefaultPasswordParams = &argon2id.Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

// HashPassword hashes a plaintext with the default argon2id parameters.
// Returns an encoded hash string (PHC format) safe to store.
func HashPassword(plain string) (string, error) {
	h, err := argon2id.CreateHash(plain, DefaultPasswordParams)
	if err != nil {
		return "", err //nolint:wrapcheck // pure crypto error path
	}
	return h, nil
}

// HashPasswordWith allows custom argon2id parameters.
func HashPasswordWith(plain string, p *argon2id.Params) (string, error) {
	h, err := argon2id.CreateHash(plain, p)
	if err != nil {
		return "", err //nolint:wrapcheck
	}
	return h, nil
}

// VerifyPassword checks a plaintext against an encoded hash. Returns
// (match, needsRehash, error). needsRehash is true when params have changed
// and the caller should rehash + persist.
func VerifyPassword(plain, hash string) (match, needsRehash bool, err error) {
	match, params, err := argon2id.CheckHash(plain, hash)
	if err != nil {
		return false, false, err //nolint:wrapcheck
	}
	if !match {
		return false, false, nil
	}
	needsRehash = params.Memory != DefaultPasswordParams.Memory ||
		params.Iterations != DefaultPasswordParams.Iterations ||
		params.Parallelism != DefaultPasswordParams.Parallelism ||
		params.KeyLength != DefaultPasswordParams.KeyLength
	return true, needsRehash, nil
}
