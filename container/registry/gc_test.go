// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/golusoris/golusoris/container/registry"
)

// snapshot fingerprints every file, directory and symlink under dir.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, lerr := os.Readlink(p)
			fmt.Fprintf(&b, "%s -> %s\n", rel, target)
			return lerr
		case d.IsDir():
			fmt.Fprintf(&b, "%s/\n", rel)
		default:
			sum := sha256.Sum256(mustRead(t, p))
			fmt.Fprintf(&b, "%s %s\n", rel, hex.EncodeToString(sum[:]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return b.String()
}

func sortedHashes(hs []v1.Hash) []v1.Hash {
	out := slices.Clone(hs)
	slices.SortFunc(out, func(a, b v1.Hash) int { return strings.Compare(a.String(), b.String()) })
	return out
}

func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "blobs", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestLayout_DeleteThenGC(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	model, sig := pushModel(t, l)
	// keep shares the model's layer and the empty config blob.
	keep, err := l.PushArtifact(ctx, registry.Artifact{ArtifactType: modelType, Blobs: []registry.Blob{
		{Name: "model.onnx", Reader: strings.NewReader("weights")}, {Name: "extra.bin", Reader: strings.NewReader("more")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, sigMan, err := l.ArtifactManifest(ctx, sig.Digest)
	if err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "blobs", "sha256", ".registry-leftover")
	if err = os.WriteFile(junk, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	before := snapshot(t, dir)
	want := registry.DeleteReport{Removed: []v1.Hash{sig.Digest, model.Digest}}
	if got, derr := l.Delete(ctx, model.Digest, registry.DeleteOptions{DryRun: true}); derr != nil || !equalReports(got, want) {
		t.Fatalf("dry Delete = %+v, %v; want %+v", got, derr, want)
	}
	if snapshot(t, dir) != before {
		t.Fatal("dry Delete changed the layout")
	}
	if got, derr := l.Delete(ctx, model.Digest, registry.DeleteOptions{}); derr != nil || !equalReports(got, want) {
		t.Fatalf("Delete = %+v, %v; want %+v", got, derr, want)
	}
	if entries := indexEntries(t, dir); len(entries) != 1 || entries[0].Digest != keep.Digest {
		t.Fatalf("index.json after Delete = %+v", entries)
	}
	if got, derr := l.Delete(ctx, model.Digest, registry.DeleteOptions{}); derr != nil || !equalReports(got, registry.DeleteReport{Absent: []v1.Hash{model.Digest}}) {
		t.Fatalf("second Delete = %+v, %v", got, derr)
	}

	garbage := sortedHashes([]v1.Hash{model.Digest, sig.Digest, sigMan.Layers[0].Digest})
	var size int64
	for _, h := range garbage {
		info, serr := os.Stat(blobPath(dir, h))
		if serr != nil {
			t.Fatal(serr)
		}
		size += info.Size()
	}
	before = snapshot(t, dir)
	dry, err := l.GC(ctx, registry.GCOptions{DryRun: true})
	if err != nil || !slices.Equal(sortedHashes(dry.Removed), garbage) || dry.Bytes != size {
		t.Fatalf("dry GC = %+v, %v; want %v (%d bytes)", dry, err, garbage, size)
	}
	if snapshot(t, dir) != before {
		t.Fatal("dry GC changed the layout")
	}
	got, err := l.GC(ctx, registry.GCOptions{})
	if err != nil || !slices.Equal(sortedHashes(got.Removed), garbage) || got.Bytes != size {
		t.Fatalf("GC = %+v, %v; want %v (%d bytes)", got, err, garbage, size)
	}
	for _, h := range garbage {
		if _, serr := os.Stat(blobPath(dir, h)); !errors.Is(serr, fs.ErrNotExist) {
			t.Fatalf("garbage blob %s survived: %v", h, serr)
		}
	}
	if _, err = os.Stat(junk); err != nil {
		t.Fatalf("GC removed a file not named by a digest: %v", err)
	}
	requireArtifact(t, l, keep.Digest)
	if again, gerr := l.GC(ctx, registry.GCOptions{}); gerr != nil || len(again.Removed) != 0 || again.Bytes != 0 {
		t.Fatalf("second GC = %+v, %v", again, gerr)
	}
}

// requireArtifact reads artifact h and every blob it references from l.
func requireArtifact(t *testing.T, l *registry.Layout, h v1.Hash) {
	t.Helper()
	ctx := testCtx(t)
	_, man, err := l.ArtifactManifest(ctx, h)
	if err != nil {
		t.Fatalf("artifact %s: %v", h, err)
	}
	for _, d := range append([]v1.Descriptor{man.Config}, man.Layers...) {
		if _, err = l.FetchBlob(ctx, d, d.Size); err != nil {
			t.Fatalf("artifact %s blob %s: %v", h, d.Digest, err)
		}
	}
}

// TestLayout_DeleteIndex deletes a copied, signed index: the referrers of
// its child go along unless another index.json entry keeps the child.
func TestLayout_DeleteIndex(t *testing.T) {
	t.Parallel()
	for _, keepChild := range []bool{false, true} {
		t.Run(fmt.Sprintf("child listed %v", keepChild), func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			c := artifactClient(t, registry.Options{}, nil)
			src := newArtifactRegistry(t, nil)
			root, child, refs := seedSigned(t, c, src, false)
			l, dir := newLayout(t)
			if _, err := c.CopyToLayout(ctx, src+"/app:v1", l); err != nil {
				t.Fatal(err)
			}
			want := registry.DeleteReport{Removed: []v1.Hash{refs[0], root.Digest, refs[1]}}
			if keepChild {
				if _, err := c.CopyToLayout(ctx, src+"/app@"+child.Digest.String(), l); err != nil {
					t.Fatal(err)
				}
				want.Removed = want.Removed[:2]
			}
			got, err := l.Delete(ctx, root.Digest, registry.DeleteOptions{})
			if err != nil || !equalReports(got, want) {
				t.Fatalf("Delete = %+v, %v; want %+v", got, err, want)
			}
			if _, err = l.GC(ctx, registry.GCOptions{}); err != nil {
				t.Fatalf("GC: %v", err)
			}
			if _, err = os.Stat(blobPath(dir, root.Digest)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("index blob survived GC: %v", err)
			}
			if !keepChild {
				if left := blobFiles(t, dir); len(left) != 0 {
					t.Fatalf("blobs left after deleting everything: %v", left)
				}
				return
			}
			m, err := l.Manifest(ctx, child.Digest)
			if err != nil {
				t.Fatalf("kept child: %v", err)
			}
			img, err := v1.ParseManifest(bytes.NewReader(m.Raw))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range append([]v1.Descriptor{img.Config}, img.Layers...) {
				if _, err = l.FetchBlob(ctx, d, d.Size); err != nil {
					t.Fatalf("kept child blob %s: %v", d.Digest, err)
				}
			}
			if sigs, rerr := l.Referrers(ctx, child.Digest, ""); rerr != nil || len(sigs) != 1 || sigs[0].Digest != refs[1] {
				t.Fatalf("kept child referrers = %v, %v", sigs, rerr)
			}
		})
	}
}

// TestLayout_GCFailsClosed proves a walk error deletes nothing although the
// layout holds garbage.
func TestLayout_GCFailsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		tamper func(t *testing.T, dir string, model, sig v1.Descriptor)
		want   error
	}{
		"garbage index.json": {want: registry.ErrInvalidArtifact, tamper: func(t *testing.T, dir string, _, _ v1.Descriptor) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{garbage"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		"flipped manifest": {want: registry.ErrDigestMismatch, tamper: func(t *testing.T, dir string, model, _ v1.Descriptor) {
			t.Helper()
			flip(t, blobPath(dir, model.Digest))
		}},
		"missing manifest": {want: fs.ErrNotExist, tamper: func(t *testing.T, dir string, _, sig v1.Descriptor) {
			t.Helper()
			if err := os.Remove(blobPath(dir, sig.Digest)); err != nil {
				t.Fatal(err)
			}
		}},
		"missing child manifest": {want: fs.ErrNotExist, tamper: func(t *testing.T, dir string, _, _ v1.Descriptor) {
			t.Helper()
			missing := v1.Descriptor{MediaType: types.OCIManifestSchema1, Size: 10, Digest: v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("d", 64)}}
			appendIndex(t, dir, missing)
		}},
		"listed non-manifest": {want: registry.ErrInvalidArtifact, tamper: func(t *testing.T, dir string, _, _ v1.Descriptor) {
			t.Helper()
			raw := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.example.notes+json","layers":[]}`)
			h, size, _ := v1.SHA256(bytes.NewReader(raw))
			p := layout.Path(dir)
			if err := p.WriteBlob(h, nopCloser{bytes.NewReader(raw)}); err != nil {
				t.Fatal(err)
			}
			if err := p.AppendDescriptor(v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: h, Size: size}); err != nil {
				t.Fatal(err)
			}
		}},
		"symlinked blob": {want: registry.ErrInvalidArtifact, tamper: func(t *testing.T, dir string, _, _ v1.Descriptor) {
			t.Helper()
			target := tempFile(t, "elsewhere", []byte("x"))
			if err := os.Symlink(target, filepath.Join(dir, "blobs", "sha256", strings.Repeat("e", 64))); err != nil {
				t.Skipf("symlink: %v", err)
			}
		}},
		"symlinked blobs dir": {want: registry.ErrInvalidArtifact, tamper: func(t *testing.T, dir string, _, _ v1.Descriptor) {
			t.Helper()
			moved := filepath.Join(t.TempDir(), "blobs")
			if err := os.Rename(filepath.Join(dir, "blobs"), moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, filepath.Join(dir, "blobs")); err != nil {
				t.Skipf("symlink: %v", err)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			l, dir := newLayout(t)
			model, sig := pushModel(t, l)
			garbage := v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("f", 64)}
			if err := os.WriteFile(blobPath(dir, garbage), []byte("unreferenced"), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.tamper(t, dir, model, sig)
			before := snapshot(t, dir)
			if _, err := l.GC(ctx, registry.GCOptions{}); !errors.Is(err, tc.want) {
				t.Fatalf("GC err = %v, want %v", err, tc.want)
			}
			if snapshot(t, dir) != before {
				t.Fatal("failed GC changed the layout")
			}
		})
	}
}

// appendIndex lists a new index manifest naming children in dir.
func appendIndex(t *testing.T, dir string, children ...v1.Descriptor) v1.Descriptor {
	t.Helper()
	raw := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`)
	for i, c := range children {
		if i > 0 {
			raw = append(raw, ',')
		}
		raw = fmt.Appendf(raw, `{"mediaType":%q,"digest":%q,"size":%d}`, c.MediaType, c.Digest, c.Size)
	}
	raw = append(raw, "]}"...)
	h, size, err := v1.SHA256(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := layout.Path(dir)
	if err = p.WriteBlob(h, nopCloser{bytes.NewReader(raw)}); err != nil {
		t.Fatal(err)
	}
	d := v1.Descriptor{MediaType: types.OCIImageIndex, Digest: h, Size: size}
	if err = p.AppendDescriptor(d); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestLayout_DeleteFailsClosed leaves index.json alone when a listed
// manifest does not verify.
func TestLayout_DeleteFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	model, sig := pushModel(t, l)
	flip(t, blobPath(dir, sig.Digest))
	before := snapshot(t, dir)
	if _, err := l.Delete(ctx, model.Digest, registry.DeleteOptions{}); !errors.Is(err, registry.ErrDigestMismatch) {
		t.Fatalf("Delete err = %v, want ErrDigestMismatch", err)
	}
	if snapshot(t, dir) != before {
		t.Fatal("failed Delete changed the layout")
	}
	for _, h := range []v1.Hash{{Algorithm: "sha256", Hex: "../../../etc/passwd"}, {Algorithm: "sha512", Hex: strings.Repeat("a", 128)}, {}} {
		if _, err := l.Delete(ctx, h, registry.DeleteOptions{}); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("Delete(%q) err = %v, want ErrInvalidArtifact", h, err)
		}
	}
}

// TestLayout_DeleteReferrersCap refuses a referrer tree above MaxReferrers.
func TestLayout_DeleteReferrersCap(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	model, sig := pushModel(t, l)
	if _, err := l.PushArtifact(ctx, registry.Artifact{ArtifactType: scoreType, Subject: &sig}); err != nil {
		t.Fatal(err)
	}
	for limit, want := range map[int]error{2: nil, 1: registry.ErrTooLarge} {
		capped, err := registry.OpenLayout(dir, registry.Options{MaxReferrers: limit})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = capped.Delete(ctx, model.Digest, registry.DeleteOptions{DryRun: true}); !errors.Is(err, want) {
			t.Errorf("MaxReferrers %d: err = %v, want %v", limit, err, want)
		}
	}
}

// TestLayout_ConcurrentWritersAndGC runs pushes, a delete and repeated GC on
// one *Layout: every pushed artifact stays whole and listed.
func TestLayout_ConcurrentWritersAndGC(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	doomed, _ := pushModel(t, l)
	const workers = 4
	const perWorker = 3
	pushed := make([][]v1.Descriptor, workers)
	errs := make(chan error, workers+1)
	var wg sync.WaitGroup
	wg.Add(workers + 1)
	for w := range workers {
		go func() {
			defer wg.Done()
			for i := range perWorker {
				d, err := l.PushArtifact(ctx, registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{
					{Name: "r.json", Reader: strings.NewReader(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i))},
				}})
				if err != nil {
					errs <- err
					return
				}
				pushed[w] = append(pushed[w], d)
			}
		}()
	}
	go func() {
		defer wg.Done()
		if _, err := l.Delete(ctx, doomed.Digest, registry.DeleteOptions{}); err != nil {
			errs <- err
			return
		}
		for range 5 {
			if _, err := l.GC(ctx, registry.GCOptions{}); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if _, err := l.GC(ctx, registry.GCOptions{}); err != nil {
		t.Fatal(err)
	}
	listed := map[v1.Hash]bool{}
	for _, e := range indexEntries(t, dir) {
		listed[e.Digest] = true
	}
	if listed[doomed.Digest] || len(listed) != workers*perWorker {
		t.Fatalf("index.json lists %d entries (doomed %v), want %d", len(listed), listed[doomed.Digest], workers*perWorker)
	}
	for _, ds := range pushed {
		for _, d := range ds {
			if !listed[d.Digest] {
				t.Fatalf("pushed %s not listed", d.Digest)
			}
			requireArtifact(t, l, d.Digest)
		}
	}
}

// TestLayout_GCManifestWithoutMediaType keeps the blobs of a listed manifest
// whose media type only index.json records.
func TestLayout_GCManifestWithoutMediaType(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	_, dir := newLayout(t)
	cfg := []byte("{}")
	cfgHash, _, _ := v1.SHA256(bytes.NewReader(cfg))
	raw := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"` + cfgHash.String() + `","size":2},"layers":[]}`)
	h, size, _ := v1.SHA256(bytes.NewReader(raw))
	p := layout.Path(dir)
	for blob, data := range map[v1.Hash][]byte{cfgHash: cfg, h: raw} {
		if err := p.WriteBlob(blob, nopCloser{bytes.NewReader(data)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.AppendDescriptor(v1.Descriptor{MediaType: types.OCIManifestSchema1, Digest: h, Size: size}); err != nil {
		t.Fatal(err)
	}
	l, err := registry.OpenLayout(dir, registry.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, gerr := l.GC(ctx, registry.GCOptions{}); gerr != nil || len(got.Removed) != 0 {
		t.Fatalf("GC = %+v, %v; want nothing removed", got, gerr)
	}
}

// TestLayout_EmptyLayout deletes from and collects a layout that has no
// blobs directory yet.
func TestLayout_EmptyLayout(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	l, dir := newLayout(t)
	if _, err := os.Stat(filepath.Join(dir, "blobs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("new layout has blobs: %v", err)
	}
	if got, err := l.GC(ctx, registry.GCOptions{}); err != nil || len(got.Removed) != 0 || got.Bytes != 0 {
		t.Fatalf("GC = %+v, %v", got, err)
	}
	h := v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("a", 64)}
	if got, err := l.Delete(ctx, h, registry.DeleteOptions{}); err != nil || !equalReports(got, registry.DeleteReport{Absent: []v1.Hash{h}}) {
		t.Fatalf("Delete = %+v, %v", got, err)
	}
}
