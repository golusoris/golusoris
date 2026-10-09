// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign_test

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/container/registry/sign"
)

// layoutImage writes a random image (or index) into a new layout with
// go-containerregistry's layout package, the way VMAFx builds one.
func layoutImage(t *testing.T, index bool) (*registry.Layout, string, v1.Hash) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "oci")
	p, err := layout.Write(dir, empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	var h v1.Hash
	if index {
		idx, rerr := random.Index(64, 1, 2)
		if rerr != nil {
			t.Fatal(rerr)
		}
		h, _ = idx.Digest()
		err = p.AppendIndex(idx)
	} else {
		img, rerr := random.Image(64, 1)
		if rerr != nil {
			t.Fatal(rerr)
		}
		h, _ = img.Digest()
		err = p.AppendImage(img)
	}
	if err != nil {
		t.Fatal(err)
	}
	l, err := registry.OpenLayout(dir, registry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return l, dir, h
}

func flipFile(t *testing.T, p string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xFF
	if err = os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func blobFile(dir string, h v1.Hash) string { return filepath.Join(dir, "blobs", h.Algorithm, h.Hex) }

func wantLayoutRejected(t *testing.T, l *registry.Layout, h v1.Hash, p sign.Policy, reason string) {
	t.Helper()
	got, err := sign.VerifyLayout(testCtx(t), l, h, p)
	if !errors.Is(err, sign.ErrNoValidSignature) || !strings.Contains(err.Error(), reason) || got != nil {
		t.Fatalf("VerifyLayout = %v, %v; want ErrNoValidSignature naming %q", got, err, reason)
	}
}

// TestImageLayout_SignsOffline signs in a layout with every key type and
// checks the bundle the way `cosign verify --key` does, then with
// VerifyLayout.
func TestImageLayout_SignsOffline(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		key   crypto.Signer
		index bool
	}{
		{name: "ecdsa-p256", key: ecKey(t)},
		{name: "ecdsa-p384 index", key: p384, index: true},
		{name: "rsa-2048", key: rsaKey},
		{name: "ed25519", key: edKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, _, h := layoutImage(t, tc.index)
			at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
			sig, err := sign.ImageLayout(testCtx(t), l, h, sign.Signer{Key: tc.key}, sign.Options{Clock: clockwork.NewFakeClockAt(at)})
			if err != nil {
				t.Fatalf("ImageLayout: %v", err)
			}
			if sig.Subject.Digest != h || (sig.Subject.MediaType.IsIndex() != tc.index) {
				t.Fatalf("subject = %+v", sig.Subject)
			}
			if _, err = verifyWithKey(t, sig.Bundle, tc.key.Public(), h); err != nil {
				t.Fatalf("cosign --key verification: %v", err)
			}
			checkLayoutReferrer(t, l, sig, at)
			got, err := sign.VerifyLayout(testCtx(t), l, h, keyPolicy(tc.key.Public()))
			if err != nil || len(got) != 1 || got[0].Descriptor.Digest != sig.Descriptor.Digest || got[0].Result == nil {
				t.Fatalf("VerifyLayout = %+v, %v", got, err)
			}
		})
	}
}

// checkLayoutReferrer pins the referrer to the manifest Image pushes.
func checkLayoutReferrer(t *testing.T, l *registry.Layout, sig sign.Signature, at time.Time) {
	t.Helper()
	ctx := testCtx(t)
	refs, err := l.Referrers(ctx, sig.Subject.Digest, sign.BundleMediaType)
	if err != nil || len(refs) != 1 || refs[0].Digest != sig.Descriptor.Digest {
		t.Fatalf("layout referrers = %+v, %v", refs, err)
	}
	desc, man, err := l.ArtifactManifest(ctx, sig.Descriptor.Digest)
	if err != nil || desc.ArtifactType != sign.BundleMediaType || man.Config.MediaType != registry.EmptyJSONMediaType {
		t.Fatalf("referrer = %+v, %+v, %v", desc, man, err)
	}
	if len(man.Layers) != 1 || man.Layers[0].MediaType != sign.BundleMediaType || man.Subject == nil ||
		man.Subject.Digest != sig.Subject.Digest || man.Subject.MediaType != sig.Subject.MediaType {
		t.Fatalf("referrer manifest = %+v", man)
	}
	want := map[string]string{
		sign.AnnotationCreated:       at.Format(time.RFC3339),
		sign.AnnotationBundleContent: "dsse-envelope",
		sign.AnnotationPredicateType: sign.PredicateType,
	}
	for k, v := range want {
		if man.Annotations[k] != v {
			t.Fatalf("annotation %s = %q, want %q", k, man.Annotations[k], v)
		}
	}
	if blob, err := l.FetchBlob(ctx, man.Layers[0], 1<<20); err != nil || !bytes.Equal(blob, sig.Bundle) {
		t.Fatalf("bundle layer = %q, %v", blob, err)
	}
}

