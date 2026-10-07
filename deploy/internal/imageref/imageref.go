// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package imageref validates deployment image references.
package imageref

import (
	"errors"
	"strings"
)

var errImmutableSHA256 = errors.New("appImage must be repository@sha256:<64 lowercase hex characters>")

// ValidateImmutableSHA256 rejects mutable or malformed image references.
func ValidateImmutableSHA256(image string) error {
	repository, digest, found := strings.Cut(image, "@sha256:")
	if !found || repository == "" || strings.ContainsAny(repository, "@ \t\r\n") || len(digest) != 64 {
		return errImmutableSHA256
	}
	for i := range 64 {
		if c := digest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return errImmutableSHA256
		}
	}
	return nil
}
