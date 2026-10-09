// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// manifestRole says how a store addresses a manifest besides its digest.
type manifestRole int

const (
	// roleChild is reachable through its parent index only.
	roleChild manifestRole = iota
	// roleRoot is the pushed or copied manifest: tagged, or listed in index.json.
	roleRoot
	// roleReferrer is listed in index.json, never tagged.
	roleReferrer
)

// sink stores verified content: a registry repository or an image layout.
type sink interface {
	putBlob(ctx context.Context, d v1.Descriptor, open func() (io.ReadCloser, error)) error
	putManifest(ctx context.Context, d v1.Descriptor, raw []byte, role manifestRole) error
}

// source serves content: manifests verified against their digest, blobs as
// verified streams.
type source interface {
	manifest(ctx context.Context, d v1.Descriptor) ([]byte, error)
	blob(ctx context.Context, d v1.Descriptor) (io.ReadCloser, error)
	referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error)
}

// remoteSink pushes into one repository; only the root manifest gets the tag.
type remoteSink struct {
	p    *remote.Pusher
	repo name.Repository
	tag  string
}

func (s remoteSink) putBlob(ctx context.Context, d v1.Descriptor, open func() (io.ReadCloser, error)) error {
	l, err := partial.CompressedToLayer(&openLayer{d: d, open: open})
	if err != nil {
		return fmt.Errorf("registry: layer %s: %w", d.Digest, err)
	}
	if err = s.p.Upload(ctx, s.repo, l); err != nil {
		return fmt.Errorf("registry: upload %s: %w", d.Digest, err)
	}
	return nil
}

func (s remoteSink) putManifest(ctx context.Context, d v1.Descriptor, raw []byte, role manifestRole) error {
	var target name.Reference = s.repo.Digest(d.Digest.String())
	if role == roleRoot && s.tag != "" {
		target = s.repo.Tag(s.tag)
	}
	if err := s.p.Put(ctx, target, rawManifest{raw: raw, mediaType: d.MediaType}); err != nil {
		return fmt.Errorf("registry: push manifest %q: %w", target.String(), err)
	}
	return nil
}

// remoteSource reads one repository by digest.
type remoteSource struct {
	c    *Client
	p    *remote.Puller
	repo name.Repository
}

// manifest relies on go-containerregistry rejecting a body that does not hash
// to the requested digest; plan.load checks the size.
func (s remoteSource) manifest(ctx context.Context, d v1.Descriptor) ([]byte, error) {
	got, err := s.p.Get(ctx, s.repo.Digest(d.Digest.String()))
	if err != nil {
		return nil, fmt.Errorf("registry: fetch manifest %s: %w", d.Digest, err)
	}
	return got.Manifest, nil
}

func (s remoteSource) blob(ctx context.Context, d v1.Descriptor) (io.ReadCloser, error) {
	return openRemoteBlob(ctx, s.p, s.repo, d)
}

func (s remoteSource) referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	return s.c.referrersOf(ctx, s.repo.Digest(h.String()), "")
}

// CopyToLayout copies the image, index or artifact src names (tag or
// digest) into l: every manifest and blob it references, and the referrers
// of each of those manifests (signatures, attestations, SBOMs). src and its
// referrers are listed in l's index.json; blobs already in l with the right
// digest are kept. Every manifest and blob is verified against its digest and
// the client's size caps before it lands. It returns src's descriptor.
func (c *Client) CopyToLayout(ctx context.Context, src string, l *Layout) (v1.Descriptor, error) {
	if l == nil {
		return v1.Descriptor{}, fmt.Errorf("%w: nil layout", ErrInvalidArtifact)
	}
	r, err := ParseReference(src)
	if err != nil {
		return v1.Descriptor{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	l.gc.RLock()
	defer l.gc.RUnlock()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: build puller: %w", err)
	}
	head, err := puller.Head(ctx, r)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: resolve %q: %w", src, err)
	}
	root := v1.Descriptor{MediaType: head.MediaType, Digest: head.Digest, Size: head.Size}
	if err = c.limits.copyGraph(ctx, remoteSource{c: c, p: puller, repo: r.Context()}, layoutSink{l: l}, root); err != nil {
		return v1.Descriptor{}, err
	}
	return root, nil
}

// CopyFromLayout copies the manifest digest names in l to dst
// ("registry/repository" or "registry/repository:tag"): every manifest and
// blob it references, and the referrers l holds for each of those manifests.
// Only the root gets the tag; registries without the OCI 1.1 referrers API
// get the referrers tag schema updated. Every manifest and blob read from l
// is verified against its digest before upload. It returns the root
// descriptor.
func (c *Client) CopyFromLayout(ctx context.Context, l *Layout, digest v1.Hash, dst string) (v1.Descriptor, error) {
	if l == nil {
		return v1.Descriptor{}, fmt.Errorf("%w: nil layout", ErrInvalidArtifact)
	}
	repo, tag, err := parsePushTarget(dst)
	if err != nil {
		return v1.Descriptor{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.limits.transfer)
	defer cancel()
	m, err := l.manifest(ctx, digest)
	if err != nil {
		return v1.Descriptor{}, err
	}
	pusher, err := remote.NewPusher(c.remoteOptions()...)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("registry: build pusher: %w", err)
	}
	root := v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}
	if err = c.limits.copyGraph(ctx, &layoutSource{l: l}, remoteSink{p: pusher, repo: repo, tag: tag}, root); err != nil {
		return v1.Descriptor{}, err
	}
	return root, nil
}

