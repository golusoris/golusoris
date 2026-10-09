// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/google/go-containerregistry/pkg/v1/validate"

	"github.com/golusoris/golusoris/container/registry"
)

const sigType = "application/vnd.dev.sigstore.bundle.v0.3+json"

func newLayout(t *testing.T) (*registry.Layout, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "oci")
	l, err := registry.CreateLayout(dir, registry.Options{})
	if err != nil {
		t.Fatalf("CreateLayout: %v", err)
	}
	return l, dir
}

func blobPath(dir string, h v1.Hash) string {
	return filepath.Join(dir, "blobs", h.Algorithm, h.Hex)
}

// flip corrupts one byte of a file in place; the size stays the same.
func flip(t *testing.T, p string) {
	t.Helper()
	b := mustRead(t, p)
	b[len(b)/2] ^= 0xFF
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func indexEntries(t *testing.T, dir string) []v1.Descriptor {
	t.Helper()
	im, err := v1.ParseIndexManifest(bytes.NewReader(mustRead(t, filepath.Join(dir, "index.json"))))
	if err != nil {
		t.Fatalf("index.json: %v", err)
	}
	return im.Manifests
}

// pushModel writes a model artifact plus one bundle-typed referrer of it.
func pushModel(t *testing.T, l *registry.Layout) (model, sig v1.Descriptor) {
	t.Helper()
	ctx := testCtx(t)
	model, err := l.PushArtifact(ctx, registry.Artifact{
		ArtifactType: modelType,
		Blobs:        []registry.Blob{{Name: "model.onnx", Reader: strings.NewReader("weights")}},
	})
	if err != nil {
		t.Fatalf("push model: %v", err)
	}
	sig, err = l.PushArtifact(ctx, registry.Artifact{
		ArtifactType: sigType,
		Blobs:        []registry.Blob{{MediaType: sigType, Reader: strings.NewReader(`{"mediaType":"bundle"}`)}},
		Annotations:  map[string]string{"dev.sigstore.bundle.content": "dsse-envelope"},
		Subject:      &model,
	})
	if err != nil {
		t.Fatalf("push referrer: %v", err)
	}
	return model, sig
}

func TestLayout_CreateAndOpen(t *testing.T) {
	t.Parallel()
	l, dir := newLayout(t)
	if l == nil || len(indexEntries(t, dir)) != 0 {
		t.Fatal("new layout is not empty")
	}
	if got := string(mustRead(t, filepath.Join(dir, "oci-layout"))); !strings.Contains(got, `"imageLayoutVersion":"1.0.0"`) {
		t.Fatalf("oci-layout = %s", got)
	}
	if _, err := registry.CreateLayout(dir, registry.Options{}); err != nil {
		t.Fatalf("CreateLayout on existing layout: %v", err)
	}
	if _, err := registry.OpenLayout(dir, registry.Options{}); err != nil {
		t.Fatalf("OpenLayout: %v", err)
	}
	ggcr, err := layout.Write(filepath.Join(t.TempDir(), "ggcr"), empty.Index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.OpenLayout(string(ggcr), registry.Options{}); err != nil {
		t.Fatalf("OpenLayout on a go-containerregistry layout: %v", err)
	}
	size := int64(len(mustRead(t, filepath.Join(dir, "index.json"))))
	if _, err = registry.OpenLayout(dir, registry.Options{MaxManifestBytes: size}); err != nil {
		t.Fatalf("index.json at MaxManifestBytes: %v", err)
	}
	if _, err = registry.OpenLayout(dir, registry.Options{MaxManifestBytes: size - 1}); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("index.json over MaxManifestBytes err = %v", err)
	}
}

func TestLayout_OpenRejects(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := registry.OpenLayout(missing, registry.Options{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("missing dir err = %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("OpenLayout created the directory")
	}
	busy := t.TempDir()
	if err := os.WriteFile(filepath.Join(busy, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CreateLayout(busy, registry.Options{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("non-empty dir err = %v", err)
	}
	_, dir := newLayout(t)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.OpenLayout(dir, registry.Options{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("garbage index.json err = %v", err)
	}
	if _, err := registry.CreateLayout(dir, registry.Options{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("CreateLayout over garbage index.json err = %v", err)
	}
	_, bare := newLayout(t)
	if err := os.Remove(filepath.Join(bare, "oci-layout")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.OpenLayout(bare, registry.Options{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("layout without oci-layout err = %v", err)
	}
}

func TestLayout_ArtifactAndReferrers(t *testing.T) {
	t.Parallel()
	l, dir := newLayout(t)
	ctx := testCtx(t)
	model, sig := pushModel(t, l)

	host := newArtifactRegistry(t, nil)
	remote, err := artifactClient(t, registry.Options{}, nil).PushArtifact(ctx, host+"/m", registry.Artifact{
		ArtifactType: modelType,
		Blobs:        []registry.Blob{{Name: "model.onnx", Reader: strings.NewReader("weights")}},
	})
	if err != nil || remote.Digest != model.Digest {
		t.Fatalf("registry push = %v, %v; layout pushed %s", remote.Digest, err, model.Digest)
	}

	refs, err := l.Referrers(ctx, model.Digest, "")
	if err != nil || len(refs) != 1 {
		t.Fatalf("Referrers = %+v, %v", refs, err)
	}
	if r := refs[0]; r.Digest != sig.Digest || r.ArtifactType != sigType || r.MediaType != types.OCIManifestSchema1 ||
		r.Annotations["dev.sigstore.bundle.content"] != "dsse-envelope" || r.Size != sig.Size {
		t.Fatalf("referrer = %+v", r)
	}
	if got, _ := l.Referrers(ctx, model.Digest, sigType); len(got) != 1 {
		t.Fatalf("filtered referrers = %v", got)
	}
	if got, _ := l.Referrers(ctx, model.Digest, scoreType); len(got) != 0 {
		t.Fatalf("artifactType filter leaked %v", got)
	}
	if got, _ := l.Referrers(ctx, sig.Digest, ""); len(got) != 0 {
		t.Fatalf("referrer has referrers: %v", got)
	}

	m, err := l.Manifest(ctx, model.Digest)
	if err != nil || m.MediaType != types.OCIManifestSchema1 || m.Digest != model.Digest || m.Size != int64(len(m.Raw)) {
		t.Fatalf("Manifest = %+v, %v", m, err)
	}
	desc, man, err := l.ArtifactManifest(ctx, sig.Digest)
	if err != nil || desc.ArtifactType != sigType || man.Subject == nil || man.Subject.Digest != model.Digest || len(man.Layers) != 1 {
		t.Fatalf("ArtifactManifest = %+v, %+v, %v", desc, man, err)
	}
	layer := man.Layers[0]
	if got, ferr := l.FetchBlob(ctx, layer, layer.Size); ferr != nil || string(got) != `{"mediaType":"bundle"}` {
		t.Fatalf("FetchBlob at maxBytes = %q, %v", got, ferr)
	}
	if _, err = l.FetchBlob(ctx, layer, layer.Size-1); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("FetchBlob over maxBytes err = %v", err)
	}

	if _, err = l.PushArtifact(ctx, registry.Artifact{
		ArtifactType: sigType, Annotations: map[string]string{"dev.sigstore.bundle.content": "dsse-envelope"},
		Blobs: []registry.Blob{{MediaType: sigType, Reader: strings.NewReader(`{"mediaType":"bundle"}`)}}, Subject: &model,
	}); err != nil {
		t.Fatalf("re-push: %v", err)
	}
	if n := len(indexEntries(t, dir)); n != 2 {
		t.Fatalf("index.json has %d entries after re-push, want 2", n)
	}
	// Another tool lists the referrer twice and adds one typed by its config.
	p := layout.Path(dir)
	if err = p.AppendDescriptor(v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: sig.Digest, Size: sig.Size}); err != nil {
		t.Fatal(err)
	}
	legacy, ok := mutate.Subject(mutate.ConfigMediaType(empty.Image, scoreType), model).(v1.Image)
	if !ok {
		t.Fatal("mutate.Subject returned no image")
	}
	if aerr := p.AppendImage(legacy); aerr != nil {
		t.Fatal(aerr)
	}
	if got, rerr := l.Referrers(ctx, model.Digest, ""); rerr != nil || len(got) != 2 {
		t.Fatalf("referrers with a duplicate entry = %+v, %v", got, rerr)
	}
	if got, rerr := l.Referrers(ctx, model.Digest, scoreType); rerr != nil || len(got) != 1 {
		t.Fatalf("referrer typed by config media type = %+v, %v", got, rerr)
	}
}

func TestLayout_ReferrersCap(t *testing.T) {
	t.Parallel()
	l, dir := newLayout(t)
	ctx := testCtx(t)
	model, _ := pushModel(t, l)
	one, err := registry.OpenLayout(dir, registry.Options{MaxReferrers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got, rerr := one.Referrers(ctx, model.Digest, ""); rerr != nil || len(got) != 1 {
		t.Fatalf("referrers at cap = %v, %v", got, rerr)
	}
	if _, err = l.PushArtifact(ctx, registry.Artifact{ArtifactType: scoreType, Subject: &model}); err != nil {
		t.Fatal(err)
	}
	if _, err = one.Referrers(ctx, model.Digest, ""); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("referrers over cap err = %v", err)
	}
	if got, err := one.Referrers(ctx, model.Digest, scoreType); err != nil || len(got) != 1 {
		t.Fatalf("filtered referrers at cap = %v, %v", got, err)
	}
}

func TestLayout_RejectsTamperedContent(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	cases := map[string]func(t *testing.T, l *registry.Layout, dir string, model, sig v1.Descriptor){
		"blob flipped": func(t *testing.T, l *registry.Layout, dir string, _, sig v1.Descriptor) {
			t.Helper()
			_, man, err := l.ArtifactManifest(ctx, sig.Digest)
			if err != nil {
				t.Fatal(err)
			}
			flip(t, blobPath(dir, man.Layers[0].Digest))
			if _, err = l.FetchBlob(ctx, man.Layers[0], 1<<20); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("FetchBlob err = %v", err)
			}
		},
		"blob truncated": func(t *testing.T, l *registry.Layout, dir string, _, sig v1.Descriptor) {
			t.Helper()
			_, man, _ := l.ArtifactManifest(ctx, sig.Digest)
			p := blobPath(dir, man.Layers[0].Digest)
			if err := os.WriteFile(p, mustRead(t, p)[1:], 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := l.FetchBlob(ctx, man.Layers[0], 1<<20); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("FetchBlob err = %v", err)
			}
		},
		"blob extended": func(t *testing.T, l *registry.Layout, dir string, _, sig v1.Descriptor) {
			t.Helper()
			_, man, _ := l.ArtifactManifest(ctx, sig.Digest)
			p := blobPath(dir, man.Layers[0].Digest)
			if err := os.WriteFile(p, append(mustRead(t, p), '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := l.FetchBlob(ctx, man.Layers[0], 1<<20); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("FetchBlob err = %v", err)
			}
		},
		"subject manifest": func(t *testing.T, l *registry.Layout, dir string, model, _ v1.Descriptor) {
			t.Helper()
			flip(t, blobPath(dir, model.Digest))
			if _, err := l.Manifest(ctx, model.Digest); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("Manifest err = %v", err)
			}
			if _, _, err := l.ArtifactManifest(ctx, model.Digest); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("ArtifactManifest err = %v", err)
			}
		},
		"referrer manifest": func(t *testing.T, l *registry.Layout, dir string, model, sig v1.Descriptor) {
			t.Helper()
			flip(t, blobPath(dir, sig.Digest))
			if _, err := l.Referrers(ctx, model.Digest, ""); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("Referrers err = %v", err)
			}
		},
		"blob missing": func(t *testing.T, l *registry.Layout, dir string, model, _ v1.Descriptor) {
			t.Helper()
			if err := os.Remove(blobPath(dir, model.Digest)); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Manifest(ctx, model.Digest); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Manifest err = %v", err)
			}
		},
		"blob symlinked": func(t *testing.T, l *registry.Layout, dir string, model, _ v1.Descriptor) {
			t.Helper()
			p := blobPath(dir, model.Digest)
			target := tempFile(t, "elsewhere", mustRead(t, p))
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Skipf("symlink: %v", err)
			}
			if _, err := l.Manifest(ctx, model.Digest); err == nil {
				t.Fatal("Manifest followed a symlinked blob")
			}
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l, dir := newLayout(t)
			model, sig := pushModel(t, l)
			tc(t, l, dir, model, sig)
		})
	}
}

func TestLayout_RejectsBadDigests(t *testing.T) {
	t.Parallel()
	l, _ := newLayout(t)
	ctx := testCtx(t)
	bad := []v1.Hash{
		{Algorithm: "sha256", Hex: "../../../../etc/passwd"},
		{Algorithm: "sha512", Hex: strings.Repeat("a", 128)},
		{Algorithm: "sha256", Hex: strings.Repeat("A", 64)},
		{},
	}
	for _, h := range bad {
		if _, err := l.Manifest(ctx, h); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("Manifest(%q) err = %v", h, err)
		}
		if _, err := l.Referrers(ctx, h, ""); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("Referrers(%q) err = %v", h, err)
		}
		if _, err := l.FetchBlob(ctx, v1.Descriptor{Digest: h, Size: 1}, 1); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("FetchBlob(%q) err = %v", h, err)
		}
	}
	if _, err := l.PushArtifact(ctx, registry.Artifact{}); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Errorf("PushArtifact without artifactType err = %v", err)
	}
	canceled, cancel := testCtxWithCancel(t)
	cancel()
	if _, err := l.Manifest(canceled, v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("a", 64)}); err == nil {
		t.Error("Manifest ignored a canceled context")
	}
}

