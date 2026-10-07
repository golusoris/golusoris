// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Referrers lists the manifests whose subject is the manifest ref resolves
// to — signatures, attestations, SBOMs — filtered by artifactType when
// non-empty. It uses the OCI 1.1 referrers API and falls back to the
// referrers tag schema ("sha256-<hex>") on registries without it, which is
// where cosign and [Client.PushArtifact] record referrers there. More than
// Options.MaxReferrers results is [ErrTooLarge], never a silent truncation.
func (c *Client) Referrers(ctx context.Context, ref, artifactType string) ([]v1.Descriptor, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	subject, ok := r.(name.Digest)
	if !ok {
		puller, perr := remote.NewPuller(c.remoteOptions()...)
		if perr != nil {
			return nil, fmt.Errorf("registry: build puller: %w", perr)
		}
		desc, herr := puller.Head(ctx, r)
		if herr != nil {
			return nil, fmt.Errorf("registry: resolve %q: %w", ref, herr)
		}
		subject = r.Context().Digest(desc.Digest.String())
	}
	opts := append(c.remoteOptions(), remote.WithContext(ctx))
	if artifactType != "" {
		opts = append(opts, remote.WithFilter("artifactType", artifactType))
	}
	idx, err := remote.Referrers(subject, opts...)
	if err != nil {
		return nil, fmt.Errorf("registry: referrers of %q: %w", subject.String(), err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("registry: referrers of %q: %w", subject.String(), err)
	}
	if len(im.Manifests) > c.limits.referrers {
		return nil, fmt.Errorf("%w: %d referrers of %s > %d", ErrTooLarge, len(im.Manifests), subject.String(), c.limits.referrers)
	}
	return im.Manifests, nil
}
