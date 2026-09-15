// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ListTags returns every tag in repo (e.g. "gcr.io/proj/img"), calling the
// registry's `/tags/list` endpoint.
func (c *Client) ListTags(ctx context.Context, repo string) ([]string, error) {
	r, err := name.NewRepository(repo)
	if err != nil {
		return nil, fmt.Errorf("registry: parse repository %q: %w", repo, err)
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("registry: build puller: %w", err)
	}
	tags, err := puller.List(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("registry: list tags %q: %w", repo, err)
	}
	return tags, nil
}
