// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package postgres_test

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/postgres"
)

func TestStore_ValidatesBeforeIO(t *testing.T) {
	t.Parallel()
	var clk *clockwork.FakeClock
	value, err := postgres.New(&pgxpool.Pool{}, clk)
	require.NoError(t, err)
	_, err = value.Claim(t.Context(), "key", "fp", 0)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = value.Claim(ctx, "key", "fp", time.Minute)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStore_MigrationsEmbedded(t *testing.T) {
	t.Parallel()
	files, err := fs.Glob(postgres.MigrationsFS, "migrations/*.sql")
	require.NoError(t, err)
	require.Len(t, files, 2)
	for _, name := range files {
		content, readErr := postgres.MigrationsFS.ReadFile(name)
		require.NoError(t, readErr)
		require.NotEmpty(t, content)
	}
}
