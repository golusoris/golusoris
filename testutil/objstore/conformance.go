// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package objstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
)

const conformanceTimeout = 2 * time.Minute

// Capabilities declares what the backend under test enforces.
type Capabilities struct {
	// PresignEnforced: the server rejects expired or retargeted presigned URLs.
	PresignEnforced bool
	// PresignHeadersEnforced: the server rejects uploads whose signed headers differ.
	PresignHeadersEnforced bool
	// URLFetchable: [storage.Bucket.URL] returns an HTTP URL serving the body.
	URLFetchable bool
}

// RunConformance exercises the backend-neutral [storage.Bucket] contract on b,
// plus [storage.Copier] and [storage.PutPresigner] when b implements them.
// Subtests use disjoint key prefixes, so b may hold unrelated objects.
func RunConformance(t *testing.T, b storage.Bucket, caps Capabilities) {
	t.Helper()
	t.Run("PutGetRoundTrip", func(t *testing.T) { testRoundTrip(t, b) })
	t.Run("EmptyObject", func(t *testing.T) { testEmptyObject(t, b) })
	t.Run("StatExistsDelete", func(t *testing.T) { testStatExistsDelete(t, b) })
	t.Run("MissingKey", func(t *testing.T) { testMissingKey(t, b) })
	t.Run("ListPrefixAndLimit", func(t *testing.T) { testList(t, b) })
	t.Run("UnsafeKeys", func(t *testing.T) { testUnsafeKeys(t, b) })
	if caps.URLFetchable {
		t.Run("URLServesBody", func(t *testing.T) { testURL(t, b) })
	}
	if copier, ok := b.(storage.Copier); ok {
		t.Run("Copy", func(t *testing.T) { testCopy(t, b, copier) })
	}
	if presigner, ok := b.(storage.PutPresigner); ok {
		t.Run("PresignPut", func(t *testing.T) { testPresignPut(t, b, presigner, caps) })
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), conformanceTimeout)
	t.Cleanup(cancel)
	return ctx
}

