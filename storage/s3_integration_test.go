// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is an S3 integrity header, not a security primitive.
	"crypto/sha256"
	"errors"
	"hash/crc32"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/testutil/objstore"
)

func startS3Bucket(t *testing.T, opts storage.S3Options) *storage.S3Bucket {
	t.Helper()
	srv := objstore.StartS3(t)
	opts.Bucket, opts.Region, opts.Endpoint = srv.Bucket, srv.Region, srv.Endpoint
	opts.AccessKey, opts.SecretKey, opts.PathStyle = srv.AccessKey, srv.SecretKey, true
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	b, err := storage.NewS3Bucket(ctx, opts)
	if err != nil {
		t.Fatalf("NewS3Bucket: %v", err)
	}
	return b
}

func TestS3Bucket_Conformance(t *testing.T) {
	t.Parallel()
	objstore.RunConformance(t, startS3Bucket(t, storage.S3Options{}), objstore.Capabilities{
		PresignEnforced: true, PresignHeadersEnforced: true, URLFetchable: true,
	})
}

func TestLocalBucket_Conformance(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objstore.RunConformance(t, b, objstore.Capabilities{})
}

// patterned is a deterministic, unseekable body: Put cannot learn its length.
type patterned struct{ remaining, offset int64 }

func (p *patterned) Read(buf []byte) (int, error) {
	if p.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(buf))
	n = min(n, p.remaining)
	for i := range n {
		buf[i] = byte((p.offset + i) % 251)
	}
	p.remaining -= n
	p.offset += n
	return int(n), nil
}