func TestLayout_IndexIsNotAnArtifact(t *testing.T) {
	t.Parallel()
	l, dir := newLayout(t)
	idx, err := random.Index(16, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = layout.Path(dir).AppendIndex(idx); err != nil {
		t.Fatal(err)
	}
	h, _ := idx.Digest()
	if _, _, err = l.ArtifactManifest(testCtx(t), h); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("ArtifactManifest on an index err = %v", err)
	}
	m, err := l.Manifest(testCtx(t), h)
	if err != nil || !m.MediaType.IsIndex() {
		t.Fatalf("Manifest = %+v, %v", m, err)
	}
}

// TestLayout_MediaTypeFromIndex reads a manifest without a mediaType field:
// index.json supplies it.
func TestLayout_MediaTypeFromIndex(t *testing.T) {
	t.Parallel()
	_, dir := newLayout(t)
	raw := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[]}`)
	h, size, _ := v1.SHA256(bytes.NewReader(raw))
	p := layout.Path(dir)
	if err := p.WriteBlob(h, nopCloser{bytes.NewReader(raw)}); err != nil {
		t.Fatal(err)
	}
	l, err := registry.OpenLayout(dir, registry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.Manifest(testCtx(t), h); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Fatalf("unlisted manifest without mediaType err = %v", err)
	}
	if err = p.AppendDescriptor(v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: h, Size: size}); err != nil {
		t.Fatal(err)
	}
	m, err := l.Manifest(testCtx(t), h)
	if err != nil || m.MediaType != types.OCIManifestSchema1 {
		t.Fatalf("Manifest = %+v, %v", m, err)
	}
}

type nopCloser struct{ *bytes.Reader }

func testCtxWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return ctx, cancel
}

// pushIndex writes a random two-image index to tag; nested wraps it in an
// outer index next to one more image.
func pushIndex(t *testing.T, tag name.Tag, nested bool) v1.ImageIndex {
	t.Helper()
	idx, err := random.Index(128, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if nested {
		img, ierr := random.Image(128, 1)
		if ierr != nil {
			t.Fatal(ierr)
		}
		idx = mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: idx}, mutate.IndexAddendum{Add: img})
	}
	if err = remote.WriteIndex(tag, idx, remote.WithTransport(newTestTransport(t))); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return idx
}

// firstImage descends through first entries to an image manifest.
func firstImage(t *testing.T, idx v1.ImageIndex) v1.Descriptor {
	t.Helper()
	for range 4 {
		im, err := idx.IndexManifest()
		if err != nil || len(im.Manifests) == 0 {
			t.Fatalf("index manifest: %v", err)
		}
		d := im.Manifests[0]
		if d.MediaType.IsImage() {
			return d
		}
		if idx, err = idx.ImageIndex(d.Digest); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("no image within 4 index levels")
	return v1.Descriptor{}
}

func validateIndex(idx v1.ImageIndex) error { return validate.Index(idx) }

func remoteIndex(t *testing.T, ref string) (v1.ImageIndex, error) {
	t.Helper()
	r, err := name.ParseReference(ref)
	if err != nil {
		return nil, err
	}
	return remote.Index(r, remote.WithTransport(newTestTransport(t)))
}

func (nopCloser) Close() error { return nil }

// seedSigned pushes an index (one child image index nested when nested is
// set) to host/app:v1 with a referrer on the index and one on its first
// image, and returns the index and that image.
func seedSigned(t *testing.T, c *registry.Client, host string, nested bool) (root, child v1.Descriptor, refs []v1.Hash) {
	t.Helper()
	ctx := testCtx(t)
	tag, err := name.NewTag(host + "/app:v1")
	if err != nil {
		t.Fatal(err)
	}
	idx := pushIndex(t, tag, nested)
	m, err := c.Manifest(ctx, tag.String())
	if err != nil {
		t.Fatal(err)
	}
	root = v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}
	child = firstImage(t, idx)
	for _, subject := range []v1.Descriptor{root, child} {
		d, err := c.PushArtifact(ctx, host+"/app", registry.Artifact{
			ArtifactType: sigType,
			Blobs:        []registry.Blob{{MediaType: sigType, Reader: strings.NewReader(`{"for":"` + subject.Digest.String() + `"}`)}},
			Subject:      &subject,
		})
		if err != nil {
			t.Fatalf("push referrer: %v", err)
		}
		refs = append(refs, d.Digest)
	}
	return root, child, refs
}

func TestCopyLayout_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		srcAPI, dstAPI bool
		nested         bool
	}{
		{name: "tag-schema to tag-schema"},
		{name: "referrers-api to tag-schema", srcAPI: true},
		{name: "tag-schema to referrers-api", dstAPI: true},
		{name: "nested index", srcAPI: true, dstAPI: true, nested: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			c := artifactClient(t, registry.Options{}, nil)
			src := newArtifactRegistry(t, nil, regsrv.WithReferrersSupport(tc.srcAPI))
			root, child, refs := seedSigned(t, c, src, tc.nested)

			l, dir := newLayout(t)
			got, err := c.CopyToLayout(ctx, src+"/app:v1", l)
			if err != nil || got.Digest != root.Digest || got.Size != root.Size || got.MediaType != root.MediaType {
				t.Fatalf("CopyToLayout = %+v, %v; want %+v", got, err, root)
			}
			checkLayout(t, l, dir, root, child, refs)
			if _, err = c.CopyToLayout(ctx, src+"/app@"+root.Digest.String(), l); err != nil {
				t.Fatalf("second CopyToLayout: %v", err)
			}
			if n := len(indexEntries(t, dir)); n != 3 {
				t.Fatalf("index.json has %d entries after a second copy, want 3", n)
			}

			dst := newArtifactRegistry(t, nil, regsrv.WithReferrersSupport(tc.dstAPI))
			pushed, err := c.CopyFromLayout(ctx, l, root.Digest, dst+"/mirror/app:v2")
			if err != nil || pushed.Digest != root.Digest {
				t.Fatalf("CopyFromLayout = %+v, %v", pushed, err)
			}
			checkRemote(t, c, dst+"/mirror/app", root, child, refs)
		})
	}
}

// checkLayout proves the layout holds the whole index (go-containerregistry
// validates every digest) and both referrers, listed in index.json.
func checkLayout(t *testing.T, l *registry.Layout, dir string, root, child v1.Descriptor, refs []v1.Hash) {
	t.Helper()
	ctx := testCtx(t)
	entries := indexEntries(t, dir)
	listed := map[v1.Hash]v1.Descriptor{}
	for _, e := range entries {
		listed[e.Digest] = e
	}
	if len(entries) != 3 || listed[root.Digest].Digest != root.Digest || listed[refs[0]].ArtifactType != sigType || listed[refs[1]].ArtifactType != sigType {
		t.Fatalf("index.json = %+v", entries)
	}
	ii, err := layout.Path(dir).ImageIndex()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ii.ImageIndex(root.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateIndex(idx); err != nil {
		t.Fatalf("layout content: %v", err)
	}
	for i, subject := range []v1.Hash{root.Digest, child.Digest} {
		got, err := l.Referrers(ctx, subject, sigType)
		if err != nil || len(got) != 1 || got[0].Digest != refs[i] {
			t.Fatalf("layout referrers of %s = %+v, %v", subject, got, err)
		}
	}
}

func checkRemote(t *testing.T, c *registry.Client, repo string, root, child v1.Descriptor, refs []v1.Hash) {
	t.Helper()
	ctx := testCtx(t)
	d, err := c.Resolve(ctx, repo+":v2")
	if err != nil || d.DigestStr() != root.Digest.String() {
		t.Fatalf("tag v2 = %v, %v", d, err)
	}
	// Unfiltered: ggcr's test registry reports config.mediaType as artifactType.
	for i, subject := range []v1.Hash{root.Digest, child.Digest} {
		got, rerr := c.Referrers(ctx, repo+"@"+subject.String(), "")
		if rerr != nil || len(got) != 1 || got[0].Digest != refs[i] {
			t.Fatalf("registry referrers of %s = %+v, %v", subject, got, rerr)
		}
	}
	idx, err := remoteIndex(t, repo+"@"+root.Digest.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = validateIndex(idx); err != nil {
		t.Fatalf("registry content: %v", err)
	}
}

func TestCopyFromLayout_RejectsTamperedLayout(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, dir string, root, child v1.Descriptor, refs []v1.Hash){
		"root manifest": func(t *testing.T, dir string, root, _ v1.Descriptor, _ []v1.Hash) {
			t.Helper()
			flip(t, blobPath(dir, root.Digest))
		},
		"child manifest": func(t *testing.T, dir string, _, child v1.Descriptor, _ []v1.Hash) {
			t.Helper()
			flip(t, blobPath(dir, child.Digest))
		},
		"referrer manifest": func(t *testing.T, dir string, _, _ v1.Descriptor, refs []v1.Hash) {
			t.Helper()
			flip(t, blobPath(dir, refs[1]))
		},
		"layer": func(t *testing.T, dir string, _, child v1.Descriptor, _ []v1.Hash) {
			t.Helper()
			m, err := v1.ParseManifest(bytes.NewReader(mustRead(t, blobPath(dir, child.Digest))))
			if err != nil {
				t.Fatal(err)
			}
			flip(t, blobPath(dir, m.Layers[0].Digest))
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			c := artifactClient(t, registry.Options{}, nil)
			src := newArtifactRegistry(t, nil)
			root, child, refs := seedSigned(t, c, src, false)
			l, dir := newLayout(t)
			if _, err := c.CopyToLayout(ctx, src+"/app:v1", l); err != nil {
				t.Fatal(err)
			}
			tamper(t, dir, root, child, refs)
			dst := newArtifactRegistry(t, nil)
			if _, err := c.CopyFromLayout(ctx, l, root.Digest, dst+"/app:v2"); !errors.Is(err, registry.ErrDigestMismatch) {
				t.Fatalf("CopyFromLayout err = %v, want ErrDigestMismatch", err)
			}
			if _, err := c.Resolve(ctx, dst+"/app:v2"); err == nil {
				t.Fatal("tampered layout reached the registry tag")
			}
		})
	}
}

func TestCopyToLayout_RejectsCorruptBlob(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	c := artifactClient(t, registry.Options{}, nil)
	src := newArtifactRegistry(t, nil)
	_, child, _ := seedSigned(t, c, src, false)
	m, err := c.Manifest(ctx, src+"/app@"+child.Digest.String())
	if err != nil {
		t.Fatal(err)
	}
	man, err := v1.ParseManifest(bytes.NewReader(m.Raw))
	if err != nil {
		t.Fatal(err)
	}
	bad := artifactClient(t, registry.Options{}, &corruptingTransport{base: newTestTransport(t), pathSuffix: "/blobs/" + man.Layers[0].Digest.String()})
	l, dir := newLayout(t)
	if _, err = bad.CopyToLayout(ctx, src+"/app:v1", l); !errors.Is(err, registry.ErrDigestMismatch) {
		t.Fatalf("CopyToLayout err = %v, want ErrDigestMismatch", err)
	}
	if n := len(indexEntries(t, dir)); n != 0 {
		t.Fatalf("index.json lists %d entries after a failed copy", n)
	}
	files, _ := os.ReadDir(filepath.Join(dir, "blobs", "sha256"))
	for _, f := range files {
		if len(f.Name()) != 64 {
			t.Fatalf("temp file left behind: %s", f.Name())
		}
		if f.Name() == man.Layers[0].Digest.Hex {
			t.Fatal("corrupt layer committed")
		}
	}
}

func TestCopyLayout_Caps(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	src := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	img, err := random.Image(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	pushImage(t, src+"/img:v1", img)
	m, _ := img.Manifest()
	raw, _ := img.RawManifest()
	h, _ := img.Digest()
	subject := v1.Descriptor{MediaType: m.MediaType, Digest: h, Size: int64(len(raw))}
	for _, at := range []string{scoreType, modelType} {
		if _, err = c.PushArtifact(ctx, src+"/img", registry.Artifact{ArtifactType: at, Subject: &subject}); err != nil {
			t.Fatal(err)
		}
	}
	refs, err := c.Referrers(ctx, src+"/img:v1", "")
	if err != nil || len(refs) != 2 {
		t.Fatalf("referrers = %v, %v", refs, err)
	}
	// Content of one copy: image manifest, config, layers, two referrer
	// manifests and the 2-byte empty blob they share as config and layer.
	maxManifest, maxBlob, total := int64(len(raw)), max(m.Config.Size, 2), int64(len(raw))+m.Config.Size+2
	for _, l := range m.Layers {
		maxBlob = max(maxBlob, l.Size)
		total += l.Size
	}
	for _, r := range refs {
		maxManifest = max(maxManifest, r.Size)
		total += r.Size
	}
	cases := []struct {
		name     string
		ok, over registry.Options
	}{
		{name: "layers", ok: registry.Options{MaxBlobs: 2}, over: registry.Options{MaxBlobs: 1}},
		{name: "blob bytes", ok: registry.Options{MaxBlobBytes: maxBlob}, over: registry.Options{MaxBlobBytes: maxBlob - 1}},
		{name: "manifest bytes", ok: registry.Options{MaxManifestBytes: maxManifest}, over: registry.Options{MaxManifestBytes: maxManifest - 1}},
		{name: "total bytes", ok: registry.Options{MaxTotalBytes: total}, over: registry.Options{MaxTotalBytes: total - 1}},
		{name: "referrers", ok: registry.Options{MaxReferrers: 2}, over: registry.Options{MaxReferrers: 1}},
	}
	for _, tc := range cases {
		l, _ := newLayout(t)
		if _, err = artifactClient(t, tc.ok, nil).CopyToLayout(ctx, src+"/img:v1", l); err != nil {
			t.Errorf("%s at cap: %v", tc.name, err)
		}
		l, _ = newLayout(t)
		if _, err = artifactClient(t, tc.over, nil).CopyToLayout(ctx, src+"/img:v1", l); !errors.Is(err, registry.ErrTooLarge) {
			t.Errorf("%s over cap: err = %v, want ErrTooLarge", tc.name, err)
		}
	}
}

func TestCopyLayout_InvalidInputs(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	c := artifactClient(t, registry.Options{}, nil)
	l, dir := newLayout(t)
	model, _ := pushModel(t, l)
	host := newArtifactRegistry(t, nil)
	invalid := map[string]func() error{
		"to nil layout":   func() error { _, err := c.CopyToLayout(ctx, host+"/a:v1", nil); return err },
		"from nil layout": func() error { _, err := c.CopyFromLayout(ctx, nil, model.Digest, host+"/a"); return err },
		"digest target": func() error {
			_, err := c.CopyFromLayout(ctx, l, model.Digest, host+"/a@"+model.Digest.String())
			return err
		},
		"bad digest": func() error {
			_, err := c.CopyFromLayout(ctx, l, v1.Hash{Algorithm: "sha256", Hex: "../x"}, host+"/a")
			return err
		},
	}
	for name, fn := range invalid {
		if err := fn(); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("%s: err = %v, want ErrInvalidArtifact", name, err)
		}
	}
	odd := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.example.notes+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a","size":2},"layers":[]}`)
	oh, _, _ := v1.SHA256(bytes.NewReader(odd))
	if err := layout.Path(dir).WriteBlob(oh, nopCloser{bytes.NewReader(odd)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CopyFromLayout(ctx, l, oh, host+"/a"); !errors.Is(err, registry.ErrInvalidArtifact) {
		t.Errorf("non-manifest root err = %v, want ErrInvalidArtifact", err)
	}
	if _, err := c.CopyToLayout(ctx, "UPPER/Case::", l); err == nil {
		t.Error("CopyToLayout accepted a malformed reference")
	}
	if _, err := c.CopyToLayout(ctx, host+"/missing:v1", l); err == nil {
		t.Error("CopyToLayout of a missing image succeeded")
	}
	absent := v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("b", 64)}
	if _, err := c.CopyFromLayout(ctx, l, absent, host+"/a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CopyFromLayout of a missing digest err = %v", err)
	}
	pushed, err := c.CopyFromLayout(ctx, l, model.Digest, host+"/models")
	if err != nil || pushed.Digest != model.Digest {
		t.Fatalf("untagged CopyFromLayout = %+v, %v", pushed, err)
	}
	if refs, err := c.Referrers(ctx, host+"/models@"+model.Digest.String(), ""); err != nil || len(refs) != 1 {
		t.Fatalf("untagged copy referrers = %v, %v", refs, err)
	}
}

