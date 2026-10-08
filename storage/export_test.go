// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

// SetS3CopyPartsForTest lowers the multipart-copy switch so integration tests
// exercise UploadPartCopy without multi-GiB fixtures.
func SetS3CopyPartsForTest(b *S3Bucket, threshold, partSize int64) {
	b.transfer.copyThreshold = threshold
	b.transfer.copyPartSize = partSize
}
