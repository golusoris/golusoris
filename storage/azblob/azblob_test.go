// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Content-MD5 is Azure's transport integrity header, not a security primitive.
	"crypto/sha256"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/storage"
	"github.com/golusoris/golusoris/storage/azblob"
	"github.com/golusoris/golusoris/testutil/objstore"
)

const testContainer = "conformance"

func startBucket(t *testing.T, opts azblob.Options) *azblob.Bucket {
	t.Helper()
	srv := objstore.StartAzurite(t)
	cred, err := container.NewSharedKeyCredential(srv.AccountName, srv.AccountKey)
	if err != nil {
		t.Fatal(err)
	}
	client, err := container.NewClientWithSharedKeyCredential(srv.ServiceURL+testContainer, cred, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err = client.Create(ctx, nil); err != nil {
		t.Fatalf("create container: %v", err)
	}
	opts.ServiceURL, opts.Container = srv.ServiceURL, testContainer
	opts.AccountName, opts.AccountKey = srv.AccountName, srv.AccountKey
	b, err := azblob.New(opts, clockwork.NewRealClock())
	if err != nil {
		t.Fatalf("azblob.New: %v", err)
	}
	return b
}

func TestBucket_Conformance(t *testing.T) {
	t.Parallel()
	objstore.RunConformance(t, startBucket(t, azblob.Options{}), objstore.Capabilities{
		PresignEnforced: true, URLFetchable: true,
	})
}

// patterned is a deterministic, unseekable body: Put cannot learn its length.
type patterned struct{ remaining, offset int64 }

func (p *patterned) Read(buf []byte) (int, error) {
	if p.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(buf)), p.remaining)
	for i := range n {
		buf[i] = byte((p.offset + i) % 251)
	}
	p.remaining -= n
	p.offset += n
	return int(n), nil
}

func TestBucket_StagedBlocksUnseekableBody(t *testing.T) {
	t.Parallel()
	b := startBucket(t, azblob.Options{BlockSize: azblob.MinBlockSize, Concurrency: 2})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const size = 3*azblob.MinBlockSize + 7
	obj, err := b.Put(ctx, "blocks/stream.bin", &patterned{remaining: size}, storage.PutOptions{
		ContentType: "video/mp4", Metadata: map[string]string{"job": "7"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != size {
		t.Fatalf("Put size = %d, want %d", obj.Size, size)
	}
	rc, stat, err := b.Get(ctx, "blocks/stream.bin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got := sha256.New()
	if _, err = io.Copy(got, rc); err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	_, _ = io.Copy(want, &patterned{remaining: size})
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) || stat.Size != size || stat.Metadata["job"] != "7" {
		t.Fatalf("Get = %+v, digest match %v", stat, bytes.Equal(got.Sum(nil), want.Sum(nil)))
	}
}

func TestBucket_PresignPutMD5Enforced(t *testing.T) {
	t.Parallel()
	b := startBucket(t, azblob.Options{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	body := []byte("checksummed upload")
	sum := md5.Sum(body) //nolint:gosec // see import.
	req, err := b.PresignPut(ctx, "checksum/md5", time.Minute, storage.PresignPutOptions{
		Checksum: storage.Checksum{Algorithm: storage.ChecksumMD5, Value: sum[:]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := put(ctx, t, req, []byte("tampered upload!!!")); status/100 == 2 {
		t.Fatalf("tampered body status = %d, want rejection", status)
	}
	if status := put(ctx, t, req, body); status/100 != 2 {
		t.Fatalf("matching body status = %d", status)
	}
}

func TestBucket_PresignPutExpiryAndDifferentKey(t *testing.T) {
	t.Parallel()
	b := startBucket(t, azblob.Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	clk := clockwork.NewRealClock()

	// Positive: PUT within TTL = 2xx.
	const keyA = "presign/expiry-key-a"
	reqPos, err := b.PresignPut(ctx, keyA, storage.MinPresignTTL, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut positive: %v", err)
	}
	if status := put(ctx, t, reqPos, []byte("positive payload")); status/100 != 2 {
		t.Fatalf("positive PUT status = %d, want 2xx", status)
	}
	stat, err := b.Stat(ctx, keyA)
	if err != nil || stat.Size != int64(len("positive payload")) {
		t.Fatalf("Stat(%q) = %+v, %v", keyA, stat, err)
	}

	// Expiry: new presign, wait past TTL -> PUT != 2xx.
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
	expStatus := put(ctx, t, reqExp, []byte("late payload"))
	for range 8 {
		if expStatus/100 != 2 {
			break
		}
		select {
		case <-clk.After(500 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("context done polling for expiry: %v", ctx.Err())
		}
		expStatus = put(ctx, t, reqExp, []byte("late payload"))
	}
	if expStatus/100 == 2 {
		t.Fatalf("expired PUT status = %d, want != 2xx within 4s", expStatus)
	}
	if _, err = b.Stat(ctx, keyExp); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(%q) = %v, want ErrNotFound", keyExp, err)
	}

	// Different key: presign for key A, rewrite URL path to key B -> PUT != 2xx and key B absent.
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
	if status := put(ctx, t, reqRewritten, []byte("tampered payload")); status/100 == 2 {
		t.Fatalf("retargeted PUT status = %d, want rejection", status)
	}
	if _, err = b.Stat(ctx, keyB); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat(%q) = %v, want ErrNotFound", keyB, err)
	}
}

func put(ctx context.Context, t *testing.T, req storage.PresignedRequest, body []byte) int {
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