// TestLayout_SignedRoundTrip carries signatures both ways: signed in a
// layout and verified there, pushed to a registry and verified by Verify;
// signed in a registry, copied into a layout and verified there offline.
func TestLayout_SignedRoundTrip(t *testing.T) {
	t.Parallel()
	for _, api := range []bool{false, true} {
		t.Run(map[bool]string{false: "tag-schema", true: "referrers-api"}[api], func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			key := ecKey(t)
			l, _, h := layoutImage(t, true)
			sig, err := sign.ImageLayout(ctx, l, h, sign.Signer{Key: key}, sign.Options{})
			if err != nil {
				t.Fatalf("ImageLayout: %v", err)
			}
			verifyLayoutOne(t, l, h, keyPolicy(key.Public()))

			host := newRegistry(t, nil, regsrv.WithReferrersSupport(api))
			c := newClient(t)
			if _, err = c.CopyFromLayout(ctx, l, h, host+"/app:v1"); err != nil {
				t.Fatalf("CopyFromLayout: %v", err)
			}
			ref := host + "/app@" + h.String()
			checkReferrer(t, host, ref, sig, mustCreated(t, l, sig))
			if got := verifyOne(t, c, ref, keyPolicy(key.Public())); got.Descriptor.Digest != sig.Descriptor.Digest {
				t.Fatalf("Verify after push = %+v", got.Descriptor)
			}

			remoteRef, digest := seed(t, host, false)
			remoteSig, err := sign.Image(ctx, c, remoteRef, sign.Signer{Key: key}, sign.Options{})
			if err != nil {
				t.Fatalf("Image: %v", err)
			}
			back, err := registry.CreateLayout(filepath.Join(t.TempDir(), "back"), registry.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.CopyToLayout(ctx, remoteRef, back); err != nil {
				t.Fatalf("CopyToLayout: %v", err)
			}
			if got := verifyLayoutOne(t, back, digest, keyPolicy(key.Public())); got.Descriptor.Digest != remoteSig.Descriptor.Digest {
				t.Fatalf("VerifyLayout after pull = %+v", got.Descriptor)
			}
		})
	}
}

func verifyLayoutOne(t *testing.T, l *registry.Layout, h v1.Hash, p sign.Policy) sign.VerifiedSignature {
	t.Helper()
	got, err := sign.VerifyLayout(testCtx(t), l, h, p)
	if err != nil || len(got) != 1 {
		t.Fatalf("VerifyLayout = %d signatures, %v; want 1", len(got), err)
	}
	return got[0]
}

