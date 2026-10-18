// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storetest

import (
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
)

// Windows CI runners outlast the 5s default on a cold open (#778); applications keep the default.
const testBusyTimeout = 30 * time.Second

// OpenSQLite opens a file database so concurrent claims use real locking.
func OpenSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dbsqlite.Open(t.Context(), dbsqlite.Options{
		Path:        filepath.Join(t.TempDir(), "idempotency.db"),
		BusyTimeout: testBusyTimeout,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
