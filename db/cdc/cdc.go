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
//	cdc.reconnect_delay # delay before retrying a failed session (default: 1s)
package cdc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
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
	maxStandbyHz     = 1000
	defaultReconnect = time.Second
	connectTimeout   = 10 * time.Second
	closeTimeout     = 5 * time.Second
	outputPlugin     = "pgoutput"
	maxIdentifierLen = 63
)

var (
	// ErrNoHandler prevents a configured consumer from acknowledging WAL when
	// no event handler has been installed.
	ErrNoHandler = errors.New("cdc: handler must be set before start")
	// ErrUnknownRelation prevents acknowledging a transaction whose relation
	// metadata was absent from the pgoutput stream or local cache.
	ErrUnknownRelation = errors.New("cdc: relation metadata is missing")
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
	// Old is the lossy legacy text projection of OldValues. New code uses OldValues
	// to distinguish NULL, binary, and unchanged values.
	Old map[string]string
	// New is the lossy legacy text projection of NewValues. New code uses NewValues
	// to distinguish NULL, binary, and unchanged values.
	New map[string]string
	// LSN is the WAL log sequence number of the commit.
	LSN pglogrepl.LSN
	// CommitTime is the commit timestamp reported by Postgres.
	CommitTime time.Time
	// OldValues preserves the data type and null/unchanged state for old columns.
	OldValues map[string]ColumnValue
	// NewValues preserves the data type and null/unchanged state for new columns.
	NewValues map[string]ColumnValue
}

// ColumnValueKind identifies one pgoutput tuple value representation.
type ColumnValueKind string

// ColumnValueKind constants preserve every pgoutput tuple state.
const (
	ColumnValueNull      ColumnValueKind = "null"
	ColumnValueUnchanged ColumnValueKind = "unchanged"
	ColumnValueText      ColumnValueKind = "text"
	ColumnValueBinary    ColumnValueKind = "binary"
)

// ColumnValue preserves a pgoutput column's representation and bytes.
type ColumnValue struct {
	Kind ColumnValueKind
	Data []byte
}

// Text returns the text value and whether the column uses text representation.
func (v ColumnValue) Text() (string, bool) {
	if v.Kind != ColumnValueText {
		return "", false
	}
	return string(v.Data), true
}

// Handler processes a single decoded WAL event.
// Returning a non-nil error leaves the transaction unacknowledged and restarts
// the replication session, so handlers must tolerate at-least-once delivery.
type Handler func(ctx context.Context, ev Event) error

