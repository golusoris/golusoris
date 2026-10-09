// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	layoutFile = "oci-layout"
	indexFile  = "index.json"
	// layoutContent marks an image-layout spec v1.0.0 directory.
	layoutContent = `{"imageLayoutVersion":"1.0.0"}`
)

// Layout is an OCI image-layout directory: blobs addressed by sha256 digest
// under blobs/sha256 and an index.json listing its top-level manifests, the
// format go-containerregistry's pkg/v1/layout, oras and skopeo read and
// write. A referrer (signature, attestation, SBOM) lives in a layout as its
// manifest blob plus an index.json entry, the way oras-go's OCI store keeps
// untagged manifests; [Layout.Referrers] finds it by its subject.
//
// Every read is verified against its digest: a layout copied by rsync,
// rclone or a USB stick is untrusted until it hashes right. The zero value is
// not usable; open one with [OpenLayout] or [CreateLayout]. One process
// writes a layout at a time; a *Layout serializes its own index.json updates.
type Layout struct {
	path    layout.Path
	timeout time.Duration
	limits  limits
	mu      sync.Mutex
	// gc excludes [Layout.GC] while a write lists blobs it already stored.
	gc sync.RWMutex
}

func newLayout(dir string, opts Options) *Layout {
	return &Layout{path: layout.Path(dir), timeout: positiveOr(opts.Timeout, DefaultTimeout), limits: newLimits(opts)}
}

// OpenLayout opens the existing image layout in dir. opts supplies the size
// caps and timeouts; its registry-only fields are ignored.
func OpenLayout(dir string, opts Options) (*Layout, error) {
	if _, err := os.Stat(filepath.Join(dir, layoutFile)); err != nil {
		return nil, fmt.Errorf("%w: %s is not an OCI image layout: %w", ErrInvalidArtifact, dir, err)
	}
	l := newLayout(dir, opts)
	if _, err := l.readIndex(); err != nil {
		return nil, err
	}
	return l, nil
}

// CreateLayout opens the image layout in dir, first creating an empty one
// when dir is missing or empty. A non-empty dir without index.json is
// [ErrInvalidArtifact].
func CreateLayout(dir string, opts Options) (*Layout, error) {
	_, err := os.Stat(filepath.Join(dir, indexFile))
	if err == nil {
		return OpenLayout(dir, opts)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("registry: layout %s: %w", dir, err)
	}
	if err = ensureEmptyDir(dir); err != nil {
		return nil, err
	}
	l := newLayout(dir, opts)
	if err = commitFile(dir, layoutFile, strings.NewReader(layoutContent)); err != nil {
		return nil, err
	}
	empty := &v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex, Manifests: []v1.Descriptor{}}
	if err = l.writeIndex(empty); err != nil {
		return nil, err
	}
	return l, nil
}

