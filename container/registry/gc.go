// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// GCOptions tunes [Layout.GC].
type GCOptions struct {
	// DryRun reports what GC would remove and changes nothing.
	DryRun bool
}

// GCReport lists the blobs [Layout.GC] removed, or with DryRun would remove.
type GCReport struct {
	// Removed are the blob digests deleted from blobs/sha256.
	Removed []v1.Hash
	// Bytes is their total size.
	Bytes int64
}

// Delete removes the manifest digest names and its referrers at every
// level from l's index.json, rewritten atomically; blobs stay until
// [Layout.GC]. For an index subject it also unlists the referrers of child
// manifests that no remaining index.json entry reaches, since GC drops
// those children. Every listed manifest is read and verified first; any
// error changes nothing. Delete is idempotent: an unlisted subject is
// reported Absent. DryRun reports and changes nothing.
func (l *Layout) Delete(ctx context.Context, digest v1.Hash, opts DeleteOptions) (DeleteReport, error) {
	if err := checkDigest(digest); err != nil {
		return DeleteReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, l.limits.transfer)
	defer cancel()
	l.mu.Lock()
	defer l.mu.Unlock()
	im, err := l.readIndex()
	if err != nil {
		return DeleteReport{}, err
	}
	order, err := l.unlistOrder(ctx, im, digest)
	if err != nil {
		return DeleteReport{}, err
	}
	rep, keep := splitEntries(im.Manifests, order, digest)
	if opts.DryRun || len(rep.Removed) == 0 {
		return rep, nil
	}
	im.Manifests = keep
	if err = l.writeIndex(im); err != nil {
		return DeleteReport{}, err
	}
	return rep, nil
}

// unlistOrder returns what Delete unlists: digest after its referrers, then
// the referrers of the manifests below it that nothing else keeps.
func (l *Layout) unlistOrder(ctx context.Context, im *v1.IndexManifest, digest v1.Hash) ([]v1.Hash, error) {
	refs, err := l.referrersIn(ctx, im)
	if err != nil {
		return nil, err
	}
	list := func(_ context.Context, h v1.Hash) ([]v1.Descriptor, error) {
		return l.limits.capReferrers(refs[h], h.String())
	}
	tree, err := l.limits.referrerTree(ctx, list, []v1.Hash{digest})
	if err != nil {
		return nil, err
	}
	orphans, err := l.orphans(ctx, im, tree)
	if err != nil || len(orphans) == 0 {
		return tree, err
	}
	more, err := l.limits.referrerTree(ctx, list, orphans)
	if err != nil {
		return nil, err
	}
	return append(tree, more...), nil
}

// orphans returns the manifests below the listed members of tree that no
// index.json entry outside tree reaches.
func (l *Layout) orphans(ctx context.Context, im *v1.IndexManifest, tree []v1.Hash) ([]v1.Hash, error) {
	var starts, rest []v1.Descriptor
	for _, d := range im.Manifests {
		if slices.Contains(tree, d.Digest) {
			starts = append(starts, d)
		} else {
			rest = append(rest, d)
		}
	}
	budget, err := l.blobBudget()
	if err != nil {
		return nil, err
	}
	below, err := l.reachable(ctx, starts, budget)
	if err != nil {
		return nil, err
	}
	var kids []v1.Hash
	for h, isManifest := range below {
		if isManifest && !slices.Contains(tree, h) {
			kids = append(kids, h)
		}
	}
	if len(kids) == 0 {
		return nil, nil
	}
	live, err := l.reachable(ctx, rest, budget)
	if err != nil {
		return nil, err
	}
	kids = slices.DeleteFunc(kids, func(h v1.Hash) bool {
		_, reached := live[h]
		return reached
	})
	slices.SortFunc(kids, func(a, b v1.Hash) int { return strings.Compare(a.Hex, b.Hex) })
	return kids, nil
}

// splitEntries drops the entries order names and reports them; an unlisted
// subject is Absent.
func splitEntries(entries []v1.Descriptor, order []v1.Hash, subject v1.Hash) (DeleteReport, []v1.Descriptor) {
	listed := make(map[v1.Hash]struct{}, len(entries))
	keep := make([]v1.Descriptor, 0, len(entries))
	for _, d := range entries {
		listed[d.Digest] = struct{}{}
		if !slices.Contains(order, d.Digest) {
			keep = append(keep, d)
		}
	}
	var rep DeleteReport
	for _, h := range order {
		if _, ok := listed[h]; ok {
			rep.Removed = append(rep.Removed, h)
		} else if h == subject {
			rep.Absent = append(rep.Absent, h)
		}
	}
	return rep, keep
}

// GC deletes the blobs under blobs/sha256 that no index.json entry reaches
// through index entries, configs and layers; referrers are index.json
// entries, so they keep their blobs. Any error while walking (malformed
// index or manifest, digest mismatch, missing manifest, symlinked blob)
// deletes nothing. Files not named by a sha256 digest stay. A second GC
// removes nothing. GC waits for this *Layout's writes in flight; no other
// process or *Layout may write the layout meanwhile.
func (l *Layout) GC(ctx context.Context, opts GCOptions) (_ GCReport, err error) {
	ctx, cancel := context.WithTimeout(ctx, l.limits.transfer)
	defer cancel()
	l.gc.Lock()
	defer l.gc.Unlock()
	dir, ok, err := l.openBlobDir()
	if err != nil || !ok {
		return GCReport{}, err
	}
	defer func() { err = errors.Join(err, closeRoot(dir)) }()
	files, err := blobFiles(dir)
	if err != nil {
		return GCReport{}, err
	}
	im, err := l.readIndex()
	if err != nil {
		return GCReport{}, err
	}
	keep, err := l.reachable(ctx, im.Manifests, len(files)+1)
	if err != nil {
		return GCReport{}, err
	}
	return sweep(ctx, dir, files, keep, opts.DryRun)
}

