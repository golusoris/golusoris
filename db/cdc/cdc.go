// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cdc implements a PostgreSQL logical-replication (WAL) consumer
// using the pglogrepl library.  It decodes pgoutput messages into structured
// [Event] values and delivers them to a caller-supplied [Handler].
//
// Postgres prerequisites:
//
//	ALTER SYSTEM SET wal_level = logical;
//	SELECT pg_create_logical_replication_slot('myslot', 'pgoutput');
//	CREATE PUBLICATION mypub FOR TABLE orders, users;
//
// Usage:
//
//	fx.New(
//	    cdc.Module,
//	    fx.Invoke(func(c *cdc.Consumer) {
//	        c.SetHandler(func(ctx context.Context, ev cdc.Event) error {
//	            slog.Info("wal", "op", ev.Op, "table", ev.Table, "new", ev.New)
//	            return nil
//	        })
//	    }),
//	)
//
// Config keys (env: APP_CDC_*):
//
//	cdc.dsn          # replication DSN (required; must include replication=database)
//	cdc.slot         # replication slot name (default: golusoris)
//	cdc.publication  # publication name (default: golusoris)
//	cdc.standby_hz   # standby status updates per second (default: 10)
package cdc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

const (
	defaultSlot      = "golusoris"
	defaultPublisher = "golusoris"
	defaultStandbyHz = 10
	outputPlugin     = "pgoutput"
)

// Op is the WAL operation type.
type Op string

// Op constants for WAL operation types.
const (
	OpInsert   Op = "INSERT"
	OpUpdate   Op = "UPDATE"
	OpDelete   Op = "DELETE"
	OpTruncate Op = "TRUNCATE"
)

// Event is a decoded WAL row-change event.
type Event struct {
	// Schema is the Postgres schema name (e.g. "public").
	Schema string
	// Table is the relation name (e.g. "orders").
	Table string
	// Op is INSERT, UPDATE, DELETE, or TRUNCATE.
	Op Op
	// Old contains the old column values (populated for UPDATE with REPLICA IDENTITY FULL, and DELETE).
	Old map[string]string
	// New contains the new column values (populated for INSERT and UPDATE).
	New map[string]string
	// LSN is the WAL log sequence number of the commit.
	LSN pglogrepl.LSN
	// CommitTime is the commit timestamp reported by Postgres.
	CommitTime time.Time
}

// Handler processes a single decoded WAL event.
// Returning a non-nil error stops the consumer.
type Handler func(ctx context.Context, ev Event) error

// noopHandler silently drops events — used when no handler is registered.
func noopHandler(_ context.Context, _ Event) error { return nil }

