// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// Artifact transfer defaults applied for zero [Options] fields.
const (
	DefaultTransferTimeout        = 10 * time.Minute
	DefaultMaxManifestBytes int64 = 4 << 20
	DefaultMaxBlobBytes     int64 = 1 << 30
	DefaultMaxTotalBytes    int64 = 4 << 30
	DefaultMaxBlobs               = 64
	DefaultMaxReferrers           = 256
)

// AnnotationTitle names a blob's file; [Client.PullArtifact] writes the
// blob under this name.
const AnnotationTitle = "org.opencontainers.image.title"

// EmptyJSONMediaType is the OCI 1.1 empty descriptor media type used for
// the config (and the sole layer) of a blob-less artifact.
const EmptyJSONMediaType types.MediaType = "application/vnd.oci.empty.v1+json"

const defaultBlobMediaType types.MediaType = "application/octet-stream"

// emptyJSON is the content of the OCI empty descriptor.
var emptyJSON = []byte("{}")

// Sentinel errors of the artifact API; match with [errors.Is].
var (
	// ErrTooLarge reports content above a configured size or count cap.
	ErrTooLarge = errors.New("registry: artifact exceeds limit")
	// ErrDigestMismatch reports content that does not hash to its descriptor.
	ErrDigestMismatch = errors.New("registry: digest mismatch")
	// ErrArtifactType reports a manifest of an unexpected artifact type.
	ErrArtifactType = errors.New("registry: unexpected artifact type")
	// ErrInvalidArtifact reports a malformed artifact, blob, name or target.
	ErrInvalidArtifact = errors.New("registry: invalid artifact")
)

type limits struct {
	transfer      time.Duration
	manifestBytes int64
	blobBytes     int64
	totalBytes    int64
	blobs         int
	referrers     int
}

func newLimits(o Options) limits {
	l := limits{
		transfer: o.TransferTimeout, manifestBytes: o.MaxManifestBytes, blobBytes: o.MaxBlobBytes,
		totalBytes: o.MaxTotalBytes, blobs: o.MaxBlobs, referrers: o.MaxReferrers,
	}
	l.transfer = positiveOr(l.transfer, DefaultTransferTimeout)
	l.manifestBytes = positiveOr(l.manifestBytes, DefaultMaxManifestBytes)
	l.blobBytes = positiveOr(l.blobBytes, DefaultMaxBlobBytes)
	l.totalBytes = positiveOr(l.totalBytes, DefaultMaxTotalBytes)
	l.blobs = positiveOr(l.blobs, DefaultMaxBlobs)
	l.referrers = positiveOr(l.referrers, DefaultMaxReferrers)
	return l
}

// limitValue covers the limit types: counts, byte sizes and durations. It is a
// named interface, not an inline union, so Semgrep's Go parser can read it
// (semgrep/semgrep#11972).
type limitValue interface{ ~int | ~int64 }

func positiveOr[T limitValue](v, d T) T {
	if v <= 0 {
		return d
	}
	return v
}

// Blob is one layer of an [Artifact]. Set exactly one of Path or Reader.
type Blob struct {
	// MediaType of the content; empty means application/octet-stream.
	MediaType types.MediaType
	// Name becomes the [AnnotationTitle] annotation. Defaults to the base
	// name of Path; a Reader blob without Name pulls as "sha256-<hex>".
	Name string
	// Path is a local file to upload.
	Path string
	// Reader streams the content; it is spooled to a temp file (bounded by
	// Options.MaxBlobBytes) to compute the digest before upload.
	Reader io.Reader
	// Annotations are attached to the layer descriptor.
	Annotations map[string]string
}

// Artifact is an OCI image-spec v1.1 artifact: empty config, caller-chosen
// artifactType and layer media types, annotations, optional subject.
// Nothing time-dependent is added, so identical input yields an identical
// manifest digest, which `cosign sign <repo>@<digest>` can sign.
type Artifact struct {
	// ArtifactType is the manifest artifactType, e.g.
	// "application/vnd.vmafx.model.v1".
	ArtifactType string
	// Blobs are the layers; none yields the spec's single empty layer.
	Blobs []Blob
	// Annotations are manifest annotations.
	Annotations map[string]string
	// Subject makes this artifact a referrer of another manifest.
	Subject *v1.Descriptor
}

// rawManifest adapts serialized manifest bytes to [remote.Taggable].
type rawManifest struct {
	raw       []byte
	mediaType types.MediaType
}

func (m rawManifest) RawManifest() ([]byte, error)        { return m.raw, nil }
func (m rawManifest) MediaType() (types.MediaType, error) { return m.mediaType, nil }

