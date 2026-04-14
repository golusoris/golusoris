// Package outbox implements the transactional-outbox pattern.
//
// Apps write domain changes + outbox events in the same pg transaction;
// a leader-gated drainer polls the outbox and dispatches each event to
// a [jobs] worker. This guarantees no event is lost across a crash:
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
// Drainer wiring: include [Module] + run under a leader (leader/k8s or
// leader/pg). Only the leader dispatches, so the outbox is processed
// exactly-once across replicas.
//
// The outbox schema lives in outbox/migrations/ as a golang-migrate
// pair. Apps apply it via db/migrate (Step 2). Embed via [MigrationsFS]
// if you prefer bundling.
package outbox

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationsFS exposes the outbox migrations so apps can pass it to
// db/migrate via Options.WithFS.
//
//go:embed migrations
var MigrationsFS embed.FS

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
	_, err = tx.Exec(ctx,
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

// Pending returns up to limit un-dispatched events ordered by created_at.
// Apps rarely call this directly — the drainer does.
func Pending(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Event, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, kind, payload, created_at, dispatched_at, attempts, last_error
		   FROM golusoris_outbox
		  WHERE dispatched_at IS NULL
		  ORDER BY created_at
		  LIMIT $1`, limit)
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
	return out, rows.Err()
}

// MarkDispatched flags a successfully-dispatched event.
func MarkDispatched(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx,
		`UPDATE golusoris_outbox SET dispatched_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("outbox: mark dispatched: %w", err)
	}
	return nil
}

// MarkFailed records a dispatch attempt failure. Apps using river for
// dispatch get retries at the river layer; this field is diagnostic
// (visible on /status or future outbox-ui).
func MarkFailed(ctx context.Context, pool *pgxpool.Pool, id int64, err error) error {
	_, execErr := pool.Exec(ctx,
		`UPDATE golusoris_outbox
		    SET attempts = attempts + 1, last_error = $2
		  WHERE id = $1`, id, err.Error())
	if execErr != nil {
		return fmt.Errorf("outbox: mark failed: %w", execErr)
	}
	return nil
}