// node is one manifest of a copy: its bytes, blobs and child manifests.
type node struct {
	desc  v1.Descriptor
	raw   []byte
	blobs []v1.Descriptor
	kids  []v1.Descriptor
	role  manifestRole
}

// frame is a pending visit; done marks a manifest whose children are planned.
type frame struct {
	d    v1.Descriptor
	done bool
}

// plan orders a copy so every manifest follows its blobs and child
// manifests, the order registries accept an index in.
type plan struct {
	lim   limits
	src   source
	nodes map[v1.Hash]*node
	order []*node
	blobs map[v1.Hash]struct{}
	total int64
}

// copyGraph copies root, its content and the referrers of each of its
// manifests from src to dst. Referrers of referrers stay behind.
func (lim limits) copyGraph(ctx context.Context, src source, dst sink, root v1.Descriptor) error {
	p := &plan{lim: lim, src: src, nodes: make(map[v1.Hash]*node), blobs: make(map[v1.Hash]struct{})}
	if err := p.walk(ctx, root); err != nil {
		return err
	}
	p.nodes[root.Digest].role = roleRoot
	if err := p.addReferrers(ctx); err != nil {
		return err
	}
	return p.run(ctx, dst)
}

// maxManifests bounds one copy: the root, its index entries, its referrers.
func (p *plan) maxManifests() int { return 1 + p.lim.blobs + p.lim.referrers }

// walk plans root and every manifest below it, children first, on the
// explicit stack walkFrames bounds by the manifest and entry caps.
func (p *plan) walk(ctx context.Context, root v1.Descriptor) error {
	maxSteps := 2 * p.maxManifests() * (p.lim.blobs + 1)
	return walkFrames([]frame{{d: root}}, maxSteps, func(f frame) ([]frame, error) { return p.visit(ctx, f) })
}

// walkFrames runs visit over an explicit stack, no recursion (HISS-01):
// each step pops one frame and pushes the frames visit returns. More than
// maxSteps steps is [ErrTooLarge].
func walkFrames(start []frame, maxSteps int, visit func(frame) ([]frame, error)) error {
	stack := slices.Clone(start)
	for step := 0; step < maxSteps && len(stack) > 0; step++ {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		next, err := visit(top)
		if err != nil {
			return err
		}
		stack = append(stack, next...)
	}
	if len(stack) > 0 {
		return fmt.Errorf("%w: graph walk exceeds %d steps", ErrTooLarge, maxSteps)
	}
	return nil
}

// visit emits a planned manifest, or loads a new one and returns the frames
// that emit it after its children.
func (p *plan) visit(ctx context.Context, f frame) ([]frame, error) {
	if f.done {
		p.order = append(p.order, p.nodes[f.d.Digest])
		return nil, nil
	}
	if _, seen := p.nodes[f.d.Digest]; seen {
		return nil, nil
	}
	n, err := p.load(ctx, f.d)
	if err != nil {
		return nil, err
	}
	p.nodes[f.d.Digest] = n
	next := make([]frame, 0, len(n.kids)+1)
	next = append(next, frame{d: f.d, done: true})
	for _, k := range n.kids {
		next = append(next, frame{d: k})
	}
	return next, nil
}

// load fetches manifest d, checks it against its descriptor and the caps,
// and records its blobs and child manifests.
func (p *plan) load(ctx context.Context, d v1.Descriptor) (*node, error) {
	if len(p.nodes) >= p.maxManifests() {
		return nil, fmt.Errorf("%w: more than %d manifests", ErrTooLarge, p.maxManifests())
	}
	if !isManifest(d.MediaType) {
		return nil, fmt.Errorf("%w: %s is %q, want a manifest", ErrInvalidArtifact, d.Digest, d.MediaType)
	}
	if d.Size < 0 || d.Size > p.lim.manifestBytes {
		return nil, fmt.Errorf("%w: manifest %s is %d bytes, limit %d", ErrTooLarge, d.Digest, d.Size, p.lim.manifestBytes)
	}
	raw, err := p.src.manifest(ctx, d)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != d.Size {
		return nil, fmt.Errorf("%w: manifest %s is %d bytes, descriptor says %d", ErrDigestMismatch, d.Digest, len(raw), d.Size)
	}
	if err = p.count(d.Size); err != nil {
		return nil, err
	}
	n := &node{desc: d, raw: raw}
	return n, p.addContent(n)
}