func ensureEmptyDir(dir string) (err error) {
	if err = os.MkdirAll(dir, pullDirMode); err != nil {
		return fmt.Errorf("registry: create %s: %w", dir, err)
	}
	f, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("registry: open %s: %w", dir, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	names, err := f.Readdirnames(1)
	if len(names) > 0 {
		return fmt.Errorf("%w: %s is neither empty nor an OCI image layout", ErrInvalidArtifact, dir)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("registry: read %s: %w", dir, err)
	}
	return nil
}

func (l *Layout) dir() string { return string(l.path) }

// readIndex reads index.json, capped like a manifest.
func (l *Layout) readIndex() (_ *v1.IndexManifest, err error) {
	f, err := os.Open(filepath.Join(l.dir(), indexFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidArtifact, l.dir(), err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(f, l.limits.manifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", indexFile, err)
	}
	if int64(len(raw)) > l.limits.manifestBytes {
		return nil, fmt.Errorf("%w: %s > %d bytes", ErrTooLarge, indexFile, l.limits.manifestBytes)
	}
	im, err := v1.ParseIndexManifest(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrInvalidArtifact, indexFile, err)
	}
	return im, nil
}

func (l *Layout) writeIndex(im *v1.IndexManifest) error {
	raw, err := json.Marshal(im)
	if err != nil {
		return fmt.Errorf("registry: encode %s: %w", indexFile, err)
	}
	return commitFile(l.dir(), indexFile, bytes.NewReader(raw))
}

// list adds d to index.json unless an entry already names its digest.
func (l *Layout) list(d v1.Descriptor) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	im, err := l.readIndex()
	if err != nil {
		return err
	}
	for _, m := range im.Manifests {
		if m.Digest == d.Digest {
			return nil
		}
	}
	im.Manifests = append(im.Manifests, v1.Descriptor{MediaType: d.MediaType, Digest: d.Digest, Size: d.Size, ArtifactType: d.ArtifactType})
	return l.writeIndex(im)
}

// readManifest reads blob h, capped and verified as a manifest.
func (l *Layout) readManifest(ctx context.Context, h v1.Hash) (_ []byte, err error) {
	if err = checkDigest(h); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("registry: layout manifest %s: %w", h, err)
	}
	f, err := l.path.Blob(h)
	if err != nil {
		return nil, fmt.Errorf("registry: layout manifest %s: %w", h, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(f, l.limits.manifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("registry: read layout manifest %s: %w", h, err)
	}
	if err = l.limits.checkManifest(raw, h); err != nil {
		return nil, err
	}
	return raw, nil
}

// openBlob opens blob d as a verified stream; the verifier checks ctx.
func (l *Layout) openBlob(ctx context.Context, d v1.Descriptor) (io.ReadCloser, error) {
	if err := checkDigest(d.Digest); err != nil {
		return nil, err
	}
	f, err := l.path.Blob(d.Digest)
	if err != nil {
		return nil, fmt.Errorf("registry: layout blob %s: %w", d.Digest, err)
	}
	return newVerifier(ctx, f, d), nil
}

// hasBlob reports whether blob d is present and hashes right.
func (l *Layout) hasBlob(ctx context.Context, d v1.Descriptor) bool {
	rc, err := l.openBlob(ctx, d)
	if err != nil {
		return false
	}
	_, err = io.Copy(io.Discard, rc)
	return errors.Join(err, rc.Close()) == nil
}

// writeBlob commits r, a verified stream of d, to blobs/sha256/<hex>.
func (l *Layout) writeBlob(d v1.Descriptor, r io.Reader) error {
	if err := checkDigest(d.Digest); err != nil {
		return err
	}
	dir := filepath.Join(l.dir(), "blobs", d.Digest.Algorithm)
	if err := os.MkdirAll(dir, pullDirMode); err != nil {
		return fmt.Errorf("registry: create %s: %w", dir, err)
	}
	return commitFile(dir, d.Digest.Hex, r)
}

// Manifest reads the manifest (or index) digest names from l, verified
// against digest and Options.MaxManifestBytes. Its media type is the
// manifest's own mediaType field, else the one index.json records for it.
func (l *Layout) Manifest(ctx context.Context, digest v1.Hash) (*Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	return l.manifest(ctx, digest)
}

func (l *Layout) manifest(ctx context.Context, h v1.Hash) (*Manifest, error) {
	raw, err := l.readManifest(ctx, h)
	if err != nil {
		return nil, err
	}
	mt, err := l.mediaType(raw, h)
	if err != nil {
		return nil, err
	}
	return &Manifest{Digest: h, MediaType: mt, Size: int64(len(raw)), Raw: raw}, nil
}

// declaredMediaType is the manifest's own mediaType field, "" without one.
func declaredMediaType(raw []byte, h v1.Hash) (types.MediaType, error) {
	var probe struct {
		MediaType types.MediaType `json:"mediaType"` //nolint:tagliatelle // OCI image-spec wire name
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("%w: decode manifest %s: %w", ErrInvalidArtifact, h, err)
	}
	return probe.MediaType, nil
}

func (l *Layout) mediaType(raw []byte, h v1.Hash) (types.MediaType, error) {
	if mt, err := declaredMediaType(raw, h); err != nil || mt != "" {
		return mt, err
	}
	im, err := l.readIndex()
	if err != nil {
		return "", err
	}
	for _, d := range im.Manifests {
		if d.Digest == h && d.MediaType != "" {
			return d.MediaType, nil
		}
	}
	return "", fmt.Errorf("%w: manifest %s declares no media type", ErrInvalidArtifact, h)
}

// ArtifactManifest reads and parses the image manifest digest names from
// l, refusing indexes and manifests above Options.MaxManifestBytes.
func (l *Layout) ArtifactManifest(ctx context.Context, digest v1.Hash) (v1.Descriptor, *v1.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	m, err := l.manifest(ctx, digest)
	if err != nil {
		return v1.Descriptor{}, nil, err
	}
	return l.limits.decodeArtifact(v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}, m.Raw)
}

// FetchBlob reads blob d of l into memory, verified against d and refused
// above maxBytes (and Options.MaxBlobBytes). Use it for small referrer
// payloads such as Sigstore bundles.
func (l *Layout) FetchBlob(ctx context.Context, d v1.Descriptor, maxBytes int64) (_ []byte, err error) {
	if err = l.limits.checkFetch(d, maxBytes); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, l.limits.transfer)
	defer cancel()
	rc, err := l.openBlob(ctx, d)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, rc); err != nil {
		return nil, fmt.Errorf("registry: read layout blob %s: %w", d.Digest, err)
	}
	return buf.Bytes(), nil
}

// Referrers lists the manifests in l's index.json whose subject is digest,
// filtered by artifactType when non-empty, as descriptors shaped like the
// OCI 1.1 referrers API returns them: artifactType (else config media
// type) and manifest annotations. Each listed manifest is read and verified.
// More than Options.MaxReferrers results is [ErrTooLarge].
func (l *Layout) Referrers(ctx context.Context, digest v1.Hash, artifactType string) ([]v1.Descriptor, error) {
	if err := checkDigest(digest); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	all, err := l.referrerIndex(ctx)
	if err != nil {
		return nil, err
	}
	var out []v1.Descriptor
	for _, d := range all[digest] {
		if artifactType == "" || d.ArtifactType == artifactType {
			out = append(out, d)
		}
	}
	return l.limits.capReferrers(out, digest.String())
}

