package pgx_test

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

// ExampleModule shows the minimal wiring needed to get a *pgxpool.Pool. The
// db.dsn config key (env: APP_DB_DSN) is the only required setting.
func ExampleModule() {
	app := fx.New(
		fx.NopLogger,
		golusoris.Core,
		dbpgx.Module,
	)
	_ = app // app.Run() in production
	// Output:
}