// PushArtifact uploads a's blobs and manifest and returns the manifest
// descriptor. ref is "registry/repository" (untagged; address it by the
// returned digest) or "registry/repository:tag". Registries without the
// OCI 1.1 referrers API get the referrers tag schema updated for Subject.
func (c *Client) PushArtifact(ctx context.Context, ref string, a Artifact) (v1.Descriptor, error) {
	repo, tag, err := parsePushTarget(ref)
	if err != nil {
		return v1.Descriptor{}, err
	}
	if err = c.checkArtifact(a); err != nil {
		return v1.Descriptor{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	pusher, err := remote.NewPusher(c.remoteOptions()...)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: build pusher: %w", err)
	}
	layers, err := c.uploadBlobs(ctx, pusher, repo, a.Blobs)
	if err != nil {
		return v1.Descriptor{}, err
	}
	raw, desc, err := c.buildManifest(a, layers)
	if err != nil {
		return v1.Descriptor{}, err
	}
	var target name.Reference = repo.Digest(desc.Digest.String())
	if tag != "" {
		target = repo.Tag(tag)
	}
	if err = pusher.Put(ctx, target, rawManifest{raw: raw, mediaType: desc.MediaType}); err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: push manifest %q: %w", target.String(), err)
	}
	return desc, nil
}

// parsePushTarget accepts "registry/repo" or "registry/repo:tag".
func parsePushTarget(ref string) (name.Repository, string, error) {
	if r, err := name.ParseReference(ref, name.StrictValidation); err == nil {
		t, ok := r.(name.Tag)
		if !ok {
			return name.Repository{}, "", fmt.Errorf("%w: push target %q must not be a digest", ErrInvalidArtifact, ref)
		}
		return t.Context(), t.TagStr(), nil
	}
	repo, err := name.NewRepository(ref)
	if err != nil {
		return name.Repository{}, "", fmt.Errorf("%w: push target %q: %w", ErrInvalidArtifact, ref, err)
	}
	return repo, "", nil
}

func (c *Client) checkArtifact(a Artifact) error {
	if a.ArtifactType == "" || !strings.Contains(a.ArtifactType, "/") {
		return fmt.Errorf("%w: artifactType %q is not a media type", ErrInvalidArtifact, a.ArtifactType)
	}
	if len(a.Blobs) > c.limits.blobs {
		return fmt.Errorf("%w: %d blobs > %d", ErrTooLarge, len(a.Blobs), c.limits.blobs)
	}
	return nil
}

// uploadBlobs uploads every blob (or the empty layer) plus the empty config.
func (c *Client) uploadBlobs(ctx context.Context, p *remote.Pusher, repo name.Repository, blobs []Blob) ([]v1.Descriptor, error) {
	if err := uploadEmpty(ctx, p, repo); err != nil {
		return nil, err
	}
	if len(blobs) == 0 {
		return []v1.Descriptor{emptyDescriptor()}, nil
	}
	layers := make([]v1.Descriptor, 0, len(blobs))
	for i := range blobs {
		d, err := c.uploadBlob(ctx, p, repo, blobs[i])
		if err != nil {
			return nil, fmt.Errorf("registry: blob %d: %w", i, err)
		}
		layers = append(layers, d)
	}
	return layers, nil
}

func emptyDescriptor() v1.Descriptor {
	sum := sha256.Sum256(emptyJSON)
	return v1.Descriptor{
		MediaType: EmptyJSONMediaType,
		Digest:    v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])},
		Size:      int64(len(emptyJSON)),
	}
}

func uploadEmpty(ctx context.Context, p *remote.Pusher, repo name.Repository) error {
	d := emptyDescriptor()
	l, err := partial.CompressedToLayer(&bytesLayer{d: d, b: emptyJSON})
	if err != nil {
		return fmt.Errorf("registry: empty blob: %w", err)
	}
	if err = p.Upload(ctx, repo, l); err != nil {
		return fmt.Errorf("registry: upload empty blob: %w", err)
	}
	return nil
}

func (c *Client) uploadBlob(ctx context.Context, p *remote.Pusher, repo name.Repository, b Blob) (_ v1.Descriptor, err error) {
	path, cleanup, err := c.blobFile(b)
	if err != nil {
		return v1.Descriptor{}, err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	d, err := c.describe(path, b)
	if err != nil {
		return v1.Descriptor{}, err
	}
	l, err := partial.CompressedToLayer(&fileLayer{d: d, path: path})
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: layer: %w", err)
	}
	if err = p.Upload(ctx, repo, l); err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: upload %s: %w", d.Digest, err)
	}
	return d, nil
}