// sweep removes the files keep lacks.
func sweep(ctx context.Context, dir *os.Root, files []fs.FileInfo, keep map[v1.Hash]bool, dry bool) (GCReport, error) {
	var rep GCReport
	for _, f := range files {
		h := v1.Hash{Algorithm: "sha256", Hex: f.Name()}
		if _, ok := keep[h]; ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rep, incomplete(len(rep.Removed) > 0, fmt.Errorf("registry: gc: %w", err))
		}
		if !dry {
			if err := dir.Remove(f.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return rep, incomplete(len(rep.Removed) > 0, fmt.Errorf("registry: gc: remove %s: %w", h, err))
			}
		}
		rep.Removed = append(rep.Removed, h)
		rep.Bytes += f.Size()
	}
	return rep, nil
}

// reachable walks manifests from roots through index entries, configs and
// layers on an explicit stack, reading and verifying at most budget
// manifests. It maps each digest reached to whether it is a manifest.
func (l *Layout) reachable(ctx context.Context, roots []v1.Descriptor, budget int) (map[v1.Hash]bool, error) {
	seen := make(map[v1.Hash]bool, len(roots))
	read := 0
	visit := func(f frame) ([]frame, error) {
		if seen[f.d.Digest] {
			return nil, nil
		}
		if read >= budget {
			return nil, fmt.Errorf("%w: more than %d manifests", ErrTooLarge, budget)
		}
		read++
		kids, blobs, err := l.content(ctx, f.d)
		if err != nil {
			return nil, err
		}
		seen[f.d.Digest] = true
		for _, b := range blobs {
			if _, ok := seen[b.Digest]; !ok {
				seen[b.Digest] = false
			}
		}
		next := make([]frame, 0, len(kids))
		for _, k := range kids {
			next = append(next, frame{d: k})
		}
		return next, nil
	}
	start := make([]frame, 0, len(roots))
	for _, d := range roots {
		start = append(start, frame{d: d})
	}
	if err := walkFrames(start, len(roots)+budget*(l.limits.blobs+1), visit); err != nil {
		return nil, err
	}
	return seen, nil
}

// content reads manifest d, verified, and returns its child manifests and
// blobs.
func (l *Layout) content(ctx context.Context, d v1.Descriptor) (kids, blobs []v1.Descriptor, err error) {
	raw, err := l.readManifest(ctx, d.Digest)
	if err != nil {
		return nil, nil, err
	}
	mt, err := declaredMediaType(raw, d.Digest)
	if err != nil {
		return nil, nil, err
	}
	if mt == "" {
		mt = d.MediaType
	}
	return contentOf(d.Digest, mt, raw, l.limits.blobs)
}

// blobBudget bounds a walk by the blob files a layout holds.
func (l *Layout) blobBudget() (_ int, err error) {
	dir, ok, err := l.openBlobDir()
	if err != nil || !ok {
		return 1, err
	}
	defer func() { err = errors.Join(err, closeRoot(dir)) }()
	files, err := blobFiles(dir)
	return len(files) + 1, err
}

// openBlobDir opens blobs/sha256 as an [os.Root], so no removal leaves it;
// neither it nor blobs may be a symlink. ok is false for a layout without
// blobs.
func (l *Layout) openBlobDir() (_ *os.Root, ok bool, err error) {
	top, err := os.OpenRoot(l.dir())
	if err != nil {
		return nil, false, fmt.Errorf("registry: open layout %s: %w", l.dir(), err)
	}
	defer func() { err = errors.Join(err, closeRoot(top)) }()
	sub := filepath.Join("blobs", "sha256")
	for _, p := range []string{"blobs", sub} {
		info, serr := top.Lstat(p)
		if errors.Is(serr, fs.ErrNotExist) {
			return nil, false, nil
		}
		if serr != nil {
			return nil, false, fmt.Errorf("registry: layout %s: %w", p, serr)
		}
		if !info.IsDir() {
			return nil, false, fmt.Errorf("%w: layout %s is not a directory", ErrInvalidArtifact, p)
		}
	}
	dir, err := top.OpenRoot(sub)
	if err != nil {
		return nil, false, fmt.Errorf("registry: open layout %s: %w", sub, err)
	}
	return dir, true, nil
}

// blobFiles lists the sha256-named entries of dir; one that is not a
// regular file, such as a symlink, is refused.
func blobFiles(dir *os.Root) (_ []fs.FileInfo, err error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, fmt.Errorf("registry: open blobs: %w", err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("registry: list blobs: %w", err)
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		if checkDigest(v1.Hash{Algorithm: "sha256", Hex: e.Name()}) != nil {
			continue
		}
		if !e.Type().IsRegular() {
			return nil, fmt.Errorf("%w: blob %s is not a regular file", ErrInvalidArtifact, e.Name())
		}
		info, ierr := e.Info()
		if ierr != nil {
			return nil, fmt.Errorf("registry: stat blob %s: %w", e.Name(), ierr)
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b fs.FileInfo) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

func closeRoot(r *os.Root) error {
	if err := r.Close(); err != nil {
		return fmt.Errorf("registry: close layout dir: %w", err)
	}
	return nil
}
