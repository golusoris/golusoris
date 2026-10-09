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
	subject, ok := r.(name.Digest)
	if !ok {
		if subject, err = c.resolve(ctx, r); err != nil {
			return nil, err
		}
	}
	return c.referrersOf(ctx, subject, artifactType)
}

// resolve maps r to its digest with one bounded manifest HEAD.
func (c *Client) resolve(ctx context.Context, r name.Reference) (name.Digest, error) {
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return name.Digest{}, fmt.Errorf("registry: build puller: %w", err)
	}
	desc, err := puller.Head(ctx, r)
	if err != nil {
		return name.Digest{}, fmt.Errorf("registry: resolve %q: %w", r.String(), err)
	}
	return r.Context().Digest(desc.Digest.String()), nil
}

// referrersOf lists the referrers of subject in one bounded call.
func (c *Client) referrersOf(ctx context.Context, subject name.Digest, artifactType string) ([]v1.Descriptor, error) {
	ctx, cancel := c.bound(ctx)
	defer cancel()
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
	return c.limits.capReferrers(im.Manifests, subject.String())
}