// referrerIndex maps each subject digest to the index.json manifests that
// name it.
func (l *Layout) referrerIndex(ctx context.Context) (map[v1.Hash][]v1.Descriptor, error) {
	im, err := l.readIndex()
	if err != nil {
		return nil, err
	}
	return l.referrersIn(ctx, im)
}

// referrersIn maps each subject digest to the entries of im that name it.
func (l *Layout) referrersIn(ctx context.Context, im *v1.IndexManifest) (map[v1.Hash][]v1.Descriptor, error) {
	out := make(map[v1.Hash][]v1.Descriptor)
	seen := make(map[v1.Hash]struct{}, len(im.Manifests))
	for _, d := range im.Manifests {
		if _, dup := seen[d.Digest]; dup || !isManifest(d.MediaType) {
			continue
		}
		seen[d.Digest] = struct{}{}
		ref, subject, err := l.referrer(ctx, d)
		if err != nil {
			return nil, err
		}
		if subject != nil {
			out[*subject] = append(out[*subject], ref)
		}
	}
	return out, nil
}

// referrer reads manifest d and returns its referrers descriptor and subject
// digest; nil subject means d refers to nothing.
func (l *Layout) referrer(ctx context.Context, d v1.Descriptor) (v1.Descriptor, *v1.Hash, error) {
	raw, err := l.readManifest(ctx, d.Digest)
	if err != nil {
		return v1.Descriptor{}, nil, err
	}
	var f referrerFields
	if err = json.Unmarshal(raw, &f); err != nil {
		return v1.Descriptor{}, nil, fmt.Errorf("%w: decode manifest %s: %w", ErrInvalidArtifact, d.Digest, err)
	}
	if f.Subject == nil {
		return v1.Descriptor{}, nil, nil
	}
	at := f.artifactType()
	ref := v1.Descriptor{MediaType: d.MediaType, Digest: d.Digest, Size: int64(len(raw)), ArtifactType: at, Annotations: f.Annotations}
	return ref, &f.Subject.Digest, nil
}

// referrerFields are the manifest fields a referrers listing reports.
type referrerFields struct {
	ArtifactType string `json:"artifactType"` //nolint:tagliatelle // OCI image-spec wire name
	Config       struct {
		MediaType types.MediaType `json:"mediaType"` //nolint:tagliatelle // OCI image-spec wire name
	} `json:"config"`
	Subject     *v1.Descriptor    `json:"subject"`
	Annotations map[string]string `json:"annotations"`
}

// artifactType follows the OCI 1.1 referrers API: artifactType, else the
// config media type.
func (f referrerFields) artifactType() string {
	if f.ArtifactType != "" {
		return f.ArtifactType
	}
	return string(f.Config.MediaType)
}

// PushArtifact writes a's blobs and manifest into l, lists the manifest in
// index.json and returns its descriptor: the manifest
// [Client.PushArtifact] would push, so a Subject makes it a referrer that
// [Layout.Referrers] and [Client.CopyFromLayout] carry along.
func (l *Layout) PushArtifact(ctx context.Context, a Artifact) (v1.Descriptor, error) {
	if err := l.limits.checkArtifact(a); err != nil {
		return v1.Descriptor{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, l.limits.transfer)
	defer cancel()
	l.gc.RLock()
	defer l.gc.RUnlock()
	return l.limits.pushArtifact(ctx, layoutSink{l: l}, a)
}

// layoutSink writes verified content into a layout.
type layoutSink struct{ l *Layout }

func (s layoutSink) putBlob(ctx context.Context, d v1.Descriptor, open func() (io.ReadCloser, error)) (err error) {
	if s.l.hasBlob(ctx, d) {
		return nil
	}
	rc, err := open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	return s.l.writeBlob(d, rc)
}

func (s layoutSink) putManifest(ctx context.Context, d v1.Descriptor, raw []byte, role manifestRole) error {
	if err := s.putBlob(ctx, d, openBytes(ctx, d, raw)); err != nil {
		return err
	}
	if role == roleChild {
		return nil
	}
	return s.l.list(d)
}

// layoutSource reads a layout; it scans index.json for referrers once.
type layoutSource struct {
	l    *Layout
	refs map[v1.Hash][]v1.Descriptor
}

func (s *layoutSource) manifest(ctx context.Context, d v1.Descriptor) ([]byte, error) {
	return s.l.readManifest(ctx, d.Digest)
}

func (s *layoutSource) blob(ctx context.Context, d v1.Descriptor) (io.ReadCloser, error) {
	return s.l.openBlob(ctx, d)
}

func (s *layoutSource) referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	if s.refs == nil {
		refs, err := s.l.referrerIndex(ctx)
		if err != nil {
			return nil, err
		}
		s.refs = refs
	}
	return s.l.limits.capReferrers(s.refs[h], h.String())
}
