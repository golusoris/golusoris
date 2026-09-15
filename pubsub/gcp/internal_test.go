// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp

import (
	"log/slog"
	"testing"

	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

// TestNewFromConfig_MissingProjectID is the negative case: an empty
// "pubsub.gcp.project_id" must fail fast, before any network I/O.
func TestNewFromConfig_MissingProjectID(t *testing.T) {
	t.Parallel()

	cfg, err := config.New(config.Options{Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	_, err = newFromConfig(params{
		Config: cfg,
		Logger: slog.New(slog.DiscardHandler),
		LC:     fxtest.NewLifecycle(t),
	})
	if err == nil {
		t.Fatal("expected error for missing project_id, got nil")
	}
}

// TestClientFromPubsub_DefaultLogger confirms the test/advanced-use
// constructor never leaves a nil logger, mirroring kafka.ClientFromKgo.
func TestClientFromPubsub_DefaultLogger(t *testing.T) {
	t.Parallel()

	c := ClientFromPubsub(nil)
	if c.logger == nil {
		t.Fatal("expected non-nil default logger")
	}
	if c.pubs == nil {
		t.Fatal("expected initialized publisher cache")
	}
}
