// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// pullDirMode is the permission of a directory PullArtifact creates.
const pullDirMode = 0o750

// PullOptions tunes one [Client.PullArtifact].
type PullOptions struct {
	// ArtifactType, when set, must equal the manifest artifactType (or the
	// config media type of artifacts without one).
	ArtifactType string
}

// PullArtifact fetches the manifest ref names (tag or digest) and writes
// each layer into dir (created if missing) as the file named by its
// [AnnotationTitle], or "sha256-<hex>" without one. Counts, sizes and file
// names are checked before any write; every blob is hashed while streamed
// into a temp file that is renamed into place only after its digest
// matched. It returns the manifest descriptor.
func (c *Client) PullArtifact(ctx context.Context, ref, dir string, opts PullOptions) (v1.Descriptor, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return v1.Descriptor{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: build puller: %w", err)
	}
	desc, man, err := c.artifactManifest(ctx, puller, r)
	if err != nil {
		return v1.Descriptor{}, err
	}
	if got := artifactTypeOf(man); opts.ArtifactType != "" && got != opts.ArtifactType {
		return v1.Descriptor{}, fmt.Errorf("%w: got %q, want %q", ErrArtifactType, got, opts.ArtifactType)
	}
	names, err := c.planPull(man)
	if err != nil {
		return v1.Descriptor{}, err
	}
	if err = os.MkdirAll(dir, pullDirMode); err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: create %s: %w", dir, err)
	}
	for i, l := range man.Layers {
		if err = pullBlob(ctx, puller, r.Context(), dir, names[i], l); err != nil {
			return v1.Descriptor{}, err
		}
	}
	return desc, nil
}

// ArtifactManifest fetches and parses the image manifest ref names,
// refusing manifests above Options.MaxManifestBytes and indexes.
func (c *Client) ArtifactManifest(ctx context.Context, ref string) (v1.Descriptor, *v1.Manifest, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return v1.Descriptor{}, nil, err
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return v1.Descriptor{}, nil, fmt.Errorf("registry: build puller: %w", err)
	}
	return c.artifactManifest(ctx, puller, r)
}

// FetchBlob reads one blob of repo into memory, verified against d and
// refused above maxBytes (and Options.MaxBlobBytes). Use it for small
// referrer payloads such as Sigstore bundles.
func (c *Client) FetchBlob(ctx context.Context, repo string, d v1.Descriptor, maxBytes int64) ([]byte, error) {
	r, err := name.NewRepository(repo)
	if err != nil {
		return nil, fmt.Errorf("%w: repository %q: %w", ErrInvalidArtifact, repo, err)
	}
	if err = c.limits.checkFetch(d, maxBytes); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("registry: build puller: %w", err)
	}
	var buf bytes.Buffer
	if err = copyBlob(ctx, puller, r, &buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *Client) artifactManifest(ctx context.Context, p *remote.Puller, r name.Reference) (v1.Descriptor, *v1.Manifest, error) {
	head, err := p.Head(ctx, r)
	if err != nil {
		return v1.Descriptor{}, nil, fmt.Errorf("registry: resolve %q: %w", r.String(), err)
	}
	if head.Size > c.limits.manifestBytes {
		return v1.Descriptor{}, nil, fmt.Errorf("%w: manifest %d bytes > %d", ErrTooLarge, head.Size, c.limits.manifestBytes)
	}
	if !head.MediaType.IsImage() {
		return v1.Descriptor{}, nil, fmt.Errorf("%w: %s is %q, want an image manifest", ErrInvalidArtifact, r.String(), head.MediaType)
	}
	got, err := p.Get(ctx, r)
	if err != nil {
		return v1.Descriptor{}, nil, fmt.Errorf("registry: fetch manifest %q: %w", r.String(), err)
	}
	return c.limits.decodeArtifact(got.Descriptor, got.Manifest)
}

// planPull checks layer count, sizes and file names before any I/O.
func (c *Client) planPull(man *v1.Manifest) ([]string, error) {
	if len(man.Layers) > c.limits.blobs {
		return nil, fmt.Errorf("%w: %d layers > %d", ErrTooLarge, len(man.Layers), c.limits.blobs)
	}
	names := make([]string, 0, len(man.Layers))
	seen := make(map[string]struct{}, len(man.Layers))
	var total int64
	for _, l := range man.Layers {
		if l.Size < 0 || l.Size > c.limits.blobBytes {
			return nil, fmt.Errorf("%w: layer %s is %d bytes, limit %d", ErrTooLarge, l.Digest, l.Size, c.limits.blobBytes)
		}
		if total += l.Size; total > c.limits.totalBytes {
			return nil, fmt.Errorf("%w: layers exceed %d bytes in total", ErrTooLarge, c.limits.totalBytes)
		}
		n := layerName(l)
		if err := validateName(n); err != nil {
			return nil, err
		}
		if _, dup := seen[n]; dup {
			return nil, fmt.Errorf("%w: duplicate file name %q", ErrInvalidArtifact, n)
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	return names, nil
}

// pullBlob streams one verified layer into dir/fileName via temp + rename.
func pullBlob(ctx context.Context, p *remote.Puller, repo name.Repository, dir, fileName string, d v1.Descriptor) (err error) {
	rc, err := openRemoteBlob(ctx, p, repo, d)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	return commitFile(dir, fileName, rc)
}

// copyBlob streams blob d of repo into w and fails unless size and sha256
// digest match exactly.
func copyBlob(ctx context.Context, p *remote.Puller, repo name.Repository, w io.Writer, d v1.Descriptor) (err error) {
	rc, err := openRemoteBlob(ctx, p, repo, d)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rc.Close()) }()
	if _, err = io.Copy(w, rc); err != nil {
		return fmt.Errorf("registry: read blob %s: %w", d.Digest, err)
	}
	return nil
}

// openRemoteBlob opens blob d of repo as a verified stream.
func openRemoteBlob(ctx context.Context, p *remote.Puller, repo name.Repository, d v1.Descriptor) (io.ReadCloser, error) {
	if err := checkDigest(d.Digest); err != nil {
		return nil, err
	}
	layer, err := p.Layer(ctx, repo.Digest(d.Digest.String()))
	if err != nil {
		return nil, fmt.Errorf("registry: blob %s: %w", d.Digest, err)
	}
	rc, err := layer.Compressed()
	if err != nil {
		return nil, fmt.Errorf("registry: fetch blob %s: %w", d.Digest, err)
	}
	return newVerifier(ctx, rc, d), nil
}

func artifactTypeOf(m *v1.Manifest) string {
	if m.ArtifactType != "" {
		return m.ArtifactType
	}
	if m.Config.MediaType == EmptyJSONMediaType {
		return ""
	}
	return string(m.Config.MediaType)
}

func layerName(d v1.Descriptor) string {
	if t := d.Annotations[AnnotationTitle]; t != "" {
		return t
	}
	return d.Digest.Algorithm + "-" + d.Digest.Hex
}

// validateName accepts one local path element: no separators, no "..".
func validateName(n string) error {
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, `/\`) || !filepath.IsLocal(n) {
		return fmt.Errorf("%w: file name %q must be a single local path element", ErrInvalidArtifact, n)
	}
	return nil
}
