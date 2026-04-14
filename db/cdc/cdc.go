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
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

const (
	defaultSlot       = "golusoris"
	defaultPublisher  = "golusoris"
	defaultStandbyHz  = 10
	outputPlugin      = "pgoutput"
)

// Op is the WAL operation type.
type Op string

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
var Module = fx.Module("golusoris.cdc",
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
				p.Logger.Info("cdc: no DSN configured, consumer disabled")
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
		c.logger.Error("cdc: connect", "err", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()

	sysident, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		c.logger.Error("cdc: identify system", "err", err)
		return
	}
	c.logger.Info("cdc: connected",
		"systemID", sysident.SystemID,
		"timeline", sysident.Timeline,
		"xlogpos", sysident.XLogPos,
	)

	if err := c.ensureSlot(ctx, conn, sysident.XLogPos); err != nil {
		c.logger.Error("cdc: ensure slot", "err", err)
		return
	}

	opts := pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			fmt.Sprintf("publication_names '%s'", c.cfg.Publication),
		},
	}
	if err := pglogrepl.StartReplication(ctx, conn, c.cfg.Slot, sysident.XLogPos, opts); err != nil {
		c.logger.Error("cdc: start replication", "err", err)
		return
	}

	relations := map[uint32]*pglogrepl.RelationMessage{}
	standbyInterval := time.Second / time.Duration(c.cfg.StandbyHz)
	nextStandby := c.clk.Now().Add(standbyInterval)
	clientXLogPos := sysident.XLogPos

	for {
		if c.clk.Now().After(nextStandby) {
			ssu := pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos}
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, ssu); err != nil {
				c.logger.Error("cdc: standby status update", "err", err)
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
			c.logger.Error("cdc: receive message", "err", err)
			return
		}

		if errMsg, ok := rawMsg.(*pgproto3.ErrorResponse); ok {
			c.logger.Error("cdc: postgres error", "msg", errMsg.Message, "code", errMsg.Code)
			return
		}

		msg, ok := rawMsg.(*pgproto3.CopyData)
		if !ok {
			continue
		}

		switch msg.Data[0] {
		case pglogrepl.PrimaryKeepaliveMessageByteID:
			pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(msg.Data[1:])
			if err != nil {
				c.logger.Warn("cdc: parse keepalive", "err", err)
				continue
			}
			if pkm.ReplyRequested {
				nextStandby = c.clk.Now() // force immediate standby status
			}

		case pglogrepl.XLogDataByteID:
			xld, err := pglogrepl.ParseXLogData(msg.Data[1:])
			if err != nil {
				c.logger.Warn("cdc: parse xlog", "err", err)
				continue
			}
			if err := c.dispatch(ctx, xld, relations, &clientXLogPos); err != nil {
				c.logger.Error("cdc: handler returned error", "err", err)
				return
			}
		}
	}
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
		c.logger.Warn("cdc: parse wal msg", "err", err)
		return nil
	}

	switch m := walMsg.(type) {
	case *pglogrepl.RelationMessage:
		relations[m.RelationID] = m

	case *pglogrepl.InsertMessage:
		rel, ok := relations[m.RelationID]
		if !ok {
			break
		}
		ev := Event{
			Schema: rel.Namespace,
			Table:  rel.RelationName,
			Op:     OpInsert,
			New:    tupleToMap(m.Tuple, rel),
			LSN:    xld.WALStart,
		}
		return c.handler(ctx, ev)

	case *pglogrepl.UpdateMessage:
		rel, ok := relations[m.RelationID]
		if !ok {
			break
		}
		ev := Event{
			Schema: rel.Namespace,
			Table:  rel.RelationName,
			Op:     OpUpdate,
			Old:    tupleToMap(m.OldTuple, rel),
			New:    tupleToMap(m.NewTuple, rel),
			LSN:    xld.WALStart,
		}
		return c.handler(ctx, ev)

	case *pglogrepl.DeleteMessage:
		rel, ok := relations[m.RelationID]
		if !ok {
			break
		}
		ev := Event{
			Schema: rel.Namespace,
			Table:  rel.RelationName,
			Op:     OpDelete,
			Old:    tupleToMap(m.OldTuple, rel),
			LSN:    xld.WALStart,
		}
		return c.handler(ctx, ev)

	case *pglogrepl.TruncateMessage:
		for _, relID := range m.RelationIDs {
			rel, ok := relations[relID]
			if !ok {
				continue
			}
			ev := Event{
				Schema: rel.Namespace,
				Table:  rel.RelationName,
				Op:     OpTruncate,
				LSN:    xld.WALStart,
			}
			if err := c.handler(ctx, ev); err != nil {
				return err
			}
		}

	case *pglogrepl.CommitMessage:
		// Advance confirmed LSN on commit.
		*clientXLogPos = m.CommitLSN
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
func (c *Consumer) ensureSlot(ctx context.Context, conn *pgconn.PgConn, startLSN pglogrepl.LSN) error {
	_, err := pglogrepl.CreateReplicationSlot(
		ctx, conn, c.cfg.Slot, outputPlugin,
		pglogrepl.CreateReplicationSlotOptions{Temporary: false},
	)
	if err != nil {
		// "SQLSTATE 42710" means the slot already exists — that's fine.
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "42710" {
			return nil
		}
		return fmt.Errorf("cdc: create slot: %w", err)
	}
	_ = startLSN
	c.logger.Info("cdc: created replication slot", "slot", c.cfg.Slot)
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