func TestS3Bucket_MultipartUnseekableBody(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{
		PartSize: storage.S3MinPartSize, MultipartThreshold: storage.S3MinPartSize, Concurrency: 2,
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	const size = 2*storage.S3MinPartSize + 3
	obj, err := b.Put(ctx, "multipart/stream.bin", &patterned{remaining: size}, storage.PutOptions{
		ContentType: "video/mp4", Metadata: map[string]string{"job": "7"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != size || !strings.HasSuffix(strings.Trim(obj.ETag, `"`), "-3") {
		t.Fatalf("Put object = %+v, want %d bytes in 3 parts", obj, size)
	}
	assertS3Body(ctx, t, b, "multipart/stream.bin", size)
	stat, err := b.Stat(ctx, "multipart/stream.bin")
	if err != nil || stat.ContentType != "video/mp4" || stat.Metadata["job"] != "7" {
		t.Fatalf("Stat = %+v, %v", stat, err)
	}
}

func TestS3Bucket_MultipartCopy(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{Concurrency: 2})
	storage.SetS3CopyPartsForTest(b, storage.S3MinPartSize, storage.S3MinPartSize)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	const size = 2*storage.S3MinPartSize + 11
	if _, err := b.Put(ctx, "copy/big src", &patterned{remaining: size}, storage.PutOptions{
		ContentType: "video/mp4", Metadata: map[string]string{"stage": "raw"},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	obj, err := b.Copy(ctx, "copy/big src", "copy/big+dst")
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if obj.Size != size || !strings.HasSuffix(strings.Trim(obj.ETag, `"`), "-3") {
		t.Fatalf("Copy object = %+v, want %d bytes in 3 parts", obj, size)
	}
	assertS3Body(ctx, t, b, "copy/big+dst", size)
	stat, err := b.Stat(ctx, "copy/big+dst")
	if err != nil || stat.ContentType != "video/mp4" || stat.Metadata["stage"] != "raw" {
		t.Fatalf("Stat copy = %+v, %v", stat, err)
	}
}

func TestS3Bucket_PresignPutChecksumEnforced(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	body := []byte("checksummed upload")
	sha := sha256.Sum256(body)
	md := md5.Sum(body) //nolint:gosec // see import.
	crc := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))
	sums := []storage.Checksum{
		{Algorithm: storage.ChecksumSHA256, Value: sha[:]},
		{Algorithm: storage.ChecksumMD5, Value: md[:]},
		{Algorithm: storage.ChecksumCRC32C, Value: []byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)}},
	}
	for _, sum := range sums {
		req, err := b.PresignPut(ctx, "checksum/"+string(sum.Algorithm), time.Minute, storage.PresignPutOptions{Checksum: sum})
		if err != nil {
			t.Fatalf("PresignPut %s: %v", sum.Algorithm, err)
		}
		if status := putPresigned(ctx, t, req, []byte("tampered upload!!!")); status/100 == 2 {
			t.Fatalf("%s: tampered body status = %d, want rejection", sum.Algorithm, status)
		}
		if status := putPresigned(ctx, t, req, body); status/100 != 2 {
			t.Fatalf("%s: matching body status = %d", sum.Algorithm, status)
		}
	}
}

func TestS3Bucket_PresignPutExpiryAndDifferentKey(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	clk := clockwork.NewRealClock()

	const keyA = "presign/expiry-key-a"
	assertS3PresignPutPositive(ctx, t, b, keyA)
	assertS3PresignPutExpired(ctx, t, b, clk)
	assertS3PresignPutDifferentKey(ctx, t, b, keyA)
}

func assertS3PresignPutPositive(ctx context.Context, t *testing.T, b *storage.S3Bucket, keyA string) {
	t.Helper()
	reqPos, err := b.PresignPut(ctx, keyA, storage.MinPresignTTL, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut positive: %v", err)
	}
	if status := putPresigned(ctx, t, reqPos, []byte("positive payload")); status/100 != 2 {
		t.Fatalf("positive PUT status = %d, want 2xx", status)
	}
	stat, err := b.Stat(ctx, keyA)
	if err != nil || stat.Size != int64(len("positive payload")) {
		t.Fatalf("Stat(%q) = %+v, %v", keyA, stat, err)
	}
}

func assertS3PresignPutExpired(ctx context.Context, t *testing.T, b *storage.S3Bucket, clk clockwork.Clock) {
	t.Helper()
	const keyExp = "presign/expiry-key-expired"
	reqExp, err := b.PresignPut(ctx, keyExp, storage.MinPresignTTL, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut expired: %v", err)
	}
	wait := reqExp.Expires.Sub(clk.Now())
	if wait > 0 {
		select {
		case <-clk.After(wait + 1500*time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("context done waiting for expiry: %v", ctx.Err())
		}
	}
	// Polls for up to 4s so emulator clock skew cannot pass a live URL as expired.
	expStatus := pollUntilExpiredS3PUT(ctx, t, reqExp, clk)
	if expStatus/100 == 2 {
		t.Fatalf("expired PUT status = %d, want != 2xx within 4s", expStatus)
	}
	if _, err = b.Stat(ctx, keyExp); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(%q) = %v, want ErrNotFound", keyExp, err)
	}
}

func pollUntilExpiredS3PUT(ctx context.Context, t *testing.T, req storage.PresignedRequest, clk clockwork.Clock) int {
	t.Helper()
	expStatus := putPresigned(ctx, t, req, []byte("late payload"))
	for range 8 {
		if expStatus/100 != 2 {
			break
		}
		select {
		case <-clk.After(500 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("context done polling for expiry: %v", ctx.Err())
		}
		expStatus = putPresigned(ctx, t, req, []byte("late payload"))
	}
	return expStatus
}

func assertS3PresignPutDifferentKey(ctx context.Context, t *testing.T, b *storage.S3Bucket, keyA string) {
	t.Helper()
	const keyB = "presign/expiry-key-b"
	reqDiff, err := b.PresignPut(ctx, keyA, time.Minute, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut keyA: %v", err)
	}
	u, err := url.Parse(reqDiff.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	u.Path = strings.Replace(u.Path, keyA, keyB, 1)
	u.RawPath = ""
	reqRewritten := reqDiff
	reqRewritten.URL = u.String()
	if status := putPresigned(ctx, t, reqRewritten, []byte("tampered payload")); status/100 == 2 {
		t.Fatalf("retargeted PUT status = %d, want rejection", status)
	}
	if _, err = b.Stat(ctx, keyB); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(%q) = %v, want ErrNotFound", keyB, err)
	}
}

func putPresigned(ctx context.Context, t *testing.T, req storage.PresignedRequest, body []byte) int {
	t.Helper()
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(httpReq.Header, req.Header)
	// Private transport: httptest.Server.Close resets http.DefaultTransport (#701).
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport, Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func assertS3Body(ctx context.Context, t *testing.T, b storage.Bucket, key string, size int64) {
	t.Helper()
	rc, _, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	got := sha256.New()
	if _, err = io.Copy(got, rc); err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	want := sha256.New()
	if _, err = io.Copy(want, &patterned{remaining: size}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("%q body digest mismatch", key)
	}
}
