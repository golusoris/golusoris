// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/gcs"
	"github.com/golusoris/golusoris/testutil/objstore"
)

func startBucket(t *testing.T, chunkSize int) *gcs.Bucket {
	t.Helper()
	srv := objstore.StartGCS(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	b, err := gcs.New(ctx, gcs.Options{
		Bucket: srv.Bucket, Endpoint: srv.Endpoint, ChunkSize: chunkSize,
	}, clockwork.NewRealClock())
	if err != nil {
		t.Fatalf("gcs.New: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := b.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	return b
}

func TestBucket_Conformance(t *testing.T) {
	t.Parallel()
	// fake-gcs-server accepts any signature, so only the unenforced presign
	// subtests run here; internal tests pin the signed URL shape.
	objstore.RunConformance(t, startBucket(t, 0), objstore.Capabilities{URLFetchable: true})
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

func TestBucket_ResumableUnseekableBody(t *testing.T) {
	t.Parallel()
	b := startBucket(t, gcs.MinChunkSize)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const size = 4*gcs.MinChunkSize + 5
	obj, err := b.Put(ctx, "resumable/stream.bin", &patterned{remaining: size}, storage.PutOptions{
		ContentType: "video/mp4", Metadata: map[string]string{"job": "7"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Size != size || obj.ContentType != "video/mp4" || obj.Metadata["job"] != "7" {
		t.Fatalf("Put object = %+v", obj)
	}
	rc, stat, err := b.Get(ctx, "resumable/stream.bin")
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
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) || stat.Size != size {
		t.Fatalf("body digest mismatch (stat size %d)", stat.Size)
	}
}
