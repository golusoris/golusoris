// Package pgfts implements the [search.Searcher] interface against
// Postgres full-text search (tsvector + tsquery).
//
// This backend is deliberately minimal: apps define their own tables +
// tsvector columns (usually via a GENERATED ALWAYS column) and point
// this searcher at them. We don't create or migrate tables — that's
// the app's concern, owned by its own migrations/.
//
// Expected table shape (customizable via [Options]):
//
//	CREATE TABLE docs (
//	    id          text PRIMARY KEY,
//	    content     text NOT NULL,
//	    search_vec  tsvector GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
//	    -- plus whatever other columns your app needs; they're returned as Document fields
//	);
//	CREATE INDEX ON docs USING GIN (search_vec);
//
// Usage:
//
//	s := pgfts.New(pool, pgfts.Options{Table:"docs", VectorColumn:"search_vec"})
//	res, _ := s.Search(ctx, "docs", search.Query{Q: "postgres"})
package pgfts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/search"
)

// Options configures the pgfts Searcher.
type Options struct {
	// Table is the table name to query. When empty, the `collection`
	// argument passed to Search is used directly.
	Table string
	// VectorColumn is the tsvector column name. Default: "search_vec".
	VectorColumn string
	// Language is the text-search configuration. Default: "english".
	Language string
	// Columns is an optional explicit list of columns to SELECT. When
	// empty, `SELECT *` is used (the columns become the Document fields).
	Columns []string
	// RankColumn is the column name holding the ts_rank score in the
	// returned Document. Default: "_score". Set to empty to skip.
	RankColumn string
}

// Searcher implements [search.Searcher] against Postgres FTS. It does
// NOT implement [search.Indexer] — insertion and schema are the app's
// responsibility.
type Searcher struct {
	pool *pgxpool.Pool
	opts Options
}

// New returns a Searcher.
func New(pool *pgxpool.Pool, opts Options) *Searcher {
	if opts.VectorColumn == "" {
		opts.VectorColumn = "search_vec"
	}
	if opts.Language == "" {
		opts.Language = "english"
	}
	if opts.RankColumn == "" {
		opts.RankColumn = "_score"
	}
	return &Searcher{pool: pool, opts: opts}
}

// Search implements [search.Searcher]. Filters are ignored (use
// RawFilter for an AND-joined WHERE fragment).
func (s *Searcher) Search(ctx context.Context, collection string, q search.Query) (search.Results, error) {
	if s.pool == nil {
		return search.Results{}, errors.New("search/pgfts: nil pool")
	}
	table := s.opts.Table
	if table == "" {
		table = collection
	}
	sqlStmt, err := s.buildSQL(table, q)
	if err != nil {
		return search.Results{}, err
	}

	rows, err := s.pool.Query(ctx, sqlStmt, s.opts.Language, q.Q)
	if err != nil {
		return search.Results{}, fmt.Errorf("search/pgfts: query: %w", err)
	}
	defer rows.Close()

	hits, err := s.scanHits(rows)
	if err != nil {
		return search.Results{}, err
	}
	return search.Results{Hits: hits, Total: int64(len(hits))}, nil
}

// buildSQL assembles the parameterized SELECT. Identifiers (table,
// columns, vector column, rank alias) are validated up-front.
func (s *Searcher) buildSQL(table string, q search.Query) (string, error) {
	if !isSafeIdent(table) || !isSafeIdent(s.opts.VectorColumn) {
		return "", errors.New("search/pgfts: unsafe table or column name")
	}
	cols := "*"
	if len(s.opts.Columns) > 0 {
		for _, c := range s.opts.Columns {
			if !isSafeIdent(c) {
				return "", fmt.Errorf("search/pgfts: unsafe column %q", c)
			}
		}
		cols = strings.Join(s.opts.Columns, ", ")
	}
	sqlStmt := fmt.Sprintf(
		`SELECT %s, ts_rank(%s, plainto_tsquery($1::regconfig, $2)) AS %s
		   FROM %s
		  WHERE %s @@ plainto_tsquery($1::regconfig, $2)`,
		cols, s.opts.VectorColumn, s.opts.RankColumn, table, s.opts.VectorColumn,
	)
	if q.RawFilter != "" {
		sqlStmt += " AND (" + q.RawFilter + ")"
	}
	sqlStmt += " ORDER BY " + s.opts.RankColumn + " DESC"
	if q.Limit > 0 {
		sqlStmt += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		sqlStmt += fmt.Sprintf(" OFFSET %d", q.Offset)
	}
	return sqlStmt, nil
}

// scanHits iterates rows and produces Hits. Splits the rank column out
// into Hit.Score; everything else becomes Document fields.
func (s *Searcher) scanHits(rows pgx.Rows) ([]search.Hit, error) {
	fields := rows.FieldDescriptions()
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	var hits []search.Hit
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("search/pgfts: values: %w", err)
		}
		doc := make(search.Document, len(names))
		var score float64
		for i, n := range names {
			if n == s.opts.RankColumn {
				score = toFloat64(vals[i])
				continue
			}
			doc[n] = vals[i]
		}
		hits = append(hits, search.Hit{Document: doc, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search/pgfts: rows: %w", err)
	}
	return hits, nil
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	default:
		return 0
	}
}

// isSafeIdent allows letters, digits, underscores, and a single dot
// separator for schema-qualified names. No quoting is performed.
func isSafeIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_',
			r == '.':
		default:
			return false
		}
	}
	return true
}
