// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Copy copies the image or index at src to dst, preserving its manifest
// exactly: one fetch from src, one push to dst. src and dst may be on
// different registries. The pushed descriptor's [remote.Taggable] is
// whatever [remote.Puller.Get] returned (an image manifest or an index),
// so multi-platform images copy without needing to know which one it is.
func (c *Client) Copy(ctx context.Context, src, dst string) error {
	srcRef, err := ParseReference(src)
	if err != nil {
		return err
	}
	dstRef, err := ParseReference(dst)
	if err != nil {
		return err
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return fmt.Errorf("registry: build puller: %w", err)
	}
	pusher, err := remote.NewPusher(c.remoteOptions()...)
	if err != nil {
		return fmt.Errorf("registry: build pusher: %w", err)
	}
	desc, err := puller.Get(ctx, srcRef)
	if err != nil {
		return fmt.Errorf("registry: fetch %q: %w", src, err)
	}
	if err := pusher.Push(ctx, dstRef, desc); err != nil {
		return fmt.Errorf("registry: push %q: %w", dst, err)
	}
	return nil
}
