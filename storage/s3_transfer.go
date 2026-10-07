// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
)

const (
	// S3MinPartSize is the smallest multipart part S3 accepts (except the last part).
	S3MinPartSize = 5 << 20
	// S3MaxPartSize is the largest multipart part, and the largest single PUT or CopyObject.
	S3MaxPartSize = 5 << 30
	// S3MaxConcurrency bounds parallel part transfers per Put or Copy call.
	S3MaxConcurrency = 32

	s3MaxParts                 = 10000
	defaultS3PartSize          = 8 << 20
	defaultS3MultipartTrigger  = 16 << 20
	defaultS3Concurrency       = 5
	defaultS3CopyPartSize      = 1 << 30
	defaultS3MultipartCopySize = S3MaxPartSize
)

// s3Transfer holds validated multipart settings for one bucket.
type s3Transfer struct {
	partSize    int64
	threshold   int64
	concurrency int
	checksums   aws.RequestChecksumCalculation
	// copyThreshold and copyPartSize are fields so tests can force multipart copies.
	copyThreshold int64
	copyPartSize  int64
}

func defaultS3Transfer() s3Transfer {
	return s3Transfer{
		partSize:      defaultS3PartSize,
		threshold:     defaultS3MultipartTrigger,
		concurrency:   defaultS3Concurrency,
		copyThreshold: defaultS3MultipartCopySize,
		copyPartSize:  defaultS3CopyPartSize,
	}
}

func resolveS3Transfer(opts S3Options) (s3Transfer, error) {
	t := defaultS3Transfer()
	if opts.PartSize != 0 {
		t.partSize = opts.PartSize
	}
	if opts.MultipartThreshold != 0 {
		t.threshold = opts.MultipartThreshold
	}
	if opts.Concurrency != 0 {
		t.concurrency = opts.Concurrency
	}
	if t.partSize < S3MinPartSize || t.partSize > S3MaxPartSize {
		return s3Transfer{}, fmt.Errorf("storage/s3: part_size %d outside %d..%d", t.partSize, S3MinPartSize, S3MaxPartSize)
	}
	if t.threshold < S3MinPartSize || t.threshold > S3MaxPartSize {
		return s3Transfer{}, fmt.Errorf(
			"storage/s3: multipart_threshold %d outside %d..%d", t.threshold, S3MinPartSize, S3MaxPartSize,
		)
	}
	if t.concurrency < 1 || t.concurrency > S3MaxConcurrency {
		return s3Transfer{}, fmt.Errorf("storage/s3: concurrency %d outside 1..%d", t.concurrency, S3MaxConcurrency)
	}
	return t, nil
}

// newS3Uploader keeps the client's checksum policy: transfermanager would
// otherwise force CRC32 on S3-compatible stores configured "when_required".
func newS3Uploader(client s3API, t s3Transfer) *transfermanager.Client {
	return transfermanager.New(client, func(o *transfermanager.Options) {
		o.PartSizeBytes = t.partSize
		o.MultipartUploadThreshold = t.threshold
		o.Concurrency = t.concurrency
		o.MaxUploadParts = s3MaxParts
		o.RequestChecksumCalculation = t.checksums
	})
}
