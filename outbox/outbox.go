// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package outbox implements the transactional-outbox pattern.
//
// Apps write domain changes + outbox events in the same pg transaction;
// a drainer polls the outbox and dispatches each event to a [jobs] worker.
// This guarantees no event is lost across a crash:
// either the event is committed (and will be dispatched eventually) or
// the whole transaction rolled back.
//
// Usage:
//
//	// In a handler — write event in same tx as domain data.
//	sqlc.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
//	    if err := queries.WithTx(tx).CreateOrder(ctx, args); err != nil {
//	        return err
//	    }
//	    return outbox.Add(ctx, tx, "order.created", order)
//	})
//
// Drainer wiring: include [Module]. The drainer locks pending rows with
// FOR UPDATE SKIP LOCKED and inserts each River job in the same transaction
// that marks its outbox row dispatched. Multiple replicas may drain safely.
// River worker execution remains at-least-once, so workers stay idempotent.
//
// The outbox schema lives in outbox/migrations/ as a golang-migrate
// pair. Apps apply it via db/migrate. Embed via [MigrationsFS]
// if you prefer bundling.
package outbox

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationsFS exposes the outbox migrations so apps can pass it to
// db/migrate via Options.WithFS.
//
//go:embed migrations
var MigrationsFS embed.FS

const (
	// DefaultSchema is the schema used by the built-in outbox migration.
	DefaultSchema = "public"
	// DefaultTable is the table created by the built-in outbox migration.
	DefaultTable = "golusoris_outbox"
	// DefaultPendingLimit bounds a Pending call when its limit is zero.
	DefaultPendingLimit = 100
	// MaxPendingLimit is the largest batch returned by one Pending call.
	MaxPendingLimit = 1000
	// DefaultRetryDelay delays a failed event before it becomes pending again.
	DefaultRetryDelay = time.Second
	// MaxRetryDelay caps configured retry backoff.
	MaxRetryDelay = 24 * time.Hour
)

var (
	// ErrInvalidPendingLimit reports a negative Pending limit.
	ErrInvalidPendingLimit = errors.New("outbox: pending limit must not be negative")
	// ErrEventNotFound reports a mutation targeting no outbox row.
	ErrEventNotFound = errors.New("outbox: event not found")
	// ErrUnexpectedRowsAffected reports a mutation touching multiple outbox rows.
	ErrUnexpectedRowsAffected = errors.New("outbox: unexpected rows affected")
	// ErrNilDispatchError reports a MarkFailed call without its failure cause.
	ErrNilDispatchError = errors.New("outbox: dispatch error is required")
	// ErrInvalidRelationIdentifier reports an empty or NUL-bearing schema or table.
	ErrInvalidRelationIdentifier = errors.New("outbox: invalid relation identifier")
)

