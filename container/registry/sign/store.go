// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/golusoris/golusoris/container/registry"
)

// store is where a signed manifest and its signatures live: a registry
// repository or an OCI image layout. Signing and verification run once over
// it, so both places get the same checks and the same wire format.
type store interface {
	manifest(ctx context.Context, h v1.Hash) (*registry.Manifest, error)
	push(ctx context.Context, a registry.Artifact) (v1.Descriptor, error)
	referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error)
	artifactManifest(ctx context.Context, h v1.Hash) (v1.Descriptor, *v1.Manifest, error)
	fetchBlob(ctx context.Context, d v1.Descriptor, maxBytes int64) ([]byte, error)
}

// remoteStore is one registry repository.
type remoteStore struct {
	c    *registry.Client
	repo name.Repository
}

func (s remoteStore) ref(h v1.Hash) string { return s.repo.Digest(h.String()).String() }

func (s remoteStore) manifest(ctx context.Context, h v1.Hash) (*registry.Manifest, error) {
	return s.c.Manifest(ctx, s.ref(h)) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s remoteStore) push(ctx context.Context, a registry.Artifact) (v1.Descriptor, error) {
	return s.c.PushArtifact(ctx, s.repo.Name(), a) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s remoteStore) referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	return s.c.Referrers(ctx, s.ref(h), "") //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s remoteStore) artifactManifest(ctx context.Context, h v1.Hash) (v1.Descriptor, *v1.Manifest, error) {
	return s.c.ArtifactManifest(ctx, s.ref(h)) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s remoteStore) fetchBlob(ctx context.Context, d v1.Descriptor, maxBytes int64) ([]byte, error) {
	return s.c.FetchBlob(ctx, s.repo.Name(), d, maxBytes) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

// layoutStore is one OCI image layout.
type layoutStore struct{ l *registry.Layout }

func (s layoutStore) manifest(ctx context.Context, h v1.Hash) (*registry.Manifest, error) {
	return s.l.Manifest(ctx, h) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s layoutStore) push(ctx context.Context, a registry.Artifact) (v1.Descriptor, error) {
	return s.l.PushArtifact(ctx, a) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s layoutStore) referrers(ctx context.Context, h v1.Hash) ([]v1.Descriptor, error) {
	return s.l.Referrers(ctx, h, "") //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s layoutStore) artifactManifest(ctx context.Context, h v1.Hash) (v1.Descriptor, *v1.Manifest, error) {
	return s.l.ArtifactManifest(ctx, h) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}

func (s layoutStore) fetchBlob(ctx context.Context, d v1.Descriptor, maxBytes int64) ([]byte, error) {
	return s.l.FetchBlob(ctx, d, maxBytes) //nolint:wrapcheck // WHY: callers wrap with the sign operation.
}
