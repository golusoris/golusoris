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
	"net/http"
	"slices"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Sentinel errors of [Client.Delete], [Layout.Delete] and [Layout.GC].
var (
	// ErrDeleteUnsupported reports a registry that refuses deletion: 405,
	// 400 or an UNSUPPORTED error code, as the distribution spec allows.
	ErrDeleteUnsupported = errors.New("registry: delete unsupported")
	// ErrPartialDelete reports a delete or garbage collection that failed
	// after removing something; the returned report lists what is gone.
	ErrPartialDelete = errors.New("registry: delete incomplete")
)

// DeleteOptions tunes [Client.Delete] and [Layout.Delete].
type DeleteOptions struct {
	// DryRun reports what a delete would remove and changes nothing.
	DryRun bool
}

// DeleteReport lists what a delete removed, or with DryRun would remove.
// A retried delete succeeds and reports what earlier attempts removed as
// Absent.
type DeleteReport struct {
	// Removed are the manifests deleted, each referrer before its subject.
	Removed []v1.Hash
	// Absent are the manifests that were already gone.
	Absent []v1.Hash
	// Tags are the referrers tag schema tags ("sha256-<hex>") deleted.
	Tags []string
}

func (r DeleteReport) changed() bool { return len(r.Removed) > 0 || len(r.Tags) > 0 }

// incomplete marks err as partial once rep records a change.
func incomplete(changed bool, err error) error {
	if changed {
		return fmt.Errorf("%w: %w", ErrPartialDelete, err)
	}
	return err
}

// listFunc lists the referrers of one manifest.
type listFunc func(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error)

// referrerTree returns roots and their referrers at every level, each
// referrer before its subject. More than Options.MaxReferrers referrers in
// the tree is [ErrTooLarge]; the walk is bounded and not recursive.
func (lim limits) referrerTree(ctx context.Context, list listFunc, roots []v1.Hash) ([]v1.Hash, error) {
	maxNodes := len(roots) + lim.referrers
	seen := make(map[v1.Hash]struct{}, len(roots))
	var order []v1.Hash
	start := make([]frame, 0, len(roots))
	for i := range roots {
		start = append(start, frame{d: v1.Descriptor{Digest: roots[len(roots)-1-i]}})
	}
	visit := func(f frame) ([]frame, error) {
		h := f.d.Digest
		if f.done {
			order = append(order, h)
			return nil, nil
		}
		if _, dup := seen[h]; dup {
			return nil, nil
		}
		if len(seen) >= maxNodes {
			return nil, fmt.Errorf("%w: more than %d referrers to delete", ErrTooLarge, lim.referrers)
		}
		seen[h] = struct{}{}
		refs, err := list(ctx, h)
		if err != nil {
			return nil, err
		}
		next := make([]frame, 0, len(refs)+1)
		next = append(next, frame{d: f.d, done: true})
		for _, r := range refs {
			next = append(next, frame{d: r})
		}
		return next, nil
	}
	if err := walkFrames(start, 2*maxNodes*(lim.referrers+1), visit); err != nil {
		return nil, err
	}
	return order, nil
}

