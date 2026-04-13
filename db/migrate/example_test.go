package migrate_test

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

// ExampleModule wires migrate alongside pgx. By default migrations are not
// run automatically; set db.migrate.auto=true to run Up() during fx Start.
func ExampleModule() {
	app := fx.New(
		fx.NopLogger,
		golusoris.Core,
		dbpgx.Module,
		dbmigrate.Module,
	)
	_ = app
	// Output:
}