// noopHandler is used by package integration fixtures; production consumers
// start without a handler and fail closed before replication.
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
	// ReconnectDelay bounds retry frequency after connection or stream failure.
	ReconnectDelay time.Duration `koanf:"reconnect_delay"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() Config {
	return Config{
		Slot:           defaultSlot,
		Publication:    defaultPublisher,
		StandbyHz:      defaultStandbyHz,
		ReconnectDelay: defaultReconnect,
	}
}

func (c Config) withDefaults() Config {
	if c.Slot == "" {
		c.Slot = defaultSlot
	}
	if c.Publication == "" {
		c.Publication = defaultPublisher
	}
	if c.StandbyHz == 0 {
		c.StandbyHz = defaultStandbyHz
	}
	if c.ReconnectDelay == 0 {
		c.ReconnectDelay = defaultReconnect
	}
	return c
}

func (c Config) standbyInterval() (time.Duration, error) {
	if c.StandbyHz <= 0 || c.StandbyHz > maxStandbyHz {
		return 0, fmt.Errorf(
			"cdc: standby_hz must be between 1 and %d, got %d",
			maxStandbyHz,
			c.StandbyHz,
		)
	}
	return time.Second / time.Duration(c.StandbyHz), nil
}

func (c Config) validate() error {
	if err := validateSlotName(c.Slot); err != nil {
		return err
	}
	if err := validatePublicationName(c.Publication); err != nil {
		return err
	}
	if c.ReconnectDelay < 0 {
		return errors.New("cdc: reconnect_delay must not be negative")
	}
	_, err := c.standbyInterval()
	return err
}

func validateSlotName(name string) error {
	if len(name) == 0 || len(name) > maxIdentifierLen {
		return fmt.Errorf("cdc: slot must contain 1-%d bytes", maxIdentifierLen)
	}
	for i := range len(name) {
		char := name[i]
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return fmt.Errorf("cdc: slot contains invalid character %q", char)
		}
	}
	return nil
}

func validatePublicationName(name string) error {
	if len(name) == 0 || len(name) > maxIdentifierLen {
		return fmt.Errorf("cdc: publication must contain 1-%d bytes", maxIdentifierLen)
	}
	if !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return errors.New("cdc: publication must be valid UTF-8 without NUL")
	}
	return nil
}

func publicationPluginArg(name string) string {
	identifier := pgx.Identifier{name}.Sanitize()
	literal := strings.ReplaceAll(identifier, "'", "''")
	return "publication_names '" + literal + "'"
}

// Consumer connects to Postgres over the logical-replication protocol,
// decodes pgoutput messages, and delivers [Event] values to a [Handler].
type Consumer struct {
	cfg        Config
	clk        clock.Clock
	logger     *slog.Logger
	handler    Handler
	runSession func(context.Context) error
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
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func newConsumer(p params) *Consumer {
	c := &Consumer{
		cfg:    p.Cfg.withDefaults(),
		clk:    p.Clock,
		logger: p.Logger,
	}
	c.runSession = c.runOnce
	var cancel context.CancelFunc
	var done chan struct{}
	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if c.cfg.DSN == "" {
				p.Logger.InfoContext(ctx, "cdc: no DSN configured, consumer disabled")
				return nil
			}
			if c.handler == nil {
				return ErrNoHandler
			}
			if err := c.cfg.validate(); err != nil {
				return err
			}
			runBase := context.WithoutCancel(ctx)
			runCtx, runCancel := context.WithCancel(runBase)
			cancel = runCancel
			done = make(chan struct{})
			go func() {
				defer close(done)
				defer runCancel()
				c.run(runCtx)
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return fmt.Errorf("cdc: stop consumer: %w", ctx.Err())
			}
		},
	})
	return c
}

// run reconnects failed replication sessions until shutdown.
func (c *Consumer) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := c.runSession(ctx)
		if ctx.Err() != nil {
			return
		}
		c.logger.WarnContext(
			ctx, "cdc: replication session ended; retrying",
			"err", err,
			"retry_in", c.cfg.ReconnectDelay,
		)
		select {
		case <-ctx.Done():
			return
		case <-c.clk.After(c.cfg.ReconnectDelay):
		}
	}
}

func (c *Consumer) runOnce(ctx context.Context) error {
	connectCtx, cancelConnect := context.WithTimeout(ctx, connectTimeout)
	conn, err := c.connect(connectCtx)
	cancelConnect()
	if err != nil {
		return err
	}
	defer func() {
		closeBase := context.WithoutCancel(ctx)
		closeCtx, cancelClose := context.WithTimeout(closeBase, closeTimeout)
		defer cancelClose()
		if cerr := conn.Close(closeCtx); cerr != nil {
			c.logger.WarnContext(ctx, "cdc: close replication conn", "err", cerr)
		}
	}()

	setupCtx, cancelSetup := context.WithTimeout(ctx, connectTimeout)
	err = c.runSetup(setupCtx, conn)
	cancelSetup()
	if err != nil {
		return err
	}

	return c.runLoop(ctx, conn, 0)
}

// runSetup identifies the system, ensures the slot, and starts replication.
// The initial acknowledged LSN stays zero until a retained commit is handled;
// acknowledging IdentifySystem's current head could drop backlog.
func (c *Consumer) runSetup(ctx context.Context, conn *pgconn.PgConn) error {
	if err := c.cfg.validate(); err != nil {
		return err
	}
	sysident, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		return fmt.Errorf("cdc: identify system: %w", err)
	}
	c.logger.InfoContext(
		ctx, "cdc: connected",
		"system_id", sysident.SystemID,
		"timeline", sysident.Timeline,
		"xlogpos", sysident.XLogPos,
	)

	if err := c.ensureSlot(ctx, conn); err != nil {
		return fmt.Errorf("cdc: ensure slot: %w", err)
	}

	opts := pglogrepl.StartReplicationOptions{
		PluginArgs: []string{
			"proto_version '1'",
			publicationPluginArg(c.cfg.Publication),
		},
	}
	// Start LSN 0 resumes from the slot's confirmed_flush_lsn. Passing
	// sysident.XLogPos (the current WAL head) would skip every change retained
	// by a persistent slot since its last confirmed position — silent data loss
	// on restart.
	if err := pglogrepl.StartReplication(ctx, conn, c.cfg.Slot, 0, opts); err != nil {
		return fmt.Errorf("cdc: start replication: %w", err)
	}
	return nil
}

// runLoop processes WAL messages until ctx is cancelled or a fatal error occurs.
func (c *Consumer) runLoop(ctx context.Context, conn *pgconn.PgConn, startLSN pglogrepl.LSN) error {
	relations := map[uint32]*pglogrepl.RelationMessage{}
	transaction := transactionState{}
	standbyInterval, err := c.cfg.standbyInterval()
	if err != nil {
		return err
	}
	nextStandby := c.clk.Now().Add(standbyInterval)
	clientXLogPos := startLSN

	for ctx.Err() == nil {
		if c.maybeSendStandby(ctx, conn, clientXLogPos, &nextStandby, standbyInterval) {
			return errors.New("cdc: standby status update failed")
		}

		rawMsg, timedOut, stop := c.receiveNext(ctx, conn, standbyInterval)
		if stop {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("cdc: receive loop stopped")
		}
		if timedOut {
			continue
		}

		if c.handleMessage(ctx, rawMsg, relations, &transaction, &clientXLogPos, &nextStandby) {
			return errors.New("cdc: message handler stopped")
		}
	}
	return ctx.Err()
}

// maybeSendStandby sends a standby status update reporting clientXLogPos when
// nextStandby has elapsed, advancing nextStandby on success. Returns true if
// the loop should stop because the send failed.
func (c *Consumer) maybeSendStandby(
	ctx context.Context,
	conn *pgconn.PgConn,
	clientXLogPos pglogrepl.LSN,
	nextStandby *time.Time,
	interval time.Duration,
) bool {
	if !c.clk.Now().After(*nextStandby) {
		return false
	}
	ssu := pglogrepl.StandbyStatusUpdate{WALWritePosition: clientXLogPos}
	err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, ssu)
	return c.afterStandbySend(ctx, err, nextStandby, interval)
}

// afterStandbySend interprets the result of a standby status update send: on
// error it logs and reports the loop should stop; on success it re-arms
// nextStandby for the next interval.
func (c *Consumer) afterStandbySend(ctx context.Context, err error, nextStandby *time.Time, interval time.Duration) bool {
	if err != nil {
		c.logger.ErrorContext(ctx, "cdc: standby status update", "err", err)
		return true
	}
	*nextStandby = c.clk.Now().Add(interval)
	return false
}

// receiveNext reads one raw WAL message bounded by a deadline of timeout
// from now. timedOut reports a deadline expiry (caller should keep looping);
// stop reports a fatal or shutdown condition (caller should return).
func (c *Consumer) receiveNext(
	ctx context.Context,
	conn *pgconn.PgConn,
	timeout time.Duration,
) (rawMsg pgproto3.BackendMessage, timedOut, stop bool) {
	recvCtx, cancel := receiveContext(ctx, timeout)
	rawMsg, err := conn.ReceiveMessage(recvCtx)
	cancel()
	return c.classifyReceive(ctx, rawMsg, err)
}

func receiveContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

// classifyReceive interprets the outcome of a ReceiveMessage call: a nil err
// passes rawMsg through; a deadline-timeout error asks the caller to retry;
// any other error asks the caller to stop — silently on graceful shutdown
// (ctx already cancelled), logged otherwise.
func (c *Consumer) classifyReceive(
	ctx context.Context,
	rawMsg pgproto3.BackendMessage,
	err error,
) (pgproto3.BackendMessage, bool, bool) {
	if err == nil {
		return rawMsg, false, false
	}
	if pgconn.Timeout(err) {
		return nil, true, false
	}
	if ctx.Err() != nil {
		return nil, false, true // graceful shutdown
	}
	c.logger.ErrorContext(ctx, "cdc: receive message", "err", err)
	return nil, false, true
}

// handleMessage processes a single raw WAL message. Returns true if the loop should stop.
func (c *Consumer) handleMessage(
	ctx context.Context,
	rawMsg pgproto3.BackendMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	transaction *transactionState,
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
	if len(msg.Data) == 0 {
		c.logger.ErrorContext(ctx, "cdc: empty replication message")
		return true
	}

	switch msg.Data[0] {
	case pglogrepl.PrimaryKeepaliveMessageByteID:
		return c.handleKeepalive(ctx, msg.Data[1:], transaction, clientXLogPos, nextStandby)
	case pglogrepl.XLogDataByteID:
		return c.handleXLogData(ctx, msg.Data[1:], relations, transaction, clientXLogPos)
	}
	return false
}

func (c *Consumer) handleKeepalive(
	ctx context.Context,
	data []byte,
	transaction *transactionState,
	clientXLogPos *pglogrepl.LSN,
	nextStandby *time.Time,
) bool {
	pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(data)
	if err != nil {
		c.logger.WarnContext(ctx, "cdc: parse keepalive", "err", err)
		return true
	}
	if !transaction.active && pkm.ServerWALEnd > *clientXLogPos {
		// Keep open transactions pinned until commit and every handler succeeds.
		*clientXLogPos = pkm.ServerWALEnd
	}
	if pkm.ReplyRequested {
		*nextStandby = c.clk.Now() // force immediate standby status
	}
	return false
}

func (c *Consumer) handleXLogData(
	ctx context.Context,
	data []byte,
	relations map[uint32]*pglogrepl.RelationMessage,
	transaction *transactionState,
	clientXLogPos *pglogrepl.LSN,
) bool {
	xld, err := pglogrepl.ParseXLogData(data)
	if err != nil {
		c.logger.WarnContext(ctx, "cdc: parse xlog", "err", err)
		return true
	}
	if err := c.dispatch(ctx, xld, relations, transaction, clientXLogPos); err != nil {
		c.logger.ErrorContext(ctx, "cdc: handler returned error", "err", err)
		return true
	}
	return false
}

// dispatch decodes a single XLogData message and calls the handler for DML ops.
func (c *Consumer) dispatch(
	ctx context.Context,
	xld pglogrepl.XLogData,
	relations map[uint32]*pglogrepl.RelationMessage,
	transaction *transactionState,
	clientXLogPos *pglogrepl.LSN,
) error {
	walMsg, err := pglogrepl.Parse(xld.WALData)
	if err != nil {
		return fmt.Errorf("cdc: parse wal message: %w", err)
	}

	switch m := walMsg.(type) {
	case *pglogrepl.BeginMessage:
		return transaction.begin(m)
	case *pglogrepl.RelationMessage:
		relations[m.RelationID] = m
	case *pglogrepl.InsertMessage:
		ev, eventErr := insertEvent(m, relations, xld.WALStart)
		return c.deliverDecoded(ctx, transaction, ev, eventErr)
	case *pglogrepl.UpdateMessage:
		ev, eventErr := updateEvent(m, relations, xld.WALStart)
		return c.deliverDecoded(ctx, transaction, ev, eventErr)
	case *pglogrepl.DeleteMessage:
		ev, eventErr := deleteEvent(m, relations, xld.WALStart)
		return c.deliverDecoded(ctx, transaction, ev, eventErr)
	case *pglogrepl.TruncateMessage:
		return c.deliverTruncate(ctx, transaction, m, relations, xld.WALStart)
	case *pglogrepl.CommitMessage:
		return transaction.commit(m, clientXLogPos)
	}
	return nil
}

func (c *Consumer) deliverDecoded(
	ctx context.Context,
	transaction *transactionState,
	event Event,
	decodeErr error,
) error {
	if decodeErr != nil {
		return decodeErr
	}
	return c.deliver(ctx, transaction, event)
}

func (c *Consumer) deliverTruncate(
	ctx context.Context,
	transaction *transactionState,
	message *pglogrepl.TruncateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	for _, relationID := range message.RelationIDs {
		relation, ok := relations[relationID]
		if !ok {
			return unknownRelationError(relationID)
		}
		event := Event{
			Schema: relation.Namespace,
			Table:  relation.RelationName,
			Op:     OpTruncate,
			LSN:    lsn,
		}
		if err := c.deliver(ctx, transaction, event); err != nil {
			return err
		}
	}
	return nil
}

func unknownRelationError(relationID uint32) error {
	return fmt.Errorf("%w: relation_id=%d", ErrUnknownRelation, relationID)
}

type transactionState struct {
	commitTime time.Time
	finalLSN   pglogrepl.LSN
	active     bool
}

func (s *transactionState) begin(message *pglogrepl.BeginMessage) error {
	if s.active {
		return errors.New("cdc: nested begin message")
	}
	s.commitTime = message.CommitTime
	s.finalLSN = message.FinalLSN
	s.active = true
	return nil
}

func (s *transactionState) commit(
	message *pglogrepl.CommitMessage,
	clientXLogPos *pglogrepl.LSN,
) error {
	if !s.active {
		return errors.New("cdc: commit without begin message")
	}
	*clientXLogPos = message.TransactionEndLSN
	*s = transactionState{}
	return nil
}

func (c *Consumer) deliver(ctx context.Context, transaction *transactionState, event Event) error {
	if !transaction.active {
		return errors.New("cdc: row change outside transaction")
	}
	event.LSN = transaction.finalLSN
	event.CommitTime = transaction.commitTime
	return c.handler(ctx, event)
}

// dispatchInsert builds an INSERT [Event] from m and delivers it to the
// handler. Missing relation metadata fails the session before acknowledgement.
func (c *Consumer) dispatchInsert(
	ctx context.Context,
	m *pglogrepl.InsertMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	event, err := insertEvent(m, relations, lsn)
	if err != nil {
		return err
	}
	return c.handler(ctx, event)
}

func insertEvent(
	m *pglogrepl.InsertMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) (Event, error) {
	rel, ok := relations[m.RelationID]
	if !ok {
		return Event{}, unknownRelationError(m.RelationID)
	}
	values, err := tupleToValues(m.Tuple, rel, false)
	if err != nil {
		return Event{}, fmt.Errorf("cdc: decode insert tuple: %w", err)
	}
	return Event{
		Schema:    rel.Namespace,
		Table:     rel.RelationName,
		Op:        OpInsert,
		New:       legacyTextValues(values),
		LSN:       lsn,
		NewValues: values,
	}, nil
}

// dispatchUpdate builds an UPDATE [Event] from m and delivers it to the
// handler. Missing relation metadata fails the session before acknowledgement.
func (c *Consumer) dispatchUpdate(
	ctx context.Context,
	m *pglogrepl.UpdateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	event, err := updateEvent(m, relations, lsn)
	if err != nil {
		return err
	}
	return c.handler(ctx, event)
}

func updateEvent(
	m *pglogrepl.UpdateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) (Event, error) {
	rel, ok := relations[m.RelationID]
	if !ok {
		return Event{}, unknownRelationError(m.RelationID)
	}
	oldValues, err := tupleToValues(
		m.OldTuple,
		rel,
		m.OldTupleType == pglogrepl.UpdateMessageTupleTypeKey,
	)
	if err != nil {
		return Event{}, fmt.Errorf("cdc: decode update old tuple: %w", err)
	}
	newValues, err := tupleToValues(m.NewTuple, rel, false)
	if err != nil {
		return Event{}, fmt.Errorf("cdc: decode update new tuple: %w", err)
	}
	return Event{
		Schema:    rel.Namespace,
		Table:     rel.RelationName,
		Op:        OpUpdate,
		Old:       legacyTextValues(oldValues),
		New:       legacyTextValues(newValues),
		LSN:       lsn,
		OldValues: oldValues,
		NewValues: newValues,
	}, nil
}

// dispatchDelete builds a DELETE [Event] from m and delivers it to the
// handler. Missing relation metadata fails the session before acknowledgement.
func (c *Consumer) dispatchDelete(
	ctx context.Context,
	m *pglogrepl.DeleteMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	event, err := deleteEvent(m, relations, lsn)
	if err != nil {
		return err
	}
	return c.handler(ctx, event)
}

func deleteEvent(
	m *pglogrepl.DeleteMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) (Event, error) {
	rel, ok := relations[m.RelationID]
	if !ok {
		return Event{}, unknownRelationError(m.RelationID)
	}
	oldValues, err := tupleToValues(
		m.OldTuple,
		rel,
		m.OldTupleType == pglogrepl.DeleteMessageTupleTypeKey,
	)
	if err != nil {
		return Event{}, fmt.Errorf("cdc: decode delete tuple: %w", err)
	}
	return Event{
		Schema:    rel.Namespace,
		Table:     rel.RelationName,
		Op:        OpDelete,
		Old:       legacyTextValues(oldValues),
		LSN:       lsn,
		OldValues: oldValues,
	}, nil
}

// dispatchTruncate delivers one TRUNCATE [Event] per relation ID in m. Missing
// relation metadata or a handler failure ends the session without ack.
func (c *Consumer) dispatchTruncate(
	ctx context.Context,
	m *pglogrepl.TruncateMessage,
	relations map[uint32]*pglogrepl.RelationMessage,
	lsn pglogrepl.LSN,
) error {
	for _, relID := range m.RelationIDs {
		rel, ok := relations[relID]
		if !ok {
			return unknownRelationError(relID)
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
	if err := validateSlotName(c.cfg.Slot); err != nil {
		return err
	}
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

// tupleToValues preserves all tuple states and maps key tuples only to key columns.
func tupleToValues(
	t *pglogrepl.TupleData,
	rel *pglogrepl.RelationMessage,
	keyOnly bool,
) (map[string]ColumnValue, error) {
	if t == nil {
		return nil, nil //nolint:nilnil // nil map distinguishes an absent tuple from a present empty tuple.
	}
	expected := len(rel.Columns)
	if keyOnly {
		expected = 0
		for _, column := range rel.Columns {
			if column.Flags&1 != 0 {
				expected++
			}
		}
	}
	if len(t.Columns) != expected {
		return nil, fmt.Errorf("tuple has %d columns; relation expects %d", len(t.Columns), expected)
	}
	out := make(map[string]ColumnValue, expected)
	relationIndex := 0
	for _, tupleColumn := range t.Columns {
		for keyOnly && rel.Columns[relationIndex].Flags&1 == 0 {
			relationIndex++
		}
		value, err := decodeColumnValue(tupleColumn)
		if err != nil {
			return nil, err
		}
		out[rel.Columns[relationIndex].Name] = value
		relationIndex++
	}
	return out, nil
}

func decodeColumnValue(column *pglogrepl.TupleDataColumn) (ColumnValue, error) {
	switch column.DataType {
	case pglogrepl.TupleDataTypeNull:
		return ColumnValue{Kind: ColumnValueNull}, nil
	case pglogrepl.TupleDataTypeToast:
		return ColumnValue{Kind: ColumnValueUnchanged}, nil
	case pglogrepl.TupleDataTypeText:
		return ColumnValue{Kind: ColumnValueText, Data: append([]byte(nil), column.Data...)}, nil
	case pglogrepl.TupleDataTypeBinary:
		return ColumnValue{Kind: ColumnValueBinary, Data: append([]byte(nil), column.Data...)}, nil
	default:
		return ColumnValue{}, fmt.Errorf("unknown tuple data type %q", column.DataType)
	}
}

func legacyTextValues(values map[string]ColumnValue) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, value := range values {
		switch value.Kind {
		case ColumnValueNull:
			out[name] = ""
		case ColumnValueText:
			out[name] = string(value.Data)
		case ColumnValueUnchanged, ColumnValueBinary:
		}
	}
	return out
}
