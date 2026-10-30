// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vector_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/ai/vector"
	"github.com/golusoris/golusoris/internal/testimages"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func TestFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []float32
		wantSlice []float32
		wantStr   string
	}{
		{
			name:      "positive standard embedding",
			input:     []float32{1.0, 2.5, -3.0},
			wantSlice: []float32{1.0, 2.5, -3.0},
			wantStr:   "[1,2.5,-3]",
		},
		{
			name:      "boundary single element",
			input:     []float32{0.0},
			wantSlice: []float32{0.0},
			wantStr:   "[0]",
		},
		{
			name:      "boundary empty slice",
			input:     []float32{},
			wantSlice: []float32{},
			wantStr:   "[]",
		},
		{
			name:      "boundary nil slice",
			input:     nil,
			wantSlice: nil,
			wantStr:   "[]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := vector.From(tc.input)
			require.Equal(t, tc.wantSlice, got.Slice())
			require.Equal(t, tc.wantStr, got.String())
		})
	}
}

func TestMetricConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, vector.Metric("<=>"), vector.Cosine)
	require.Equal(t, vector.Metric("<->"), vector.L2)
	require.Equal(t, vector.Metric("<#>"), vector.InnerProduct)
}

// TestPostgres runs against one pgvector container; subtests are sequential
// because the first ones need the extension absent.
//
//nolint:paralleltest,tparallel // subtests share one database and run in order.
func TestPostgres(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t, pgtest.Options{Image: testimages.Pgvector})
	ctx := t.Context()

	t.Run("negative register without extension", func(t *testing.T) {
		testPostgresUnregistered(t, ctx, pool)
	})

	seedVectorItems(t, ctx, pool)

	t.Run("positive register and module start", func(t *testing.T) {
		testPostgresRegisterModule(t, ctx, pool)
	})

	testPostgresSearches(t, ctx, pool)
}

func testPostgresUnregistered(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	require.ErrorContains(t, vector.RegisterTypes(ctx, pool), "vector: register types")
	app := fx.New(fx.NopLogger, fx.Supply(pool), vector.Module)
	require.ErrorContains(t, app.Start(ctx), "ai/vector: register types")
}

func seedVectorItems(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `CREATE EXTENSION vector;
		CREATE TABLE items (id text PRIMARY KEY, tenant int NOT NULL, embedding vector(2) NOT NULL)`)
	require.NoError(t, err)
	for _, row := range []struct {
		id     string
		tenant int
		vec    []float32
	}{{"a", 1, []float32{1, 0}}, {"b", 2, []float32{0, 1}}, {"c", 2, []float32{5, 5}}} {
		_, err = pool.Exec(ctx, "INSERT INTO items VALUES ($1, $2, $3)", row.id, row.tenant, vector.From(row.vec))
		require.NoError(t, err)
	}
}

func testPostgresRegisterModule(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	require.NoError(t, vector.RegisterTypes(ctx, pool))
	app := fx.New(fx.NopLogger, fx.Supply(pool), vector.Module)
	require.NoError(t, app.Start(ctx))
	require.NoError(t, app.Stop(ctx))
}

func testPostgresSearches(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	// Query [1, 0.9]: cosine ranks c first, L2 ranks a first, inner product ranks c first.
	query := []float32{1, 0.9}
	search := func(t *testing.T, q vector.SearchQuery) []string {
		t.Helper()
		q.Table, q.IDColumn, q.VecColumn, q.Embedding = "items", "id", "embedding", query
		ids, err := vector.SimilaritySearch(ctx, pool, q)
		require.NoError(t, err)
		return ids
	}

	t.Run("positive metrics order neighbours", func(t *testing.T) {
		require.Equal(t, []string{"c", "a", "b"}, search(t, vector.SearchQuery{Metric: vector.Cosine, Limit: 3}))
		require.Equal(t, []string{"a", "b", "c"}, search(t, vector.SearchQuery{Metric: vector.L2, Limit: 3}))
		require.Equal(t, []string{"c", "a", "b"}, search(t, vector.SearchQuery{Metric: vector.InnerProduct, Limit: 3}))
	})

	t.Run("boundary defaults cosine and limit ten", func(t *testing.T) {
		require.Equal(t, []string{"c", "a", "b"}, search(t, vector.SearchQuery{}))
	})

	t.Run("boundary limit one", func(t *testing.T) {
		require.Equal(t, []string{"a"}, search(t, vector.SearchQuery{Metric: vector.L2, Limit: 1}))
	})

	t.Run("positive filter binds from third parameter", func(t *testing.T) {
		got := search(t, vector.SearchQuery{Metric: vector.L2, Limit: 3, Filter: "tenant = $3", FilterArgs: []any{2}})
		require.Equal(t, []string{"b", "c"}, got)
	})

	t.Run("negative missing table", func(t *testing.T) {
		_, err := vector.SimilaritySearch(ctx, pool, vector.SearchQuery{
			Table: "absent", IDColumn: "id", VecColumn: "embedding", Embedding: query,
		})
		require.ErrorContains(t, err, "vector: query")
	})

	t.Run("negative unscannable id", func(t *testing.T) {
		_, err := vector.SimilaritySearch(ctx, pool, vector.SearchQuery{
			Table: "items", IDColumn: "NULL::text", VecColumn: "embedding", Embedding: query,
		})
		require.ErrorContains(t, err, "vector: scan")
	})
}
