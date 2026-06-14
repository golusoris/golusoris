package migrate_test

import (
	"testing"
	"testing/fstest"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/log"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// TestForceResetsRecordedVersion exercises Force against a real DB: after Up,
// Force(0) resets the recorded version + clears dirty without dropping the
// table (bookkeeping only). Skips when Docker is unavailable (pgtest).
func TestForceResetsRecordedVersion(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)

	fsys := fstest.MapFS{
		"migrations/1_init.up.sql":   {Data: []byte("CREATE TABLE force_probe (id int);")},
		"migrations/1_init.down.sql": {Data: []byte("DROP TABLE force_probe;")},
	}
	m, err := dbmigrate.New(
		dbmigrate.Options{FS: fsys},
		dbpgx.Options{DSN: dsn},
		log.New(log.Options{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if v, dirty, _ := m.Version(); v != 1 || dirty {
		t.Fatalf("after Up: v=%d dirty=%v, want 1,false", v, dirty)
	}

	if err := m.Force(0); err != nil {
		t.Fatalf("Force(0): %v", err)
	}
	if v, dirty, _ := m.Version(); v != 0 || dirty {
		t.Errorf("after Force(0): v=%d dirty=%v, want 0,false", v, dirty)
	}
}
