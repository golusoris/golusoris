// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats

import (
	"context"
	"errors"
	"testing"
)

func TestPublishSyncPreCanceledDoesNotPublish(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &Client{}
	err := client.PublishSync(ctx, "events.test", []byte("must-not-send"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PublishSync error = %v, want context.Canceled", err)
	}
}