// blobFile returns a local file holding b and the function releasing it.
func (c *Client) blobFile(b Blob) (string, func() error, error) {
	switch {
	case b.Path != "" && b.Reader != nil:
		return "", nil, fmt.Errorf("%w: blob sets both path and reader", ErrInvalidArtifact)
	case b.Path != "":
		return filepath.Clean(b.Path), func() error { return nil }, nil
	case b.Reader != nil:
		return c.spool(b.Reader)
	default:
		return "", nil, fmt.Errorf("%w: blob needs path or reader", ErrInvalidArtifact)
	}
}

// spool copies r into a temp file, refusing more than MaxBlobBytes.
func (c *Client) spool(r io.Reader) (_ string, _ func() error, err error) {
	f, err := os.CreateTemp("", "registry-blob-*")
	if err != nil {
		return "", nil, fmt.Errorf("registry: spool blob: %w", err)
	}
	remove := func() error { return os.Remove(f.Name()) }
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			err = errors.Join(err, remove())
		}
	}()
	n, err := io.Copy(f, io.LimitReader(r, c.limits.blobBytes+1))
	if err != nil {
		return "", nil, fmt.Errorf("registry: spool blob: %w", err)
	}
	if n > c.limits.blobBytes {
		return "", nil, fmt.Errorf("%w: blob > %d bytes", ErrTooLarge, c.limits.blobBytes)
	}
	return f.Name(), remove, nil
}

// describe hashes the blob file and builds its layer descriptor.
func (c *Client) describe(path string, b Blob) (_ v1.Descriptor, err error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: open blob: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, c.limits.blobBytes+1))
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: hash blob: %w", err)
	}
	if n > c.limits.blobBytes {
		return v1.Descriptor{}, fmt.Errorf("%w: blob > %d bytes", ErrTooLarge, c.limits.blobBytes)
	}
	annotations, err := blobAnnotations(b)
	if err != nil {
		return v1.Descriptor{}, err
	}
	mt := b.MediaType
	if mt == "" {
		mt = defaultBlobMediaType
	}
	return v1.Descriptor{
		MediaType:   mt,
		Digest:      v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(h.Sum(nil))},
		Size:        n,
		Annotations: annotations,
	}, nil
}

func blobAnnotations(b Blob) (map[string]string, error) {
	title := b.Name
	if title == "" && b.Path != "" {
		title = filepath.Base(b.Path)
	}
	if title == "" {
		return b.Annotations, nil
	}
	if err := validateName(title); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(b.Annotations)+1)
	maps.Copy(out, b.Annotations)
	out[AnnotationTitle] = title
	return out, nil
}

// buildManifest serializes the artifact manifest deterministically.
func (c *Client) buildManifest(a Artifact, layers []v1.Descriptor) ([]byte, v1.Descriptor, error) {
	m := v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		Config:        emptyDescriptor(),
		Layers:        layers,
		Annotations:   a.Annotations,
		Subject:       a.Subject,
		ArtifactType:  a.ArtifactType,
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, v1.Descriptor{}, fmt.Errorf("registry: encode manifest: %w", err)
	}
	if int64(len(raw)) > c.limits.manifestBytes {
		return nil, v1.Descriptor{}, fmt.Errorf("%w: manifest %d bytes > %d", ErrTooLarge, len(raw), c.limits.manifestBytes)
	}
	sum := sha256.Sum256(raw)
	return raw, v1.Descriptor{
		MediaType:    types.OCIManifestSchema1,
		Digest:       v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])},
		Size:         int64(len(raw)),
		ArtifactType: a.ArtifactType,
		Annotations:  a.Annotations,
	}, nil
}

// fileLayer serves a blob from a local file for upload.
type fileLayer struct {
	d    v1.Descriptor
	path string
}

func (l *fileLayer) Digest() (v1.Hash, error)            { return l.d.Digest, nil }
func (l *fileLayer) Size() (int64, error)                { return l.d.Size, nil }
func (l *fileLayer) MediaType() (types.MediaType, error) { return l.d.MediaType, nil }

func (l *fileLayer) Compressed() (io.ReadCloser, error) {
	f, err := os.Open(filepath.Clean(l.path))
	if err != nil {
		return nil, fmt.Errorf("registry: open blob: %w", err)
	}
	return f, nil
}

// bytesLayer serves a small in-memory blob for upload.
type bytesLayer struct {
	d v1.Descriptor
	b []byte
}

func (l *bytesLayer) Digest() (v1.Hash, error)            { return l.d.Digest, nil }
func (l *bytesLayer) Size() (int64, error)                { return l.d.Size, nil }
func (l *bytesLayer) MediaType() (types.MediaType, error) { return l.d.MediaType, nil }
func (l *bytesLayer) Compressed() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(string(l.b))), nil
}