// Delete removes the manifest ref names, which must carry a digest
// ("registry/repo@sha256:..."; a tag alone is [ErrInvalidArtifact]),
// together with its referrers at every level: signatures, attestations and
// their own signatures. Referrers come from the OCI 1.1 referrers API and
// the referrers tag schema index ("sha256-<hex>"), whose tag is deleted
// once its referrers are; each listed referrer must name its subject, else
// nothing is deleted. Referrers go first and the subject last, so a failed
// delete can be retried. An index subject keeps its child manifests, which
// other indexes may share; registry garbage collection owns untagged ones.
//
// Delete is idempotent: manifests already gone are reported Absent, not
// failed. A registry refusing deletion is [ErrDeleteUnsupported]; a failure
// after something was removed is [ErrPartialDelete], with the report
// listing what is gone. DryRun lists and probes, deleting nothing; it
// cannot tell whether the registry allows deletion.
func (c *Client) Delete(ctx context.Context, ref string, opts DeleteOptions) (DeleteReport, error) {
	subject, err := name.NewDigest(ref)
	if err != nil {
		return DeleteReport{}, fmt.Errorf("%w: delete target %q must name a digest: %w", ErrInvalidArtifact, ref, err)
	}
	h, err := v1.NewHash(subject.DigestStr())
	if err != nil {
		return DeleteReport{}, fmt.Errorf("%w: delete target %q: %w", ErrInvalidArtifact, ref, err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	d, err := c.newRemoteDelete(subject.Context(), opts.DryRun)
	if err != nil {
		return DeleteReport{}, err
	}
	order, err := c.limits.referrerTree(ctx, d.referrers, []v1.Hash{h})
	if err != nil {
		return DeleteReport{}, err
	}
	return d.run(ctx, order)
}

// remoteDelete removes manifests and referrers tags from one repository.
type remoteDelete struct {
	c      *Client
	puller *remote.Puller
	pusher *remote.Pusher
	repo   name.Repository
	dry    bool
	// tags maps a manifest to its referrers tag schema index.
	tags map[v1.Hash]v1.Hash
}

func (c *Client) newRemoteDelete(repo name.Repository, dry bool) (*remoteDelete, error) {
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("registry: build puller: %w", err)
	}
	pusher, err := remote.NewPusher(c.remoteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("registry: build pusher: %w", err)
	}
	return &remoteDelete{c: c, puller: puller, pusher: pusher, repo: repo, dry: dry, tags: make(map[v1.Hash]v1.Hash)}, nil
}

// referrers lists h's referrers from the referrers API (or tag schema) and
// from the tag schema index, which still holds referrers pushed before a
// registry gained the API, and checks that each one names h.
func (d *remoteDelete) referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	listed, err := d.c.referrersOf(ctx, d.repo.Digest(h.String()), "")
	if err != nil {
		return nil, err
	}
	tagged, err := d.tagIndex(ctx, h)
	if err != nil {
		return nil, err
	}
	all, err := d.c.limits.capReferrers(mergeDescriptors(listed, tagged), h.String())
	if err != nil {
		return nil, err
	}
	for _, r := range all {
		if err = d.checkReferrer(ctx, r.Digest, h); err != nil {
			return nil, err
		}
	}
	return all, nil
}

// tagIndex reads the referrers tag schema index of h, if one exists.
func (d *remoteDelete) tagIndex(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	tag := d.repo.Tag(schemaTag(h))
	got, err := d.puller.Get(ctx, tag)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry: referrers tag %q: %w", tag.String(), err)
	}
	if !got.MediaType.IsIndex() {
		return nil, nil // not a referrers list: someone else's tag
	}
	if got.Size > d.c.limits.manifestBytes {
		return nil, fmt.Errorf("%w: referrers tag %q is %d bytes > %d", ErrTooLarge, tag.String(), got.Size, d.c.limits.manifestBytes)
	}
	im, err := v1.ParseIndexManifest(bytes.NewReader(got.Manifest))
	if err != nil {
		return nil, fmt.Errorf("%w: decode referrers tag %q: %w", ErrInvalidArtifact, tag.String(), err)
	}
	d.tags[h] = got.Digest
	return im.Manifests, nil
}

