// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/net/http/httpguts"
)

const (
	// MinPresignTTL is the shortest presigned-URL lifetime; signers count whole seconds.
	MinPresignTTL = time.Second
	// MaxPresignTTL is the longest lifetime S3 SigV4, GCS V4, and user-delegation SAS all accept.
	MaxPresignTTL = 7 * 24 * time.Hour
)

var (
	// ErrPresignTTL is returned when a presign lifetime is outside MinPresignTTL..MaxPresignTTL.
	ErrPresignTTL = errors.New("storage: presign ttl out of range")
	// ErrUnsupportedChecksum is returned when a backend cannot bind the requested checksum.
	ErrUnsupportedChecksum = errors.New("storage: unsupported checksum")
	// ErrUnsupportedConstraint is returned when a backend's signing scheme
	// cannot enforce a requested presign constraint, such as upload length.
	ErrUnsupportedConstraint = errors.New("storage: unsupported presign constraint")
	// ErrCopySameKey is returned when a copy names one key as both source and destination.
	ErrCopySameKey = errors.New("storage: copy source and destination are the same key")
)

// ChecksumAlgorithm names a whole-object digest a presigned upload can bind.
type ChecksumAlgorithm string

const (
	// ChecksumSHA256 binds x-amz-checksum-sha256 (S3 only).
	ChecksumSHA256 ChecksumAlgorithm = "sha256"
	// ChecksumCRC32C binds a Castagnoli CRC32 (S3, GCS).
	ChecksumCRC32C ChecksumAlgorithm = "crc32c"
	// ChecksumMD5 binds Content-MD5 (S3, GCS, Azure).
	ChecksumMD5 ChecksumAlgorithm = "md5"
)

// Checksum is a raw, unencoded digest of the complete object body.
type Checksum struct {
	Algorithm ChecksumAlgorithm
	Value     []byte
}

// Base64 returns the standard base64 form the storage HTTP APIs carry.
func (c Checksum) Base64() string { return base64.StdEncoding.EncodeToString(c.Value) }

// PresignPutOptions constrains a presigned upload. Zero fields leave that
// aspect unconstrained. Backends bind what their signing scheme can enforce
// and return [ErrUnsupportedConstraint] or [ErrUnsupportedChecksum] for the
// rest instead of dropping it; see each backend's PresignPut documentation.
type PresignPutOptions struct {
	// ContentType the uploader must send; empty accepts any content type.
	ContentType string
	// ContentLength is the exact body length in bytes; zero accepts any length.
	ContentLength int64
	// Metadata is user metadata the uploader must send as headers.
	Metadata map[string]string
	// Checksum binds a whole-body digest; a zero Algorithm binds none.
	Checksum Checksum
}

// PresignedRequest is a credential-free HTTP request an untrusted client may
// send until Expires.
type PresignedRequest struct {
	Method string
	URL    string
	// Header holds headers the client must send verbatim, including any
	// Content-Length the signature binds.
	Header  http.Header
	Expires time.Time
}

// PutPresigner is implemented by backends that sign upload URLs.
type PutPresigner interface {
	// PresignPut signs a PUT of key valid for ttl. The object need not exist.
	PresignPut(ctx context.Context, key string, ttl time.Duration, opts PresignPutOptions) (PresignedRequest, error)
}

// Copier is implemented by backends that copy objects server-side.
type Copier interface {
	// Copy replaces dstKey with srcKey's body, content type, and metadata.
	// Returns [ErrNotFound] when srcKey does not exist.
	Copy(ctx context.Context, srcKey, dstKey string) (Object, error)
}

// ValidatePresignTTL rejects lifetimes outside MinPresignTTL..MaxPresignTTL.
func ValidatePresignTTL(ttl time.Duration) error {
	if ttl < MinPresignTTL || ttl > MaxPresignTTL {
		return fmt.Errorf("%w: %s not in %s..%s", ErrPresignTTL, ttl, MinPresignTTL, MaxPresignTTL)
	}
	return nil
}

// ValidatePresignPut checks ttl and opts before any backend signs.
func ValidatePresignPut(ttl time.Duration, opts PresignPutOptions) error {
	if err := ValidatePresignTTL(ttl); err != nil {
		return err
	}
	if opts.ContentLength < 0 {
		return fmt.Errorf("storage: presign content length %d is negative", opts.ContentLength)
	}
	if opts.ContentType != "" && !httpguts.ValidHeaderFieldValue(opts.ContentType) {
		return fmt.Errorf("storage: presign content type %q is not a valid header value", opts.ContentType)
	}
	for name, value := range opts.Metadata {
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("storage: presign metadata %q is not a valid header", name)
		}
	}
	return validateChecksum(opts.Checksum)
}

func validateChecksum(sum Checksum) error {
	var size int
	switch sum.Algorithm {
	case "":
		size = 0
	case ChecksumSHA256:
		size = 32
	case ChecksumCRC32C:
		size = 4
	case ChecksumMD5:
		size = 16
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedChecksum, sum.Algorithm)
	}
	if len(sum.Value) != size {
		return fmt.Errorf("storage: %q checksum has %d bytes, want %d", sum.Algorithm, len(sum.Value), size)
	}
	return nil
}