// Config holds logical-replication consumer configuration.
type Config struct {
	// DSN is the libpq connection string. Must include "replication=database".
	DSN string `koanf:"dsn"`
	// Slot is the replication slot name (default: "golusoris").
	Slot string `koanf:"slot"`
	// Publication is the Postgres PUBLICATION name (default: "golusoris").
	Publication string `koanf:"publication"`
	// StandbyHz controls how many standby-status updates per second are sent (default: 10).
	StandbyHz int `koanf:"standby_hz"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() Config {
	return Config{
		Slot:        defaultSlot,
		Publication: defaultPublisher,
		StandbyHz:   defaultStandbyHz,
	}
}

func (c Config) withDefaults() Config {
	if c.Slot == "" {
		c.Slot = defaultSlot
	}
	if c.Publication == "" {
		c.Publication = defaultPublisher
	}
	if c.StandbyHz <= 0 {
		c.StandbyHz = defaultStandbyHz
	}
	return c
}

// Consumer connects to Postgres over the logical-replication protocol,
// decodes pgoutput messages, and delivers [Event] values to a [Handler].
type Consumer struct {
	cfg     Config
	clk     clock.Clock
	logger  *slog.Logger
	handler Handler
}

// SetHandler replaces the event handler.  Must be called before fx Start.
func (c *Consumer) SetHandler(h Handler) { c.handler = h }

// Module provides *Consumer into the fx graph.
// Requires *config.Config, clock.Clock, and *slog.Logger.
var Module = fx.Module(
	"golusoris.cdc",
	fx.Provide(loadConfig),
	fx.Provide(newConsumer),
)

// params struct for newConsumer to accept named dependencies.
type params struct {
	fx.In
	LC     fx.Lifecycle
	Cfg    Config
	Clock  clock.Clock
	Logger *slog.Logger
}

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("cdc", &c); err != nil {
		return Config{}, fmt.Errorf("cdc: load config: %w", err)
	}
	return c.withDefaults(), nil
}

func newConsumer(p params) *Consumer {
	c := &Consumer{
		cfg:     p.Cfg,
		clk:     p.Clock,
		logger:  p.Logger,
		handler: noopHandler,
	}
	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if c.cfg.DSN == "" {
				p.Logger.InfoContext(ctx, "cdc: no DSN configured, consumer disabled")
				return nil
			}
			go c.run(ctx)
			return nil
		},
	})
	return c
}

// run is the consumer loop, executed in a goroutine.
func (c *Consumer) run(ctx context.Context) {
	conn, err := c.connect(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "cdc: connect", "err", err)
		return
	}
	defer func() {
		if cerr := conn.Close(ctx); cerr != nil {
			c.logger.WarnContext(ctx, "cdc: close replication conn", "err", cerr)
		}
	}()

	startLSN, ok := c.runSetup(ctx, conn)
	if !ok {
		return
	}

	c.runLoop(ctx, conn, startLSN)
}

// runSetup identifies the system, ensures the slot, and starts replication.
// Returns the starting LSN and true on success.
func (c *Consumer) runSetup(ctx context.Context, conn *pgconn.PgConn) (pglogrepl.LSN, bool) {
	sysident, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		c.logger.ErrorContext(ctx, "cdc: identify system", "err", err)
		return 0, false
	}
	c.logger.InfoContext(
		ctx, "cdc: connected",
		"system_id", sysident.SystemID,
		"timeline", sysident.Timeline,
		"xlogpos", sysident.XLogPos,
	)

	if err := c.ensureSlot(ctx, conn); err != nil {
		c.logger.ErrorContext(ctx, "cdc: ensure slot", "err", err)
		return 0, false
	}

	opts := pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", c.cfg.Publication),
		},
	}
	// Start LSN 0 resumes from the slot's confirmed_flush_lsn. Passing
	// sysident.XLogPos (the current WAL head) would skip every change retained
	// by a persistent slot since its last confirmed position — silent data loss
	// on restart.
	if err := pglogrepl.StartReplication(ctx, conn, c.cfg.Slot, 0, opts); err != nil {
		c.logger.ErrorContext(ctx, "cdc: start replication", "err", err)
		return 0, false
	}
	return sysident.XLogPos, true
}

// runLoop processes WAL messages until ctx is cancelled or a fatal error occurs.
func (c *Consumer) runLoop(ctx context.Context, conn *pgconn.PgConn, startLSN pglogrepl.LSN) {
	relations := map[uint32]*pglogrepl.RelationMessage{}
	standbyInterval := time.Second / time.Duration(c.cfg.StandbyHz)
	nextStandby := c.clk.Now().Add(standbyInterval)
	clientXLogPos := startLSN

	for ctx.Err() == nil {
		if c.clk.Now().After(nextStandby) {
			ssu := pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos}
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, ssu); err != nil {
				c.logger.ErrorContext(ctx, "cdc: standby status update", "err", err)
				return
			}
			nextStandby = c.clk.Now().Add(standbyInterval)
		}

		recvCtx, cancel := context.WithDeadline(ctx, c.clk.Now().Add(standbyInterval))
		rawMsg, err := conn.ReceiveMessage(recvCtx)
		cancel()
		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if ctx.Err() != nil {
				return // graceful shutdown
			}
			c.logger.ErrorContext(ctx, "cdc: receive message", "err", err)
			return
		}

		if stop := c.handleMessage(ctx, rawMsg, relations, &clientXLogPos, &nextStandby); stop {
			return
		}
	}
}

// handleMessage processes a single raw WAL message. Returns true if the loop should stop.
func (c *Consumer) handleMessage(
	ctx context.Context,
	rawMsg pgproto3.BackendMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	clientXLogPos *pglogrepl.LSN,
	nextStandby *time.Time,
) bool {
	if errMsg, ok := rawMsg.(*pgproto3.ErrorResponse); ok {
		c.logger.ErrorContext(ctx, "cdc: postgres error", "msg", errMsg.Message, "code", errMsg.Code)
		return true
	}

	msg, ok := rawMsg.(*pgproto3.CopyData)
	if !ok {
		return false
	}

	switch msg.Data[0] {
	case pglogrepl.PrimaryKeepaliveMessageByteID:
		pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
		if err != nil {
			c.logger.WarnContext(ctx, "cdc: parse keepalive", "err", err)
			return false
		}
		if pkm.ReplyRequested {
			*nextStandby = c.clk.Now() // force immediate standby status
		}

	case pglogrepl.XLogDataByteID:
		xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
		if err != nil {
			c.logger.WarnContext(ctx, "cdc: parse xlog", "err", err)
			return false
		}
		if err := c.dispatch(ctx, xld, relations, clientXLogPos); err != nil {
			c.logger.ErrorContext(ctx, "cdc: handler returned error", "err", err)
			return true
		}
	}
	return false
}

// dispatch decodes a single XLogData message and calls the handler for DML ops.
func (c *Consumer) dispatch(
	ctx context.Context,
	xld pglogrepl.XLogData,
	relations map[uint32]*pglogrepl.RelationMessage,
	clientXLogPos *pglogrepl.LSN,
) error {
	walMsg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		c.logger.WarnContext(ctx, "cdc: parse wal msg", "err", err)
		return nil
	}

	switch m := walMsg.(type) {
	case *pglogrepl.RelationMessage:
		relations[m.RelationID] = m
	case *pglogrepl.InsertMessage:
		return c.dispatchInsert(ctx, m, relations, xld.WALStart)
	case *pglogrepl.UpdateMessage:
		return c.dispatchUpdate(ctx, m, relations, xld.WALStart)
	case *pglogrepl.DeleteMessage:
		return c.dispatchDelete(ctx, m, relations, xld.WALStart)
	case *pglogrepl.TruncateMessage:
		return c.dispatchTruncate(ctx, m, relations, xld.WALStart)
	case *pglogrepl.CommitMessage:
		// Advance confirmed LSN on commit.
		*clientXLogPos = m.CommitLSN
	}
	return nil
}

// dispatchInsert builds an INSERT [Event] from m and delivers it to the
// handler. A message for an unknown relation (no prior RelationMessage seen)
// is silently dropped, matching dispatch's pre-extraction behavior.
func (c *Consumer) dispatchInsert(
	ctx context.Context,
	m *pglogrepl.InsertMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	rel, ok := relations[m.RelationID]
	if !ok {
		return nil
	}
	return c.handler(ctx, Event{
		Schema: rel.Namespace,
		Table:  rel.RelationName,
		Op:     OpInsert,
		New:    tupleToMap(m.Tuple, rel),
		LSN:    lsn,
	})
}

// dispatchUpdate builds an UPDATE [Event] from m and delivers it to the
// handler. A message for an unknown relation is silently dropped.
func (c *Consumer) dispatchUpdate(
	ctx context.Context,
	m *pglogrepl.UpdateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	rel, ok := relations[m.RelationID]
	if !ok {
		return nil
	}
	return c.handler(ctx, Event{
		Schema: rel.Namespace,
		Table:  rel.RelationName,
		Op:     OpUpdate,
		Old:    tupleToMap(m.OldTuple, rel),
		New:    tupleToMap(m.NewTuple, rel),
		LSN:    lsn,
	})
}

// dispatchDelete builds a DELETE [Event] from m and delivers it to the
// handler. A message for an unknown relation is silently dropped.
func (c *Consumer) dispatchDelete(
	ctx context.Context,
	m *pglogrepl.DeleteMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	rel, ok := relations[m.RelationID]
	if !ok {
		return nil
	}
	return c.handler(ctx, Event{
		Schema: rel.Namespace,
		Table:  rel.RelationName,
		Op:     OpDelete,
		Old:    tupleToMap(m.OldTuple, rel),
		LSN:    lsn,
	})
}

// dispatchTruncate delivers one TRUNCATE [Event] per relation ID in m that is
// known (has a prior RelationMessage); unknown IDs are skipped. It stops and
// returns on the first handler error, leaving any remaining relation IDs
// undelivered for this message.
func (c *Consumer) dispatchTruncate(
	ctx context.Context,
	m *pglogrepl.TruncateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	for _, relID := range m.RelationIDs {
		rel, ok := relations[relID]
		if !ok {
			continue
		}
		if err := c.handler(ctx, Event{
			Schema: rel.Namespace,
			Table:  rel.RelationName,
			Op:     OpTruncate,
			LSN:    lsn,
		}); err != nil {
			return err
		}
	}
	return nil
}

// connect opens a replication connection to Postgres.
func (c *Consumer) connect(ctx context.Context) (*pgconn.PgConn, error) {
	conn, err := pgconn.Connect(ctx, c.cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("cdc: pgconn.Connect: %w", err)
	}
	return conn, nil
}

// ensureSlot creates the replication slot if it does not already exist.
func (c *Consumer) ensureSlot(ctx context.Context, conn *pgconn.PgConn) error {
	_, err := pglogrepl.CreateReplicationSlot(
		ctx, conn, c.cfg.Slot, outputPlugin,
		pglogrepl.CreateReplicationSlotOptions{Temporary: false},
	)
	if err != nil {
		// "SQLSTATE 42710" means the slot already exists — that's fine.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42710" {
			return nil
		}
		return fmt.Errorf("cdc: create slot: %w", err)
	}
	c.logger.InfoContext(ctx, "cdc: created replication slot", "slot", c.cfg.Slot)
	return nil
}

// tupleToMap converts a TupleData to a string-keyed map using the relation column order.
// Returns nil when t is nil (e.g. OldTuple absent on non-FULL replica identity).
func tupleToMap(t *pglogrepl.TupleData, rel *pglogrepl.RelationMessage) map[string]string {
	if t == nil {
		return nil
	}
	out := make(map[string]string, len(t.Columns))
	for i, col := range t.Columns {
		if i >= len(rel.Columns) {
			break
		}
		name := rel.Columns[i].Name
		switch col.DataType {
		case pglogrepl.TupleDataTypeNull:
			out[name] = ""
		case pglogrepl.TupleDataTypeText:
			out[name] = string(col.Data)
		default:
			// 'u' (unchanged TOAST) — value not sent; omit from map
		}
	}
	return out
}