// checkReferrer refuses a listed referrer whose manifest names another
// subject: a stale or forged tag schema index must not delete unrelated
// manifests. A referrer already gone passes; its delete reports it absent.
func (d *remoteDelete) checkReferrer(ctx context.Context, r, subject v1.Hash) error {
	got, err := d.puller.Get(ctx, d.repo.Digest(r.String()))
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("registry: fetch referrer %s: %w", r, err)
	}
	if got.Size > d.c.limits.manifestBytes {
		return fmt.Errorf("%w: referrer %s is %d bytes > %d", ErrTooLarge, r, got.Size, d.c.limits.manifestBytes)
	}
	var f referrerFields
	if err = json.Unmarshal(got.Manifest, &f); err != nil {
		return fmt.Errorf("%w: decode referrer %s: %w", ErrInvalidArtifact, r, err)
	}
	if f.Subject == nil || f.Subject.Digest != subject {
		return fmt.Errorf("%w: %s is listed as a referrer of %s but names another subject", ErrInvalidArtifact, r, subject)
	}
	return nil
}

// run deletes each manifest of order after its referrers tag schema index.
func (d *remoteDelete) run(ctx context.Context, order []v1.Hash) (DeleteReport, error) {
	var rep DeleteReport
	for _, h := range order {
		if err := d.removeTag(ctx, h, &rep); err != nil {
			return rep, incomplete(rep.changed(), err)
		}
		if err := d.remove(ctx, h, &rep); err != nil {
			return rep, incomplete(rep.changed(), err)
		}
	}
	return rep, nil
}

// remove deletes manifest h, or on a dry run checks that it exists.
func (d *remoteDelete) remove(ctx context.Context, h v1.Hash, rep *DeleteReport) error {
	target := d.repo.Digest(h.String())
	var err error
	if d.dry {
		_, err = d.puller.Head(ctx, target)
	} else {
		err = d.pusher.Delete(ctx, target)
	}
	switch {
	case err == nil:
		rep.Removed = append(rep.Removed, h)
	case isNotFound(err):
		rep.Absent = append(rep.Absent, h)
	default:
		return deleteError(target, err)
	}
	return nil
}

// removeTag deletes the referrers tag schema index of h, by tag or, where
// the registry deletes manifests by digest only, by its digest.
func (d *remoteDelete) removeTag(ctx context.Context, h v1.Hash, rep *DeleteReport) error {
	idx, ok := d.tags[h]
	if !ok {
		return nil
	}
	tag := d.repo.Tag(schemaTag(h))
	if !d.dry {
		err := d.pusher.Delete(ctx, tag)
		if isUnsupported(err) {
			err = d.pusher.Delete(ctx, d.repo.Digest(idx.String()))
		}
		if err != nil && !isNotFound(err) {
			return deleteError(tag, err)
		}
	}
	rep.Tags = append(rep.Tags, tag.TagStr())
	return nil
}

func deleteError(target name.Reference, err error) error {
	if isUnsupported(err) {
		return fmt.Errorf("%w: %s: %w", ErrDeleteUnsupported, target.String(), err)
	}
	return fmt.Errorf("registry: delete %s: %w", target.String(), err)
}

// schemaTag is the referrers tag schema tag of h: "<alg>-<hex>".
func schemaTag(h v1.Hash) string { return h.Algorithm + "-" + h.Hex }

// mergeDescriptors appends the entries of b whose digest a lacks.
func mergeDescriptors(a, b []v1.Descriptor) []v1.Descriptor {
	seen := make(map[v1.Hash]struct{}, len(a))
	for _, d := range a {
		seen[d.Digest] = struct{}{}
	}
	out := slices.Clone(a)
	for _, d := range b {
		if _, dup := seen[d.Digest]; !dup {
			seen[d.Digest] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}

func isNotFound(err error) bool {
	var terr *transport.Error
	return errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound
}

// isUnsupported matches the distribution spec's answers to a disabled
// delete: 405, 400, or the UNSUPPORTED error code.
func isUnsupported(err error) bool {
	var terr *transport.Error
	if !errors.As(err, &terr) {
		return false
	}
	if terr.StatusCode == http.StatusMethodNotAllowed || terr.StatusCode == http.StatusBadRequest {
		return true
	}
	for _, e := range terr.Errors {
		if e.Code == transport.UnsupportedErrorCode {
			return true
		}
	}
	return false
}