func mustCreated(t *testing.T, l *registry.Layout, sig sign.Signature) time.Time {
	t.Helper()
	_, man, err := l.ArtifactManifest(testCtx(t), sig.Descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, man.Annotations[sign.AnnotationCreated])
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestVerifyLayout_RejectsTampered(t *testing.T) {
	t.Parallel()
	key := ecKey(t)
	signed := func(t *testing.T) (*registry.Layout, string, v1.Hash, sign.Signature) {
		t.Helper()
		l, dir, h := layoutImage(t, false)
		sig, err := sign.ImageLayout(testCtx(t), l, h, sign.Signer{Key: key}, sign.Options{})
		if err != nil {
			t.Fatal(err)
		}
		return l, dir, h, sig
	}
	t.Run("bundle blob", func(t *testing.T) {
		t.Parallel()
		l, dir, h, sig := signed(t)
		_, man, err := l.ArtifactManifest(testCtx(t), sig.Descriptor.Digest)
		if err != nil {
			t.Fatal(err)
		}
		flipFile(t, blobFile(dir, man.Layers[0].Digest))
		wantLayoutRejected(t, l, h, keyPolicy(key.Public()), "digest mismatch")
	})
	t.Run("subject manifest", func(t *testing.T) {
		t.Parallel()
		l, dir, h, _ := signed(t)
		flipFile(t, blobFile(dir, h))
		if _, err := sign.VerifyLayout(testCtx(t), l, h, keyPolicy(key.Public())); !errors.Is(err, registry.ErrDigestMismatch) {
			t.Fatalf("VerifyLayout err = %v, want ErrDigestMismatch", err)
		}
	})
	t.Run("signature of another image", func(t *testing.T) {
		t.Parallel()
		l, _, h, sig := signed(t)
		other, _, oh := layoutImage(t, false)
		ctx := testCtx(t)
		m, err := other.Manifest(ctx, oh)
		if err != nil {
			t.Fatal(err)
		}
		subject := v1.Descriptor{MediaType: m.MediaType, Digest: oh, Size: m.Size}
		if _, err = other.PushArtifact(ctx, registry.Artifact{
			ArtifactType: sign.BundleMediaType, Subject: &subject,
			Blobs: []registry.Blob{{MediaType: sign.BundleMediaType, Reader: bytes.NewReader(sig.Bundle)}},
		}); err != nil {
			t.Fatal(err)
		}
		wantLayoutRejected(t, other, oh, keyPolicy(key.Public()), "verify bundle")
		verifyLayoutOne(t, l, h, keyPolicy(key.Public()))
	})
	t.Run("other key", func(t *testing.T) {
		t.Parallel()
		l, _, h, _ := signed(t)
		wantLayoutRejected(t, l, h, keyPolicy(ecKey(t).Public()), "verify bundle")
	})
	t.Run("unsigned", func(t *testing.T) {
		t.Parallel()
		l, _, h := layoutImage(t, false)
		wantLayoutRejected(t, l, h, keyPolicy(key.Public()), "has no Sigstore bundle referrers")
	})
}

func TestImageLayout_RejectsBeforeIO(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	key := ecKey(t)
	l, _, h := layoutImage(t, false)
	good := sign.Signer{Key: key}
	cases := []struct {
		name string
		err  error
		fn   func() error
	}{
		{"sign nil layout", sign.ErrInvalidOptions, func() error { _, err := sign.ImageLayout(ctx, nil, h, good, sign.Options{}); return err }},
		{"sign zero digest", sign.ErrDigestRequired, func() error { _, err := sign.ImageLayout(ctx, l, v1.Hash{}, good, sign.Options{}); return err }},
		{"sign no key", sign.ErrInvalidOptions, func() error { _, err := sign.ImageLayout(ctx, l, h, sign.Signer{}, sign.Options{}); return err }},
		{"sign bad digest", registry.ErrInvalidArtifact, func() error {
			_, err := sign.ImageLayout(ctx, l, v1.Hash{Algorithm: "sha256", Hex: "../index.json"}, good, sign.Options{})
			return err
		}},
		{"sign missing digest", fs.ErrNotExist, func() error {
			_, err := sign.ImageLayout(ctx, l, v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("d", 64)}, good, sign.Options{})
			return err
		}},
		{"verify nil layout", sign.ErrInvalidOptions, func() error { _, err := sign.VerifyLayout(ctx, nil, h, keyPolicy(key.Public())); return err }},
		{"verify zero digest", sign.ErrDigestRequired, func() error { _, err := sign.VerifyLayout(ctx, l, v1.Hash{}, keyPolicy(key.Public())); return err }},
		{"verify empty policy", sign.ErrInvalidOptions, func() error { _, err := sign.VerifyLayout(ctx, l, h, sign.Policy{}); return err }},
	}
	for _, tc := range cases {
		if err := tc.fn(); !errors.Is(err, tc.err) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.err)
		}
	}
	if refs, err := l.Referrers(ctx, h, ""); err != nil || len(refs) != 0 {
		t.Fatalf("rejected calls wrote referrers: %v, %v", refs, err)
	}
}
