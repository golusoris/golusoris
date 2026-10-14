// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/log"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func testMigrationsFS() fstest.MapFS {
	return fstest.MapFS{
		"migrations/1_first.up.sql":    {Data: []byte("CREATE TABLE step_probe_1 (id int);")},
		"migrations/1_first.down.sql":  {Data: []byte("DROP TABLE step_probe_1;")},
		"migrations/2_second.up.sql":   {Data: []byte("CREATE TABLE step_probe_2 (id int);")},
		"migrations/2_second.down.sql": {Data: []byte("DROP TABLE step_probe_2;")},
	}
}

// TestMigrator_DownRoundTrip exercises Down against a real DB: Up -> Down -> Up round trip.
func TestMigrator_DownRoundTrip(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)

	m, err := dbmigrate.New(
		dbmigrate.Options{FS: testMigrationsFS()},
		dbpgx.Options{DSN: dsn},
		log.New(log.Options{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	// Initial Up: all migrations applied
	require.NoError(t, m.Up())
	v, dirty, err := m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(2), v)
	require.False(t, dirty)

	// Down: rolls back all migrations
	require.NoError(t, m.Down())
	v, dirty, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(0), v)
	require.False(t, dirty)

	// Down again is idempotent (ErrNoChange treated as success)
	require.NoError(t, m.Down())

	// Up again completes round trip
	require.NoError(t, m.Up())
	v, dirty, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(2), v)
	require.False(t, dirty)
}

// TestMigrator_Steps exercises Steps(+n), Steps(-n), and Steps beyond available.
func TestMigrator_Steps(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)

	m, err := dbmigrate.New(
		dbmigrate.Options{FS: testMigrationsFS()},
		dbpgx.Options{DSN: dsn},
		log.New(log.Options{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	// Steps(+1): apply first migration
	require.NoError(t, m.Steps(1))
	v, dirty, err := m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(1), v)
	require.False(t, dirty)

	// Steps(+1): apply second migration
	require.NoError(t, m.Steps(1))
	v, dirty, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(2), v)
	require.False(t, dirty)

	// Steps(-1): revert second migration
	require.NoError(t, m.Steps(-1))
	v, dirty, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(1), v)
	require.False(t, dirty)

	// Steps(-1): revert first migration
	require.NoError(t, m.Steps(-1))
	v, dirty, err = m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(0), v)
	require.False(t, dirty)

	// Steps beyond available: error
	require.Error(t, m.Steps(100))
}
