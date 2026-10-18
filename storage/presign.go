// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"time"

	corestorage "github.com/golusoris/golusoris/core/storage"
)

const (
	// MinPresignTTL is the shortest presigned-URL lifetime; signers count whole seconds.
	MinPresignTTL = corestorage.MinPresignTTL
	// MaxPresignTTL is the longest lifetime S3 SigV4, GCS V4, and user-delegation SAS all accept.
	MaxPresignTTL = corestorage.MaxPresignTTL
)

var (
	// ErrPresignTTL is returned when a presign lifetime is outside MinPresignTTL..MaxPresignTTL.
	ErrPresignTTL = corestorage.ErrPresignTTL
	// ErrUnsupportedChecksum is returned when a backend cannot bind the requested checksum.
	ErrUnsupportedChecksum = corestorage.ErrUnsupportedChecksum
	// ErrUnsupportedConstraint is returned when a backend's signing scheme
	// cannot enforce a requested presign constraint, such as upload length.
	ErrUnsupportedConstraint = corestorage.ErrUnsupportedConstraint
	// ErrCopySameKey is returned when a copy names one key as both source and destination.
	ErrCopySameKey = corestorage.ErrCopySameKey
)

// ChecksumAlgorithm names a whole-object digest a presigned upload can bind.
type ChecksumAlgorithm = corestorage.ChecksumAlgorithm

const (
	// ChecksumSHA256 binds x-amz-checksum-sha256 (S3 only).
	ChecksumSHA256 = corestorage.ChecksumSHA256
	// ChecksumCRC32C binds a Castagnoli CRC32 (S3, GCS).
	ChecksumCRC32C = corestorage.ChecksumCRC32C
	// ChecksumMD5 binds Content-MD5 (S3, GCS, Azure).
	ChecksumMD5 = corestorage.ChecksumMD5
)

// Checksum is a raw, unencoded digest of the complete object body.
type Checksum = corestorage.Checksum

// PresignPutOptions constrains a presigned upload. Zero fields leave that
// aspect unconstrained. Backends bind what their signing scheme can enforce
// and return [ErrUnsupportedConstraint] or [ErrUnsupportedChecksum] for the
// rest instead of dropping it; see each backend's PresignPut documentation.
type PresignPutOptions = corestorage.PresignPutOptions

// PresignedRequest is a credential-free HTTP request an untrusted client may
// send until Expires.
type PresignedRequest = corestorage.PresignedRequest

// PutPresigner is implemented by backends that sign upload URLs.
type PutPresigner = corestorage.PutPresigner

// Copier is implemented by backends that copy objects server-side.
type Copier = corestorage.Copier

// ValidatePresignTTL rejects lifetimes outside MinPresignTTL..MaxPresignTTL.
func ValidatePresignTTL(ttl time.Duration) error {
	return corestorage.ValidatePresignTTL(ttl) //nolint:wrapcheck // Preserve shared API error values and messages.
}

// ValidatePresignPut checks ttl and opts before any backend signs.
func ValidatePresignPut(ttl time.Duration, opts PresignPutOptions) error {
	return corestorage.ValidatePresignPut(ttl, opts) //nolint:wrapcheck // Preserve shared API error values and messages.
}
