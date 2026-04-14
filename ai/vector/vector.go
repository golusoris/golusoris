// Package vector provides helpers for storing and querying vector embeddings
// using pgvector (the PostgreSQL vector extension). Wraps pgvector-go types
// for use with pgx/v5.
//
// Usage:
//
//	// Store an embedding:
//	_, err := pool.Exec(ctx,
//	    "INSERT INTO items (id, embedding) VALUES ($1, $2)",
//	    id, vector.From(embedding))
//
//	// Cosine similarity search:
//	ids, err := vector.SimilaritySearch(ctx, pool, vector.SearchQuery{
//	    Table:     "items",
//	    IDColumn:  "id",
//	    VecColumn: "embedding",
//	    Embedding: query,
//	    Limit:     10,
//	    Metric:    vector.Cosine,
//	})
package vector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	pgvec "github.com/pgvector/pgvector-go"
	pgvecpgx "github.com/pgvector/pgvector-go/pgx"
)

// Vector is an alias for the pgvector type. Use it in sqlc-generated
// structs and pgx query parameters.
type Vector = pgvec.Vector

// From converts a float32 slice to a [Vector] suitable for use in pgx queries.
func From(embedding []float32) Vector {
	return pgvec.NewVector(embedding)
}

// Metric is the distance metric for similarity search.
type Metric string

const (
	// Cosine computes 1 - cosine_similarity. Lower = more similar.
	Cosine Metric = "<=>"
	// L2 computes Euclidean distance. Lower = more similar.
	L2 Metric = "<->"
	// InnerProduct computes negative inner product. Lower = more similar.
	InnerProduct Metric = "<#>"
)

// SearchQuery parameters for a nearest-neighbour search.
type SearchQuery struct {
	// Table is the Postgres table name.
	Table string
	// IDColumn is the primary key column returned in results.
	IDColumn string
	// VecColumn is the column storing the vector embeddings.
	VecColumn string
	// Embedding is the query vector.
	Embedding []float32
	// Limit is the maximum number of neighbours to return.
	Limit int
	// Metric controls the distance function. Default: Cosine.
	Metric Metric
	// Filter is an optional SQL WHERE clause appended to the query.
	// Example: "tenant_id = $2" (use positional parameters starting at $2).
	Filter string
	// FilterArgs are the arguments for Filter placeholders.
	FilterArgs []any
}

// SimilaritySearch returns the IDs of the nearest neighbours in order of
// increasing distance (most similar first).
func SimilaritySearch(ctx context.Context, pool *pgxpool.Pool, q SearchQuery) ([]string, error) {
	if q.Metric == "" {
		q.Metric = Cosine
	}
	if q.Limit == 0 {
		q.Limit = 10
	}

	where := ""
	args := []any{From(q.Embedding), q.Limit}
	if q.Filter != "" {
		where = " WHERE " + q.Filter
		args = append(args, q.FilterArgs...)
	}

	sql := fmt.Sprintf(
		"SELECT %s FROM %s%s ORDER BY %s %s $1 LIMIT $2",
		q.IDColumn, q.Table, where, q.VecColumn, string(q.Metric),
	)

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("vector: query: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("vector: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RegisterTypes registers pgvector's custom OID so pgx can encode/decode
// Vector columns automatically. Call once after acquiring a connection or
// using pgxpool.AfterConnect.
func RegisterTypes(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("vector: acquire conn: %w", err)
	}
	defer conn.Release()
	if err := pgvecpgx.RegisterTypes(ctx, conn.Conn()); err != nil {
		return fmt.Errorf("vector: register types: %w", err)
	}
	return nil
}