// Event is a row in the outbox table.
type Event struct {
	ID           int64           `json:"id"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	DispatchedAt *time.Time      `json:"dispatched_at,omitempty"`
	Attempts     int             `json:"attempts"`
	LastError    *string         `json:"last_error,omitempty"`
}

// Add writes an event to the outbox within an existing transaction. The
// payload can be any JSON-marshalable value; a []byte or
// json.RawMessage is used verbatim.
func Add(ctx context.Context, tx pgx.Tx, kind string, payload any) error {
	if kind == "" {
		return errors.New("outbox: kind required")
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		ctx,
		`INSERT INTO golusoris_outbox (kind, payload) VALUES ($1, $2)`,
		kind, raw,
	)
	if err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

func marshalPayload(payload any) (json.RawMessage, error) {
	switch v := payload.(type) {
	case json.RawMessage:
		return v, nil
	case []byte:
		return v, nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("outbox: marshal payload: %w", err)
	}
	return b, nil
}

// Pending returns due, un-dispatched events ordered by next attempt time and id.
// A zero limit uses [DefaultPendingLimit], a negative limit is rejected, and a
// limit above [MaxPendingLimit] is capped. Apps rarely call this directly.
func Pending(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Event, error) {
	return queryPending(ctx, pool, limit, false)
}

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryPending(ctx context.Context, queryer rowQuerier, limit int, lock bool) ([]Event, error) {
	limit, err := normalizePendingLimit(limit)
	if err != nil {
		return nil, err
	}
	lockClause := ""
	if lock {
		lockClause = " FOR UPDATE SKIP LOCKED"
	}
	rows, err := queryer.Query(ctx,
		`SELECT id, kind, payload, created_at, dispatched_at, attempts, last_error
		   FROM golusoris_outbox
		  WHERE dispatched_at IS NULL AND next_attempt_at <= now()
		  ORDER BY next_attempt_at, id
		  LIMIT $1`+lockClause, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: query pending: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.Payload, &ev.CreatedAt,
			&ev.DispatchedAt, &ev.Attempts, &ev.LastError); err != nil {
			return nil, fmt.Errorf("outbox: scan: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox: iterate pending: %w", err)
	}
	return out, nil
}

func normalizePendingLimit(limit int) (int, error) {
	if limit < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidPendingLimit, limit)
	}
	if limit == 0 {
		return DefaultPendingLimit, nil
	}
	return min(limit, MaxPendingLimit), nil
}

// MarkDispatched flags a successfully-dispatched event. It returns
// [ErrEventNotFound] when id does not identify an outbox row.
func MarkDispatched(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	return markDispatched(ctx, pool, id)
}

// Executor is the pgx execution subset used by outbox row mutations.
type Executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func markDispatched(ctx context.Context, executor Executor, id int64) error {
	return markDispatchedIn(ctx, executor, DefaultSchema, DefaultTable, id)
}

// MarkDispatchedIn flags a dispatched event in schema.table. Identifiers are
// validated and quoted before use. It returns [ErrEventNotFound] for a missing id.
func MarkDispatchedIn(
	ctx context.Context,
	executor Executor,
	schema string,
	table string,
	id int64,
) error {
	return markDispatchedIn(ctx, executor, schema, table, id)
}

func markDispatchedIn(
	ctx context.Context,
	executor Executor,
	schema string,
	table string,
	id int64,
) error {
	relation, err := sanitizeRelation(schema, table)
	if err != nil {
		return err
	}
	tag, err := executor.Exec(ctx,
		`UPDATE `+relation+` SET dispatched_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("outbox: mark dispatched: %w", err)
	}
	return requireOneAffectedRow(tag, id, "mark dispatched")
}

// ValidateRelation rejects relation identifiers that cannot be addressed by
// PostgreSQL. Valid identifiers may still require quoting, which row mutations
// apply before constructing SQL.
func ValidateRelation(schema string, table string) error {
	_, err := sanitizeRelation(schema, table)
	return err
}

func sanitizeRelation(schema string, table string) (string, error) {
	if schema == "" || strings.ContainsRune(schema, '\x00') {
		return "", fmt.Errorf("%w: schema %q", ErrInvalidRelationIdentifier, schema)
	}
	if table == "" || strings.ContainsRune(table, '\x00') {
		return "", fmt.Errorf("%w: table %q", ErrInvalidRelationIdentifier, table)
	}
	return pgx.Identifier{schema, table}.Sanitize(), nil
}

// MarkFailed records a dispatch attempt failure. It rejects a nil cause and
// returns [ErrEventNotFound] when id does not identify an outbox row. Apps
// using River for dispatch also get retries at the River layer. The outbox row
// becomes pending again after [DefaultRetryDelay].
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id int64, err error) error {
	return markFailed(ctx, pool, id, err, DefaultRetryDelay)
}

func markFailed(
	ctx context.Context,
	executor Executor,
	id int64,
	dispatchErr error,
	retryDelay time.Duration,
) error {
	if dispatchErr == nil {
		return ErrNilDispatchError
	}
	retryDelay = normalizeRetryDelay(retryDelay)
	delayMicros := max(int64(1), retryDelay.Microseconds())
	tag, execErr := executor.Exec(ctx,
		`UPDATE golusoris_outbox
		    SET attempts = attempts + 1,
		        last_error = $2,
		        next_attempt_at = now() + ($3::bigint * interval '1 microsecond')
		  WHERE id = $1`, id, dispatchErr.Error(), delayMicros)
	if execErr != nil {
		return fmt.Errorf("outbox: mark failed: %w", execErr)
	}
	return requireOneAffectedRow(tag, id, "mark failed")
}

func normalizeRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return DefaultRetryDelay
	}
	return min(delay, MaxRetryDelay)
}

func requireOneAffectedRow(tag pgconn.CommandTag, id int64, operation string) error {
	rows := tag.RowsAffected()
	switch rows {
	case 1:
		return nil
	case 0:
		return fmt.Errorf("outbox: %s event %d: %w", operation, id, ErrEventNotFound)
	default:
		return fmt.Errorf(
			"outbox: %s event %d: %w: got %d, want 1",
			operation,
			id,
			ErrUnexpectedRowsAffected,
			rows,
		)
	}
}
