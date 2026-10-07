// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"testing"
	"time"
)

func TestMigrationUnlockContextIsIndependentAndBounded(t *testing.T) {
	t.Parallel()

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()
	ctx, cancel := migrationUnlockContext(parent)
	defer cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("unlock context inherited cancellation: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("unlock context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > migrationUnlockTimeout {
		t.Fatalf("unlock deadline remaining = %v, want (0, %v]", remaining, migrationUnlockTimeout)
	}
}

func TestMigrationConfigHasExplicitLogger(t *testing.T) {
	t.Parallel()

	config := migrationConfig()
	if config == nil || config.Logger == nil {
		t.Fatal("migration config has no explicit logger")
	}
}
