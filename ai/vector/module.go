package vector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module registers the pgvector types on the injected pgx pool at startup, so
// vector columns scan/encode correctly across the app.
//
//	fx.New(golusoris.Core, golusoris.DB, vector.Module)
//
// Requires a *pgxpool.Pool (via golusoris.DB). It provides no new type — it
// configures the existing pool. Use the package's SimilaritySearch / From
// helpers directly once registered.
var Module = fx.Module("golusoris.ai.vector",
	fx.Invoke(func(lc fx.Lifecycle, pool *pgxpool.Pool) {
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				if err := RegisterTypes(ctx, pool); err != nil {
					return fmt.Errorf("ai/vector: register types: %w", err)
				}
				return nil
			},
		})
	}),
)
