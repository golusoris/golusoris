// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package objstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
)

const (
	httpTimeout = 30 * time.Second
	// expiryGrace absorbs whole-second expiry rounding on the server.
	expiryGrace = 1500 * time.Millisecond
)

func testURL(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	mustPut(ctx, t, b, "url/object", []byte("served"))
	signed, err := b.URL(ctx, "url/object")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	status, body := send(ctx, t, http.MethodGet, signed, nil, nil)
	if status != http.StatusOK || string(body) != "served" {
		t.Fatalf("GET URL = %d %q", status, body)
	}
}

func testPresignPut(t *testing.T, b storage.Bucket, p storage.PutPresigner, caps Capabilities) {
	t.Helper()
	t.Run("UploadReadableThroughGet", func(t *testing.T) { testPresignUpload(t, b, p) })
	t.Run("TTLBounds", func(t *testing.T) { testPresignTTLBounds(t, p) })
	t.Run("InvalidOptions", func(t *testing.T) { testPresignInvalidOptions(t, p) })
	if caps.PresignEnforced {
		t.Run("OtherKeyRejected", func(t *testing.T) { testPresignOtherKey(t, b, p) })
		t.Run("ExpiredRejected", func(t *testing.T) { testPresignExpired(t, b, p) })
	}
	if caps.PresignHeadersEnforced {
		t.Run("HeaderMismatchRejected", func(t *testing.T) { testPresignHeaderMismatch(t, p) })
	}
}

func testPresignUpload(t *testing.T, b storage.Bucket, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	body := []byte("uploaded by an untrusted client")
	meta := map[string]string{"job": "42"}
	req, err := p.PresignPut(ctx, "presign/upload.bin", time.Minute, storage.PresignPutOptions{
		ContentType: "video/mp4", ContentLength: int64(len(body)), Metadata: meta,
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	if req.Method != http.MethodPut || req.Expires.Before(time.Now()) || req.Expires.After(time.Now().Add(2*time.Minute)) {
		t.Fatalf("presigned request = %s expires %s", req.Method, req.Expires)
	}
	if status := sendPresigned(ctx, t, req.URL, req.Header, body); status/100 != 2 {
		t.Fatalf("presigned PUT status = %d", status)
	}
	got, stat := mustGet(ctx, t, b, "presign/upload.bin")
	if !bytes.Equal(got, body) {
		t.Fatalf("uploaded body = %q, want %q", got, body)
	}
	assertAttributes(t, stat, "video/mp4", meta)
}

func testPresignTTLBounds(t *testing.T, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	for _, ttl := range []time.Duration{0, storage.MinPresignTTL - 1, storage.MaxPresignTTL + time.Second} {
		if _, err := p.PresignPut(ctx, "presign/ttl", ttl, storage.PresignPutOptions{}); !errors.Is(err, storage.ErrPresignTTL) {
			t.Fatalf("PresignPut ttl %s = %v, want ErrPresignTTL", ttl, err)
		}
	}
	for _, ttl := range []time.Duration{storage.MinPresignTTL, storage.MaxPresignTTL} {
		if _, err := p.PresignPut(ctx, "presign/ttl", ttl, storage.PresignPutOptions{}); err != nil {
			t.Fatalf("PresignPut ttl %s: %v", ttl, err)
		}
	}
}

func testPresignInvalidOptions(t *testing.T, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	if _, err := p.PresignPut(ctx, "../escape", time.Minute, storage.PresignPutOptions{}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("PresignPut unsafe key = %v, want ErrUnsafeKey", err)
	}
	invalid := []storage.PresignPutOptions{
		{ContentLength: -1},
		{Metadata: map[string]string{"bad name": "v"}},
		{Checksum: storage.Checksum{Algorithm: storage.ChecksumMD5, Value: []byte{1}}},
		{Checksum: storage.Checksum{Algorithm: "whirlpool", Value: []byte{1}}},
	}
	for _, opts := range invalid {
		if _, err := p.PresignPut(ctx, "presign/invalid", time.Minute, opts); err == nil {
			t.Fatalf("PresignPut(%+v) accepted", opts)
		}
	}
}

func testPresignOtherKey(t *testing.T, b storage.Bucket, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	req, err := p.PresignPut(ctx, "presign/target", time.Minute, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	u.Path = strings.Replace(u.Path, "presign/target", "presign/hijack", 1)
	u.RawPath = ""
	if status := sendPresigned(ctx, t, u.String(), req.Header, []byte("x")); status/100 == 2 {
		t.Fatalf("retargeted presigned PUT status = %d, want rejection", status)
	}
	if ok, existsErr := b.Exists(ctx, "presign/hijack"); existsErr != nil || ok {
		t.Fatalf("retargeted key exists = %v, %v", ok, existsErr)
	}
}

func testPresignExpired(t *testing.T, b storage.Bucket, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	req, err := p.PresignPut(ctx, "presign/expired", storage.MinPresignTTL, storage.PresignPutOptions{})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	time.Sleep(time.Until(req.Expires) + expiryGrace)
	if status := sendPresigned(ctx, t, req.URL, req.Header, []byte("late")); status/100 == 2 {
		t.Fatalf("expired presigned PUT status = %d, want rejection", status)
	}
	if ok, existsErr := b.Exists(ctx, "presign/expired"); existsErr != nil || ok {
		t.Fatalf("expired upload exists = %v, %v", ok, existsErr)
	}
}

func testPresignHeaderMismatch(t *testing.T, p storage.PutPresigner) {
	t.Helper()
	ctx := testContext(t)
	req, err := p.PresignPut(ctx, "presign/typed", time.Minute, storage.PresignPutOptions{
		ContentType: "text/plain", ContentLength: 5,
	})
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	wrongType := req.Header.Clone()
	wrongType.Set("Content-Type", "application/json")
	if status := sendPresigned(ctx, t, req.URL, wrongType, []byte("12345")); status/100 == 2 {
		t.Fatalf("content-type mismatch status = %d, want rejection", status)
	}
	if status := sendPresigned(ctx, t, req.URL, req.Header, []byte("123456")); status/100 == 2 {
		t.Fatalf("content-length mismatch status = %d, want rejection", status)
	}
}

// sendPresigned PUTs body; Go derives Content-Length from body, so a signed
// length that differs from len(body) reaches the server as a mismatch.
func sendPresigned(ctx context.Context, t *testing.T, target string, header http.Header, body []byte) int {
	t.Helper()
	status, _ := send(ctx, t, http.MethodPut, target, header, body)
	return status
}

func send(ctx context.Context, t *testing.T, method, target string, header http.Header, body []byte) (int, []byte) {
	t.Helper()
	httpReq, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	for name, values := range header {
		if http.CanonicalHeaderKey(name) == "Content-Length" {
			continue
		}
		httpReq.Header[http.CanonicalHeaderKey(name)] = values
	}
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close response: %v", closeErr)
		}
	}()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, respBody
}