func testRoundTrip(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	body := []byte("conformance round trip")
	meta := map[string]string{"owner": "alice"}
	obj, err := b.Put(ctx, "roundtrip/a b+c.txt", bytes.NewReader(body), storage.PutOptions{
		ContentType: "text/plain", Metadata: meta,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Key != "roundtrip/a b+c.txt" || obj.Size != int64(len(body)) {
		t.Fatalf("Put object = %+v", obj)
	}
	got, stat := mustGet(ctx, t, b, obj.Key)
	if !bytes.Equal(got, body) {
		t.Fatalf("Get body = %q, want %q", got, body)
	}
	assertAttributes(t, stat, "text/plain", meta)
}

func testEmptyObject(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	if _, err := b.Put(ctx, "empty/zero", bytes.NewReader(nil), storage.PutOptions{}); err != nil {
		t.Fatalf("Put empty: %v", err)
	}
	got, stat := mustGet(ctx, t, b, "empty/zero")
	if len(got) != 0 || stat.Size != 0 {
		t.Fatalf("empty object body=%d size=%d", len(got), stat.Size)
	}
	if stat.ContentType != storage.DefaultContentType {
		t.Fatalf("default content type = %q, want %q", stat.ContentType, storage.DefaultContentType)
	}
}

func testStatExistsDelete(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	key := "stat/object"
	mustPut(ctx, t, b, key, []byte("12345"))
	stat, err := b.Stat(ctx, key)
	if err != nil || stat.Size != 5 || stat.Key != key {
		t.Fatalf("Stat = %+v, %v", stat, err)
	}
	if ok, existsErr := b.Exists(ctx, key); existsErr != nil || !ok {
		t.Fatalf("Exists = %v, %v; want true", ok, existsErr)
	}
	if err = b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, existsErr := b.Exists(ctx, key); existsErr != nil || ok {
		t.Fatalf("Exists after Delete = %v, %v; want false", ok, existsErr)
	}
	if err = b.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete = %v, want nil", err)
	}
}

func testMissingKey(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	if _, err := b.Stat(ctx, "missing/key"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Stat missing = %v, want ErrNotFound", err)
	}
	rc, _, err := b.Get(ctx, "missing/key")
	if rc != nil {
		t.Errorf("Get missing returned a body")
		if closeErr := rc.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
}

func testList(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	for _, key := range []string{"list/a", "list/b", "list/c", "listing-other/d"} {
		mustPut(ctx, t, b, key, []byte(key))
	}
	all, err := b.List(ctx, storage.ListOptions{Prefix: "list/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys := make([]string, 0, len(all))
	for _, obj := range all {
		keys = append(keys, obj.Key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"list/a", "list/b", "list/c"}) {
		t.Fatalf("List keys = %v", keys)
	}
	limited, err := b.List(ctx, storage.ListOptions{Prefix: "list/", Limit: 2})
	if err != nil || len(limited) != 2 {
		t.Fatalf("List limit 2 = %d objects, %v", len(limited), err)
	}
	for _, limit := range []int{-1, storage.MaxListLimit + 1} {
		if _, err = b.List(ctx, storage.ListOptions{Prefix: "list/", Limit: limit}); err == nil {
			t.Fatalf("List limit %d accepted", limit)
		}
	}
}

func testUnsafeKeys(t *testing.T, b storage.Bucket) {
	t.Helper()
	ctx := testContext(t)
	for _, key := range []string{"", "../escape", "a/../b", "/abs", "a//b"} {
		if _, err := b.Put(ctx, key, bytes.NewReader(nil), storage.PutOptions{}); !errors.Is(err, storage.ErrUnsafeKey) {
			t.Fatalf("Put(%q) = %v, want ErrUnsafeKey", key, err)
		}
		if _, err := b.Stat(ctx, key); !errors.Is(err, storage.ErrUnsafeKey) {
			t.Fatalf("Stat(%q) = %v, want ErrUnsafeKey", key, err)
		}
	}
}

func testCopy(t *testing.T, b storage.Bucket, c storage.Copier) {
	t.Helper()
	ctx := testContext(t)
	meta := map[string]string{"stage": "raw"}
	body := []byte("copy me")
	if _, err := b.Put(ctx, "copy/src", bytes.NewReader(body), storage.PutOptions{
		ContentType: "text/plain", Metadata: meta,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	mustPut(ctx, t, b, "copy/dst", []byte("stale destination"))
	obj, err := c.Copy(ctx, "copy/src", "copy/dst")
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if obj.Key != "copy/dst" || obj.Size != int64(len(body)) {
		t.Fatalf("Copy object = %+v", obj)
	}
	got, stat := mustGet(ctx, t, b, "copy/dst")
	if !bytes.Equal(got, body) {
		t.Fatalf("copied body = %q, want %q", got, body)
	}
	assertAttributes(t, stat, "text/plain", meta)
	if _, err = c.Copy(ctx, "copy/missing", "copy/other"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Copy missing = %v, want ErrNotFound", err)
	}
	if _, err = c.Copy(ctx, "copy/src", "copy/src"); !errors.Is(err, storage.ErrCopySameKey) {
		t.Fatalf("Copy onto itself = %v, want ErrCopySameKey", err)
	}
	if _, err = c.Copy(ctx, "copy/src", "../escape"); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("Copy to unsafe key = %v, want ErrUnsafeKey", err)
	}
}

func mustPut(ctx context.Context, t *testing.T, b storage.Bucket, key string, body []byte) {
	t.Helper()
	if _, err := b.Put(ctx, key, bytes.NewReader(body), storage.PutOptions{}); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func mustGet(ctx context.Context, t *testing.T, b storage.Bucket, key string) ([]byte, storage.Object) {
	t.Helper()
	rc, obj, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			t.Errorf("close %q: %v", key, closeErr)
		}
	}()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return body, obj
}

func assertAttributes(t *testing.T, obj storage.Object, contentType string, meta map[string]string) {
	t.Helper()
	if obj.ContentType != contentType {
		t.Fatalf("content type = %q, want %q", obj.ContentType, contentType)
	}
	if !maps.Equal(lowerKeys(obj.Metadata), lowerKeys(meta)) {
		t.Fatalf("metadata = %v, want %v", obj.Metadata, meta)
	}
}

// lowerKeys folds metadata names: S3 and Azure return them canonicalised.
func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}
