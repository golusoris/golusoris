// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newPresignTestBucket(t *testing.T) *S3Bucket {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return newTestBucket(t, srv)
}

func TestS3Bucket_PresignPut_SignsConstraints(t *testing.T) {
	t.Parallel()
	b := newPresignTestBucket(t)
	sum := sha256.Sum256([]byte("hello"))
	req, err := b.PresignPut(context.Background(), "uploads/video.mp4", 90*time.Second, PresignPutOptions{
		ContentType:   "video/mp4",
		ContentLength: 5,
		Metadata:      map[string]string{"job": "42"},
		Checksum:      Checksum{Algorithm: ChecksumSHA256, Value: sum[:]},
	})
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, req.Method)
	require.Equal(t, "video/mp4", req.Header.Get("Content-Type"))
	require.Equal(t, "5", req.Header.Get("Content-Length"))
	require.Equal(t, "42", req.Header.Get("X-Amz-Meta-Job"))
	require.Equal(t, Checksum{Value: sum[:]}.Base64(), req.Header.Get("X-Amz-Checksum-Sha256"))
	require.Empty(t, req.Header.Get("Host"))

	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	require.Equal(t, "/my-bucket/uploads/video.mp4", u.Path)
	q := u.Query()
	require.Equal(t, "90", q.Get("X-Amz-Expires"))
	signed := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	require.Subset(t, signed, []string{"content-length", "content-type", "host", "x-amz-checksum-sha256", "x-amz-meta-job"})
	signedAt, err := time.Parse(s3AmzDateLayout, q.Get("X-Amz-Date"))
	require.NoError(t, err)
	require.Equal(t, signedAt.Add(90*time.Second), req.Expires)
}

func TestS3Bucket_PresignPut_Unconstrained(t *testing.T) {
	t.Parallel()
	b := newPresignTestBucket(t)
	req, err := b.PresignPut(context.Background(), "k", MinPresignTTL, PresignPutOptions{})
	require.NoError(t, err)
	require.Empty(t, req.Header)
	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	require.Equal(t, "host", u.Query().Get("X-Amz-SignedHeaders"))
	require.Equal(t, "1", u.Query().Get("X-Amz-Expires"))
}

func TestS3Bucket_PresignPut_ChecksumHeaders(t *testing.T) {
	t.Parallel()
	b := newPresignTestBucket(t)
	for _, tc := range []struct {
		sum    Checksum
		header string
	}{
		{sum: Checksum{Algorithm: ChecksumCRC32C, Value: []byte{1, 2, 3, 4}}, header: "X-Amz-Checksum-Crc32c"},
		{sum: Checksum{Algorithm: ChecksumMD5, Value: make([]byte, 16)}, header: "Content-Md5"},
	} {
		req, err := b.PresignPut(context.Background(), "k", time.Minute, PresignPutOptions{Checksum: tc.sum})
		require.NoError(t, err)
		require.Equal(t, tc.sum.Base64(), req.Header.Get(tc.header), tc.sum.Algorithm)
	}
}

func TestS3Bucket_PresignPut_RejectsBeforeSigning(t *testing.T) {
	t.Parallel()
	b := newS3BucketForTest("bucket", nil, nil, time.Minute) // nil presigner: any signing attempt panics
	_, err := b.PresignPut(context.Background(), "../escape", time.Minute, PresignPutOptions{})
	require.ErrorIs(t, err, ErrUnsafeKey)
	_, err = b.PresignPut(context.Background(), "k", MaxPresignTTL+time.Second, PresignPutOptions{})
	require.ErrorIs(t, err, ErrPresignTTL)
	_, err = b.PresignPut(context.Background(), "k", time.Minute, PresignPutOptions{
		Checksum: Checksum{Algorithm: "whirlpool"},
	})
	require.ErrorIs(t, err, ErrUnsupportedChecksum)
}

func TestS3PresignedRequest_RejectsMissingDate(t *testing.T) {
	t.Parallel()
	_, err := s3PresignedRequest(&v4PresignedRequest{URL: "https://example.test/b/k", Method: http.MethodPut}, time.Minute)
	require.Error(t, err)
	_, err = s3PresignedRequest(&v4PresignedRequest{URL: "://bad", Method: http.MethodPut}, time.Minute)
	require.Error(t, err)
}
