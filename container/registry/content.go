// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// verifier streams one blob and fails the read unless the stream ends at
// exactly the descriptor's size and sha256 digest. Every blob this package
// moves, from a registry, a layout or a local file, passes through one.
type verifier struct {
	ctx context.Context //nolint:containedctx // WHY: io.Reader has no ctx parameter; Read checks cancellation per chunk.
	r   io.Reader
	c   io.Closer
	d   v1.Descriptor
	h   hash.Hash
	n   int64
}

// newVerifier reads at most d.Size+1 bytes: one byte too many fails at EOF.
func newVerifier(ctx context.Context, rc io.ReadCloser, d v1.Descriptor) *verifier {
	return &verifier{ctx: ctx, r: io.LimitReader(rc, d.Size+1), c: rc, d: d, h: sha256.New()}
}

func (v *verifier) Read(p []byte) (int, error) {
	if err := v.ctx.Err(); err != nil {
		return 0, fmt.Errorf("registry: read blob %s: %w", v.d.Digest, err)
	}
	n, err := v.r.Read(p)
	// A reader of exactly d.Size bytes (an upload with Content-Length) never
	// reads EOF, so bytes past the size and a full-size mismatch are withheld.
	if v.n+int64(n) > v.d.Size {
		return 0, v.mismatch(nil)
	}
	v.h.Write(p[:n])
	v.n += int64(n)
	complete := v.n == v.d.Size && hex.EncodeToString(v.h.Sum(nil)) == v.d.Digest.Hex
	switch {
	case v.n == v.d.Size && !complete:
		return 0, v.mismatch(err)
	case err == nil:
		return n, nil
	case complete:
		return n, err //nolint:wrapcheck // WHY: io.EOF must reach io.Copy unwrapped; other errors are wrapped by the copy site.
	default:
		// The source's own verifier may fail the read at EOF; the local hash
		// decides, so a short, long or corrupted stream is a mismatch.
		return n, v.mismatch(err)
	}
}

func (v *verifier) mismatch(cause error) error {
	if errors.Is(cause, io.EOF) {
		cause = nil
	}
	return errors.Join(fmt.Errorf("%w: blob %s (%d of %d bytes)", ErrDigestMismatch, v.d.Digest, v.n, v.d.Size), cause)
}

func (v *verifier) Close() error {
	if err := v.c.Close(); err != nil {
		return fmt.Errorf("registry: close blob %s: %w", v.d.Digest, err)
	}
	return nil
}

// checkDigest accepts a well-formed sha256 digest only: a layout maps the
// digest to a file path, so anything else could escape blobs/.
func checkDigest(h v1.Hash) error {
	if h.Algorithm != "sha256" {
		return fmt.Errorf("%w: digest algorithm %q unsupported", ErrInvalidArtifact, h.Algorithm)
	}
	if _, err := v1.NewHash(h.String()); err != nil {
		return fmt.Errorf("%w: digest %q: %w", ErrInvalidArtifact, h.String(), err)
	}
	return nil
}

func isManifest(mt types.MediaType) bool { return mt.IsImage() || mt.IsIndex() }

// checkManifest caps raw at the manifest limit and checks it hashes to h.
func (lim limits) checkManifest(raw []byte, h v1.Hash) error {
	if int64(len(raw)) > lim.manifestBytes {
		return fmt.Errorf("%w: manifest %d bytes > %d", ErrTooLarge, len(raw), lim.manifestBytes)
	}
	if sum := sha256.Sum256(raw); h.Algorithm != "sha256" || hex.EncodeToString(sum[:]) != h.Hex {
		return fmt.Errorf("%w: manifest %s", ErrDigestMismatch, h)
	}
	return nil
}

// decodeArtifact verifies raw against d and parses it as an image manifest.
func (lim limits) decodeArtifact(d v1.Descriptor, raw []byte) (v1.Descriptor, *v1.Manifest, error) {
	if err := lim.checkManifest(raw, d.Digest); err != nil {
		return v1.Descriptor{}, nil, err
	}
	if !d.MediaType.IsImage() {
		return v1.Descriptor{}, nil, fmt.Errorf("%w: %s is %q, want an image manifest", ErrInvalidArtifact, d.Digest, d.MediaType)
	}
	man, err := v1.ParseManifest(bytes.NewReader(raw))
	if err != nil {
		return v1.Descriptor{}, nil, fmt.Errorf("%w: decode manifest %s: %w", ErrInvalidArtifact, d.Digest, err)
	}
	d.Size = int64(len(raw))
	d.ArtifactType = artifactTypeOf(man)
	return d, man, nil
}

// checkFetch refuses a blob above maxBytes or the configured blob cap.
func (lim limits) checkFetch(d v1.Descriptor, maxBytes int64) error {
	if limit := min(maxBytes, lim.blobBytes); d.Size < 0 || d.Size > limit {
		return fmt.Errorf("%w: blob %s is %d bytes, limit %d", ErrTooLarge, d.Digest, d.Size, limit)
	}
	return nil
}

// capReferrers refuses more referrers than the configured cap.
func (lim limits) capReferrers(refs []v1.Descriptor, subject string) ([]v1.Descriptor, error) {
	if len(refs) > lim.referrers {
		return nil, fmt.Errorf("%w: %d referrers of %s > %d", ErrTooLarge, len(refs), subject, lim.referrers)
	}
	return refs, nil
}

// commitFile writes r to dir/name through a temp file in dir that is synced
// and renamed into place only after r ended without error.
func commitFile(dir, name string, r io.Reader) (err error) {
	tmp, err := os.CreateTemp(dir, ".registry-*")
	if err != nil {
		return fmt.Errorf("registry: create temp file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, discard(tmp))
		}
	}()
	if _, err = io.Copy(tmp, r); err != nil {
		return fmt.Errorf("registry: write %s: %w", name, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("registry: sync %s: %w", name, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("registry: close %s: %w", name, err)
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("registry: commit %s: %w", name, err)
	}
	committed = true
	return nil
}

// discard closes (if still open) and removes a temp file.
func discard(f *os.File) error {
	cerr := f.Close()
	if errors.Is(cerr, os.ErrClosed) {
		cerr = nil
	}
	return errors.Join(cerr, os.Remove(f.Name()))
}

// openLayer serves a blob for upload from a function that opens a verified
// stream; go-containerregistry may open it again on retry.
type openLayer struct {
	d    v1.Descriptor
	open func() (io.ReadCloser, error)
}

func (l *openLayer) Digest() (v1.Hash, error)            { return l.d.Digest, nil }
func (l *openLayer) Size() (int64, error)                { return l.d.Size, nil }
func (l *openLayer) MediaType() (types.MediaType, error) { return l.d.MediaType, nil }
func (l *openLayer) Compressed() (io.ReadCloser, error)  { return l.open() }

// openFile opens the local file at path as a verified stream of d.
func openFile(ctx context.Context, path string, d v1.Descriptor) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("registry: open blob: %w", err)
		}
		return newVerifier(ctx, f, d), nil
	}
}

// openBytes serves b as a verified stream of d.
func openBytes(ctx context.Context, d v1.Descriptor, b []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return newVerifier(ctx, io.NopCloser(bytes.NewReader(b)), d), nil
	}
}
