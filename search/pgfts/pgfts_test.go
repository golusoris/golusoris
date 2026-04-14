package pgfts_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/search"
	"github.com/golusoris/golusoris/search/pgfts"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func TestSearch_RealPostgres(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)

	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE docs (
			id text PRIMARY KEY,
			title text NOT NULL,
			body  text NOT NULL,
			search_vec tsvector GENERATED ALWAYS AS
				(setweight(to_tsvector('english', title), 'A') ||
				 setweight(to_tsvector('english', body),  'B')) STORED
		);
		CREATE INDEX ON docs USING GIN (search_vec);
	`)
	require.NoError(t, err)

	// Insert a few rows.
	rows := []struct{ id, title, body string }{
		{"1", "Introducing Postgres", "A powerful relational database."},
		{"2", "Go programming", "Fast, typed, concurrent systems language."},
		{"3", "Full text search", "tsvector and tsquery make Postgres full-text search fast."},
	}
	for _, r := range rows {
		_, err = pool.Exec(ctx, `INSERT INTO docs (id,title,body) VALUES ($1,$2,$3)`, r.id, r.title, r.body)
		require.NoError(t, err, pgx.ErrNoRows)
	}

	s := pgfts.New(pool, pgfts.Options{Table: "docs"})

	res, err := s.Search(ctx, "docs", search.Query{Q: "postgres"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Hits)
	// Doc 1 ("Introducing Postgres") must appear and outrank doc 3 (weighted A).
	require.Equal(t, "1", res.Hits[0].Document["id"])
	require.Greater(t, res.Hits[0].Score, 0.0)
}

func TestSearch_LimitOffset(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE tiny (
			id text PRIMARY KEY,
			content text,
			search_vec tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED
		)`)
	require.NoError(t, err)
	for i, word := range []string{"alpha", "alpha", "alpha", "alpha"} {
		_, err = pool.Exec(ctx, `INSERT INTO tiny VALUES ($1,$2)`, []byte{byte('1' + i)}, word)
		require.NoError(t, err)
	}

	s := pgfts.New(pool, pgfts.Options{Table: "tiny"})
	res, err := s.Search(ctx, "tiny", search.Query{Q: "alpha", Limit: 2, Offset: 1})
	require.NoError(t, err)
	require.Len(t, res.Hits, 2)
}

func TestSearch_RejectsUnsafeIdent(t *testing.T) {
	t.Parallel()
	s := pgfts.New(nil, pgfts.Options{Table: "bad; DROP TABLE x;--"})
	_, err := s.Search(context.Background(), "", search.Query{Q: "x"})
	require.Error(t, err)
}