// TestCopyLayout_ForeignLayerStays copies an image whose foreign layer has no
// blob anywhere: its URL is where clients fetch it.
func TestCopyLayout_ForeignLayerStays(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	_, dir := newLayout(t)
	foreign := v1.Descriptor{
		MediaType: types.DockerForeignLayer, Size: 1 << 20, URLs: []string{"https://example.com/layer.tar.gz"},
		Digest: v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("c", 64)},
	}
	cfg := []byte("{}")
	cfgHash, _, _ := v1.SHA256(bytes.NewReader(cfg))
	man, err := json.Marshal(v1.Manifest{
		SchemaVersion: 2, MediaType: types.DockerManifestSchema2,
		Config: v1.Descriptor{MediaType: types.DockerConfigJSON, Digest: cfgHash, Size: 2}, Layers: []v1.Descriptor{foreign},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, size, _ := v1.SHA256(bytes.NewReader(man))
	p := layout.Path(dir)
	for blob, data := range map[v1.Hash][]byte{cfgHash: cfg, h: man} {
		if err = p.WriteBlob(blob, nopCloser{bytes.NewReader(data)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.AppendDescriptor(v1.Descriptor{MediaType: types.DockerManifestSchema2, Digest: h, Size: size}); err != nil {
		t.Fatal(err)
	}
	l, err := registry.OpenLayout(dir, registry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := artifactClient(t, registry.Options{}, nil)
	host := newArtifactRegistry(t, nil)
	if _, err = c.CopyFromLayout(ctx, l, h, host+"/win:v1"); err != nil {
		t.Fatalf("CopyFromLayout: %v", err)
	}
	back, _ := newLayout(t)
	if _, err = c.CopyToLayout(ctx, host+"/win:v1", back); err != nil {
		t.Fatalf("CopyToLayout: %v", err)
	}
}

// TestCopyFromLayout_ChildSizeMismatch refuses an index whose entry names a
// real manifest with the wrong size.
func TestCopyFromLayout_ChildSizeMismatch(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	model, _ := pushModel(t, l)
	model.Size++
	raw, err := json.Marshal(v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex, Manifests: []v1.Descriptor{model}})
	if err != nil {
		t.Fatal(err)
	}
	h, _, _ := v1.SHA256(bytes.NewReader(raw))
	if err = layout.Path(dir).WriteBlob(h, nopCloser{bytes.NewReader(raw)}); err != nil {
		t.Fatal(err)
	}
	host := newArtifactRegistry(t, nil)
	if _, err = artifactClient(t, registry.Options{}, nil).CopyFromLayout(ctx, l, h, host+"/a:v1"); !errors.Is(err, registry.ErrDigestMismatch) {
		t.Fatalf("CopyFromLayout err = %v, want ErrDigestMismatch", err)
	}
}

// TestCopyLayout_ManifestCountCap fills a copy to exactly 1 + MaxBlobs +
// MaxReferrers manifests, then one more.
func TestCopyLayout_ManifestCountCap(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	c := artifactClient(t, registry.Options{}, nil)
	src := newArtifactRegistry(t, nil)
	tag, err := name.NewTag(src + "/app:v1")
	if err != nil {
		t.Fatal(err)
	}
	idx := pushIndex(t, tag, false)
	im, _ := idx.IndexManifest()
	rootHash, _ := idx.Digest()
	rootSize, _ := idx.Size()
	root := v1.Descriptor{MediaType: types.OCIImageIndex, Digest: rootHash, Size: rootSize}
	opts := registry.Options{MaxBlobs: 2, MaxReferrers: 2}
	attach := func(subject v1.Descriptor) {
		t.Helper()
		if _, perr := c.PushArtifact(ctx, src+"/app", registry.Artifact{ArtifactType: scoreType, Subject: &subject}); perr != nil {
			t.Fatal(perr)
		}
	}
	attach(root)
	attach(im.Manifests[0])
	l, _ := newLayout(t)
	if _, err = artifactClient(t, opts, nil).CopyToLayout(ctx, src+"/app:v1", l); err != nil {
		t.Fatalf("5 manifests at the cap: %v", err)
	}
	attach(im.Manifests[1])
	l, _ = newLayout(t)
	if _, err = artifactClient(t, opts, nil).CopyToLayout(ctx, src+"/app:v1", l); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("6 manifests err = %v, want ErrTooLarge", err)
	}
}

// TestLayout_ManifestCap reads a manifest at exactly MaxManifestBytes, then
// with one byte less.
func TestLayout_ManifestCap(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	model, err := l.PushArtifact(ctx, registry.Artifact{
		ArtifactType: modelType,
		Blobs:        []registry.Blob{{Name: "model.onnx", Reader: strings.NewReader("weights")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if idx := int64(len(mustRead(t, filepath.Join(dir, "index.json")))); idx >= model.Size {
		t.Fatalf("index.json (%d bytes) must stay below the manifest (%d bytes) for this test", idx, model.Size)
	}
	for limit, want := range map[int64]error{model.Size: nil, model.Size - 1: registry.ErrTooLarge} {
		capped, oerr := registry.OpenLayout(dir, registry.Options{MaxManifestBytes: limit})
		if oerr != nil {
			t.Fatal(oerr)
		}
		if _, merr := capped.Manifest(ctx, model.Digest); !errors.Is(merr, want) {
			t.Errorf("Manifest with MaxManifestBytes %d err = %v, want %v", limit, merr, want)
		}
		if _, _, aerr := capped.ArtifactManifest(ctx, model.Digest); !errors.Is(aerr, want) {
			t.Errorf("ArtifactManifest with MaxManifestBytes %d err = %v, want %v", limit, aerr, want)
		}
	}
}

// TestCopyToLayout_RepairsCorruptBlob re-copies into a layout whose layer
// was damaged after an earlier copy: the bad file is replaced, not kept.
func TestCopyToLayout_RepairsCorruptBlob(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	c := artifactClient(t, registry.Options{}, nil)
	src := newArtifactRegistry(t, nil)
	root, child, _ := seedSigned(t, c, src, false)
	l, dir := newLayout(t)
	if _, err := c.CopyToLayout(ctx, src+"/app:v1", l); err != nil {
		t.Fatal(err)
	}
	m, err := v1.ParseManifest(bytes.NewReader(mustRead(t, blobPath(dir, child.Digest))))
	if err != nil {
		t.Fatal(err)
	}
	flip(t, blobPath(dir, m.Layers[0].Digest))
	if _, err = c.CopyToLayout(ctx, src+"/app:v1", l); err != nil {
		t.Fatalf("second CopyToLayout: %v", err)
	}
	ii, err := layout.Path(dir).ImageIndex()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ii.ImageIndex(root.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateIndex(idx); err != nil {
		t.Fatalf("damaged layer kept: %v", err)
	}
}