// addContent records the child manifests and blobs of n.
func (p *plan) addContent(n *node) error {
	kids, blobs, err := contentOf(n.desc.Digest, n.desc.MediaType, n.raw, p.lim.blobs)
	if err != nil {
		return err
	}
	n.kids = kids
	for _, b := range blobs {
		if err = p.addBlob(n, b); err != nil {
			return err
		}
	}
	return nil
}

func indexContent(h v1.Hash, raw []byte, maxEntries int) (kids, blobs []v1.Descriptor, err error) {
	im, err := v1.ParseIndexManifest(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: decode index %s: %w", ErrInvalidArtifact, h, err)
	}
	if len(im.Manifests) > maxEntries {
		return nil, nil, fmt.Errorf("%w: index %s has %d entries > %d", ErrTooLarge, h, len(im.Manifests), maxEntries)
	}
	for _, d := range im.Manifests {
		if isManifest(d.MediaType) {
			kids = append(kids, d)
		} else {
			blobs = append(blobs, d)
		}
	}
	return kids, blobs, nil
}

// contentOf parses manifest raw of media type mt into its child manifests
// and its blobs (config, layers, other index entries), refusing more than
// maxEntries index entries or layers.
func contentOf(h v1.Hash, mt types.MediaType, raw []byte, maxEntries int) (kids, blobs []v1.Descriptor, err error) {
	if mt.IsIndex() {
		return indexContent(h, raw, maxEntries)
	}
	if !mt.IsImage() {
		return nil, nil, fmt.Errorf("%w: %s is %q, want a manifest", ErrInvalidArtifact, h, mt)
	}
	m, err := v1.ParseManifest(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: decode manifest %s: %w", ErrInvalidArtifact, h, err)
	}
	if len(m.Layers) > maxEntries {
		return nil, nil, fmt.Errorf("%w: manifest %s has %d layers > %d", ErrTooLarge, h, len(m.Layers), maxEntries)
	}
	return nil, append([]v1.Descriptor{m.Config}, m.Layers...), nil
}

// addBlob records blob d of n; foreign layers stay where their URLs point.
func (p *plan) addBlob(n *node, d v1.Descriptor) error {
	if !d.MediaType.IsDistributable() {
		return nil
	}
	if err := checkDigest(d.Digest); err != nil {
		return err
	}
	if d.Size < 0 || d.Size > p.lim.blobBytes {
		return fmt.Errorf("%w: blob %s is %d bytes, limit %d", ErrTooLarge, d.Digest, d.Size, p.lim.blobBytes)
	}
	n.blobs = append(n.blobs, d)
	if _, dup := p.blobs[d.Digest]; dup {
		return nil
	}
	p.blobs[d.Digest] = struct{}{}
	return p.count(d.Size)
}

func (p *plan) count(size int64) error {
	if p.total += size; p.total > p.lim.totalBytes {
		return fmt.Errorf("%w: content exceeds %d bytes in total", ErrTooLarge, p.lim.totalBytes)
	}
	return nil
}

// addReferrers plans the referrers of every manifest planned so far.
func (p *plan) addReferrers(ctx context.Context) error {
	for _, s := range slices.Clone(p.order) {
		refs, err := p.src.referrers(ctx, s.desc.Digest)
		if err != nil {
			return err
		}
		for _, r := range refs {
			if err = p.addReferrer(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *plan) addReferrer(ctx context.Context, r v1.Descriptor) error {
	if _, seen := p.nodes[r.Digest]; seen {
		return nil
	}
	if err := p.walk(ctx, r); err != nil {
		return err
	}
	n := p.nodes[r.Digest]
	n.role = roleReferrer
	// The manifest, not the listing, names the artifact type: registries
	// disagree on what a referrers listing reports.
	var f referrerFields
	if err := json.Unmarshal(n.raw, &f); err != nil {
		return fmt.Errorf("%w: decode referrer %s: %w", ErrInvalidArtifact, r.Digest, err)
	}
	n.desc.ArtifactType = f.artifactType()
	return nil
}

// run stores each manifest after its not yet stored blobs.
func (p *plan) run(ctx context.Context, dst sink) error {
	sent := make(map[v1.Hash]struct{}, len(p.blobs))
	for _, n := range p.order {
		for _, b := range n.blobs {
			if _, ok := sent[b.Digest]; ok {
				continue
			}
			if err := dst.putBlob(ctx, b, p.opener(ctx, b)); err != nil {
				return err
			}
			sent[b.Digest] = struct{}{}
		}
		if err := dst.putManifest(ctx, n.desc, n.raw, n.role); err != nil {
			return err
		}
	}
	return nil
}

func (p *plan) opener(ctx context.Context, d v1.Descriptor) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return p.src.blob(ctx, d) }
}
