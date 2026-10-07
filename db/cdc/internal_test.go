// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgproto3"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

// knownRelation is a minimal "public" schema RelationMessage fixture
// registered under id.
func knownRelation(id uint32, table string) *pglogrepl.RelationMessage {
	return &pglogrepl.RelationMessage{RelationID: id, Namespace: "public", RelationName: table}
}

// recordingHandler captures every delivered [Event] and optionally returns
// failOn for the call at index failAt (0-based), for exercising early-stop
// behavior in dispatchTruncate.
type recordingHandler struct {
	events []Event
	failAt int
	failOn error
}

type recordingLifecycle struct {
	hooks []fx.Hook
}

func (l *recordingLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

func (h *recordingHandler) handle(_ context.Context, ev Event) error {
	if h.failOn != nil && len(h.events) == h.failAt {
		return h.failOn
	}
	h.events = append(h.events, ev)
	return nil
}

func TestWithDefaults_slot(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Slot != defaultSlot {
		t.Errorf("Slot = %q, want %q", c.Slot, defaultSlot)
	}
}

func TestWithDefaults_publication(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Publication != defaultPublisher {
		t.Errorf("Publication = %q, want %q", c.Publication, defaultPublisher)
	}
}

func TestWithDefaults_standbyHz(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.StandbyHz != defaultStandbyHz {
		t.Errorf("StandbyHz = %d, want %d", c.StandbyHz, defaultStandbyHz)
	}
}

func TestWithDefaults_reconnectDelay(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.ReconnectDelay != defaultReconnect {
		t.Errorf("ReconnectDelay = %v, want %v", c.ReconnectDelay, defaultReconnect)
	}
}

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	c := Config{
		Slot: "myslot", Publication: "mypub", StandbyHz: 5,
		ReconnectDelay: 3 * time.Second,
	}.withDefaults()
	if c.Slot != "myslot" {
		t.Errorf("Slot = %q, want \"myslot\"", c.Slot)
	}
	if c.Publication != "mypub" {
		t.Errorf("Publication = %q, want \"mypub\"", c.Publication)
	}
	if c.StandbyHz != 5 {
		t.Errorf("StandbyHz = %d, want 5", c.StandbyHz)
	}
	if c.ReconnectDelay != 3*time.Second {
		t.Errorf("ReconnectDelay = %v, want 3s", c.ReconnectDelay)
	}
}

func TestConfigRejectsNegativeCadence(t *testing.T) {
	t.Parallel()

	base := Config{
		Slot:           "slot_01",
		Publication:    "publication",
		StandbyHz:      defaultStandbyHz,
		ReconnectDelay: defaultReconnect,
	}
	negativeStandby := base
	negativeStandby.StandbyHz = -1
	if err := negativeStandby.withDefaults().validate(); err == nil {
		t.Fatal("negative standby_hz accepted")
	}
	negativeReconnect := base
	negativeReconnect.ReconnectDelay = -time.Second
	if err := negativeReconnect.withDefaults().validate(); err == nil {
		t.Fatal("negative reconnect_delay accepted")
	}
}

func TestConsumerLifecycleOutlivesStartupContextAndJoinsOnStop(t *testing.T) {
	t.Parallel()

	lifecycle := &recordingLifecycle{}
	c := newConsumer(params{
		LC:     lifecycle,
		Cfg:    Config{DSN: "postgres://unused"}.withDefaults(),
		Clock:  clock.NewFake(),
		Logger: slog.New(slog.DiscardHandler),
	})
	c.SetHandler(noopHandler)
	started := make(chan context.Context, 1)
	finished := make(chan struct{})
	c.runSession = func(ctx context.Context) error {
		started <- ctx
		<-ctx.Done()
		close(finished)
		return ctx.Err()
	}

	if len(lifecycle.hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(lifecycle.hooks))
	}
	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := lifecycle.hooks[0].OnStart(startCtx); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	runCtx := <-started
	cancelStart()
	select {
	case <-runCtx.Done():
		t.Fatal("startup context cancellation stopped the consumer")
	default:
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := lifecycle.hooks[0].OnStop(stopCtx); err != nil {
		t.Fatalf("OnStop() error = %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("OnStop returned before the replication session exited")
	}
}

func TestConsumerLifecycleRejectsMissingHandler(t *testing.T) {
	t.Parallel()
	lifecycle := &recordingLifecycle{}
	c := newConsumer(params{
		LC:     lifecycle,
		Cfg:    Config{DSN: "postgres://unused"}.withDefaults(),
		Clock:  clock.NewFake(),
		Logger: slog.New(slog.DiscardHandler),
	})
	c.SetHandler(nil)
	if err := lifecycle.hooks[0].OnStart(context.Background()); !errors.Is(err, ErrNoHandler) {
		t.Fatalf("OnStart() error = %v, want ErrNoHandler", err)
	}
}

func TestConsumerLifecycleRejectsExcessiveStandbyHz(t *testing.T) {
	t.Parallel()
	lifecycle := &recordingLifecycle{}
	c := newConsumer(params{
		LC: lifecycle,
		Cfg: Config{
			DSN: "postgres://unused", StandbyHz: maxStandbyHz + 1,
		}.withDefaults(),
		Clock:  clock.NewFake(),
		Logger: slog.New(slog.DiscardHandler),
	})
	c.SetHandler(noopHandler)
	if err := lifecycle.hooks[0].OnStart(context.Background()); err == nil {
		t.Fatal("OnStart() accepted excessive standby_hz")
	}
}

func TestConsumerRunReconnectsFailedSessions(t *testing.T) {
	t.Parallel()

	fakeClock := clock.NewFake()
	var calls atomic.Int32
	called := make(chan int32, 3)
	c := &Consumer{
		cfg:    Config{ReconnectDelay: time.Second},
		clk:    fakeClock,
		logger: slog.New(slog.DiscardHandler),
	}
	c.runSession = func(ctx context.Context) error {
		call := calls.Add(1)
		called <- call
		if call < 3 {
			return errors.New("session failed")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run(runCtx)
	}()

	waitForCall(t, called, 1)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	if err := fakeClock.BlockUntilContext(waitCtx, 1); err != nil {
		cancelWait()
		t.Fatalf("wait for reconnect timer: %v", err)
	}
	cancelWait()
	fakeClock.Advance(time.Second)
	waitForCall(t, called, 2)
	waitCtx, cancelWait = context.WithTimeout(context.Background(), time.Second)
	if err := fakeClock.BlockUntilContext(waitCtx, 1); err != nil {
		cancelWait()
		t.Fatalf("wait for second reconnect timer: %v", err)
	}
	cancelWait()
	fakeClock.Advance(time.Second)
	waitForCall(t, called, 3)
	cancelRun()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop after cancellation")
	}
}

func waitForCall(t *testing.T, called <-chan int32, want int32) {
	t.Helper()
	select {
	case got := <-called:
		if got != want {
			t.Fatalf("session call = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for session call %d", want)
	}
}

func TestTransactionMetadataAndAcknowledgement(t *testing.T) {
	t.Parallel()

	commitTime := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	transaction := transactionState{}
	if err := transaction.begin(&pglogrepl.BeginMessage{FinalLSN: 41, CommitTime: commitTime}); err != nil {
		t.Fatalf("begin() error = %v", err)
	}
	handler := &recordingHandler{}
	c := &Consumer{handler: handler.handle}
	if err := c.deliver(context.Background(), &transaction, Event{Op: OpInsert, LSN: 7}); err != nil {
		t.Fatalf("deliver() error = %v", err)
	}
	if len(handler.events) != 1 {
		t.Fatalf("events = %d, want 1", len(handler.events))
	}
	if handler.events[0].LSN != 41 || !handler.events[0].CommitTime.Equal(commitTime) {
		t.Fatalf("event metadata = %+v", handler.events[0])
	}

	acknowledged := pglogrepl.LSN(3)
	if err := transaction.commit(&pglogrepl.CommitMessage{
		CommitLSN: 41, TransactionEndLSN: 42, CommitTime: commitTime,
	}, &acknowledged); err != nil {
		t.Fatalf("commit() error = %v", err)
	}
	if acknowledged != 42 {
		t.Fatalf("acknowledged LSN = %d, want 42", acknowledged)
	}
	if transaction.active {
		t.Fatal("transaction remained active after commit")
	}
}

func TestTransactionProtocolRejectsInvalidOrdering(t *testing.T) {
	t.Parallel()
	transaction := transactionState{}
	c := &Consumer{handler: noopHandler}
	if err := c.deliver(context.Background(), &transaction, Event{}); err == nil {
		t.Fatal("deliver outside a transaction succeeded")
	}
	position := pglogrepl.LSN(9)
	if err := transaction.commit(&pglogrepl.CommitMessage{TransactionEndLSN: 10}, &position); err == nil {
		t.Fatal("commit without begin succeeded")
	}
	if position != 9 {
		t.Fatalf("position advanced to %d after invalid commit", position)
	}
	if err := transaction.begin(&pglogrepl.BeginMessage{}); err != nil {
		t.Fatalf("first begin error = %v", err)
	}
	if err := transaction.begin(&pglogrepl.BeginMessage{}); err == nil {
		t.Fatal("nested begin succeeded")
	}
}

func TestHandleMessageRejectsEmptyCopyData(t *testing.T) {
	t.Parallel()
	c := &Consumer{logger: slog.New(slog.DiscardHandler), clk: clock.NewFake()}
	transaction := transactionState{}
	position := pglogrepl.LSN(0)
	nextStandby := time.Time{}
	stop := c.handleMessage(
		context.Background(),
		&pgproto3.CopyData{},
		map[uint32]*pglogrepl.RelationMessage{},
		&transaction,
		&position,
		&nextStandby,
	)
	if !stop {
		t.Fatal("empty CopyData did not stop the session")
	}
}

func TestHandleMessageKeepaliveAdvancesOnlyOutsideTransaction(t *testing.T) {
	t.Parallel()
	const (
		startPosition = pglogrepl.LSN(10)
		serverWALEnd  = pglogrepl.LSN(42)
	)

	for _, test := range []struct {
		name   string
		active bool
		want   pglogrepl.LSN
	}{
		{name: "idle consumer advances", want: serverWALEnd},
		{name: "active transaction stays pinned", active: true, want: startPosition},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			consumer := &Consumer{logger: slog.New(slog.DiscardHandler), clk: clock.NewFake()}
			transaction := transactionState{active: test.active}
			position := startPosition
			nextStandby := time.Time{}

			stop := consumer.handleMessage(
				t.Context(),
				primaryKeepalive(serverWALEnd),
				map[uint32]*pglogrepl.RelationMessage{},
				&transaction,
				&position,
				&nextStandby,
			)
			if stop {
				t.Fatal("keepalive stopped the replication session")
			}
			if position != test.want {
				t.Fatalf("client position = %s, want %s", position, test.want)
			}
		})
	}
}

func primaryKeepalive(serverWALEnd pglogrepl.LSN) *pgproto3.CopyData {
	data := make([]byte, 18)
	data[0] = pglogrepl.PrimaryKeepaliveMessageByteID
	binary.BigEndian.PutUint64(data[1:9], uint64(serverWALEnd))
	return &pgproto3.CopyData{Data: data}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_CDC_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Slot != defaultSlot {
		t.Errorf("Slot = %q, want %q", c.Slot, defaultSlot)
	}
	if c.Publication != defaultPublisher {
		t.Errorf("Publication = %q, want %q", c.Publication, defaultPublisher)
	}
	if c.StandbyHz != defaultStandbyHz {
		t.Errorf("StandbyHz = %d, want %d", c.StandbyHz, defaultStandbyHz)
	}
	if c.ReconnectDelay != defaultReconnect {
		t.Errorf("ReconnectDelay = %v, want %v", c.ReconnectDelay, defaultReconnect)
	}
}

func TestDispatchInsert(t *testing.T) {
	t.Parallel()
	rel := knownRelation(1, "orders")

	t.Run("known relation delivers event", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchInsert(context.Background(), &pglogrepl.InsertMessage{RelationID: 1}, relations, 42)
		if err != nil {
			t.Fatalf("dispatchInsert() error = %v", err)
		}
		if len(h.events) != 1 {
			t.Fatalf("events = %d, want 1", len(h.events))
		}
		ev := h.events[0]
		if ev.Op != OpInsert || ev.Schema != "public" || ev.Table != "orders" || ev.LSN != 42 {
			t.Errorf("event = %+v, unexpected", ev)
		}
	})

	t.Run("unknown relation fails", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchInsert(context.Background(), &pglogrepl.InsertMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if !errors.Is(err, ErrUnknownRelation) {
			t.Fatalf("dispatchInsert() error = %v, want ErrUnknownRelation", err)
		}
		if len(h.events) != 0 {
			t.Errorf("events = %d, want 0", len(h.events))
		}
	})

	t.Run("handler error propagates", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("boom")
		h := &recordingHandler{failAt: 0, failOn: wantErr}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchInsert(context.Background(), &pglogrepl.InsertMessage{RelationID: 1}, relations, 1)
		if !errors.Is(err, wantErr) {
			t.Fatalf("dispatchInsert() error = %v, want %v", err, wantErr)
		}
	})
}

func TestDispatchUpdate(t *testing.T) {
	t.Parallel()
	rel := knownRelation(1, "orders")

	t.Run("known relation delivers event", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchUpdate(context.Background(), &pglogrepl.UpdateMessage{RelationID: 1}, relations, 7)
		if err != nil {
			t.Fatalf("dispatchUpdate() error = %v", err)
		}
		if len(h.events) != 1 || h.events[0].Op != OpUpdate {
			t.Errorf("events = %+v, want one OpUpdate event", h.events)
		}
	})

	t.Run("unknown relation fails", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchUpdate(context.Background(), &pglogrepl.UpdateMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if !errors.Is(err, ErrUnknownRelation) {
			t.Fatalf("dispatchUpdate() error = %v, want ErrUnknownRelation", err)
		}
		if len(h.events) != 0 {
			t.Errorf("events = %d, want 0", len(h.events))
		}
	})

	t.Run("handler error propagates", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("boom")
		h := &recordingHandler{failAt: 0, failOn: wantErr}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchUpdate(context.Background(), &pglogrepl.UpdateMessage{RelationID: 1}, relations, 1)
		if !errors.Is(err, wantErr) {
			t.Fatalf("dispatchUpdate() error = %v, want %v", err, wantErr)
		}
	})
}

func TestDispatchDelete(t *testing.T) {
	t.Parallel()
	rel := knownRelation(1, "orders")

	t.Run("known relation delivers event", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchDelete(context.Background(), &pglogrepl.DeleteMessage{RelationID: 1}, relations, 3)
		if err != nil {
			t.Fatalf("dispatchDelete() error = %v", err)
		}
		if len(h.events) != 1 || h.events[0].Op != OpDelete {
			t.Errorf("events = %+v, want one OpDelete event", h.events)
		}
	})

	t.Run("unknown relation fails", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchDelete(context.Background(), &pglogrepl.DeleteMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if !errors.Is(err, ErrUnknownRelation) {
			t.Fatalf("dispatchDelete() error = %v, want ErrUnknownRelation", err)
		}
		if len(h.events) != 0 {
			t.Errorf("events = %d, want 0", len(h.events))
		}
	})

	t.Run("handler error propagates", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("boom")
		h := &recordingHandler{failAt: 0, failOn: wantErr}
		c := &Consumer{handler: h.handle}
		relations := map[uint32]*pglogrepl.RelationMessage{1: rel}
		err := c.dispatchDelete(context.Background(), &pglogrepl.DeleteMessage{RelationID: 1}, relations, 1)
		if !errors.Is(err, wantErr) {
			t.Fatalf("dispatchDelete() error = %v, want %v", err, wantErr)
		}
	})
}

func TestAfterStandbySend_success(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := &Consumer{logger: slog.New(slog.NewTextHandler(&buf, nil)), clk: clock.NewFake()}
	var next time.Time
	if stop := c.afterStandbySend(context.Background(), nil, &next, 5*time.Second); stop {
		t.Fatal("afterStandbySend(nil err) should not stop the loop")
	}
	if want := c.clk.Now().Add(5 * time.Second); !next.Equal(want) {
		t.Errorf("nextStandby = %v, want %v", next, want)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected log output on success: %s", buf.String())
	}
}

func TestAfterStandbySend_error(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := &Consumer{logger: slog.New(slog.NewTextHandler(&buf, nil)), clk: clock.NewFake()}
	before := c.clk.Now()
	next := before
	if stop := c.afterStandbySend(context.Background(), errors.New("boom"), &next, 5*time.Second); !stop {
		t.Fatal("afterStandbySend(err) should stop the loop")
	}
	if !next.Equal(before) {
		t.Errorf("nextStandby should be unchanged on error: got %v, want %v", next, before)
	}
	if !strings.Contains(buf.String(), "standby status update") {
		t.Errorf("expected an error log entry, got %q", buf.String())
	}
}

func TestClassifyReceive_success(t *testing.T) {
	t.Parallel()
	c := &Consumer{logger: slog.New(slog.DiscardHandler)}
	want := &pgproto3.CopyData{Data: []byte("x")}
	got, timedOut, stop := c.classifyReceive(context.Background(), want, nil)
	if got != want {
		t.Errorf("rawMsg = %v, want passthrough of %v", got, want)
	}
	if timedOut || stop {
		t.Errorf("timedOut=%v stop=%v, want both false on success", timedOut, stop)
	}
}

func TestClassifyReceive_gracefulShutdown(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := &Consumer{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ctx already done: a receive error here is a graceful shutdown, not a fault.
	_, timedOut, stop := c.classifyReceive(ctx, nil, errors.New("use of closed network connection"))
	if timedOut {
		t.Error("timedOut = true, want false")
	}
	if !stop {
		t.Error("stop = false, want true (graceful shutdown)")
	}
	if buf.Len() != 0 {
		t.Errorf("graceful shutdown must not log an error, got %q", buf.String())
	}
}

func TestClassifyReceive_fatalError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	c := &Consumer{logger: slog.New(slog.NewTextHandler(&buf, nil))}
	_, timedOut, stop := c.classifyReceive(context.Background(), nil, errors.New("connection reset by peer"))
	if timedOut {
		t.Error("timedOut = true, want false")
	}
	if !stop {
		t.Error("stop = false, want true")
	}
	if !strings.Contains(buf.String(), "receive message") {
		t.Errorf("expected the error to be logged, got %q", buf.String())
	}
}

func TestDispatchTruncate(t *testing.T) {
	t.Parallel()
	relations := map[uint32]*pglogrepl.RelationMessage{
		1: knownRelation(1, "orders"),
		2: knownRelation(2, "users"),
	}

	t.Run("delivers one event per known relation", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		msg := &pglogrepl.TruncateMessage{RelationIDs: []uint32{1, 2}}
		if err := c.dispatchTruncate(context.Background(), msg, relations, 5); err != nil {
			t.Fatalf("dispatchTruncate() error = %v", err)
		}
		if len(h.events) != 2 {
			t.Fatalf("events = %d, want 2", len(h.events))
		}
		if h.events[0].Table != "orders" || h.events[1].Table != "users" {
			t.Errorf("events = %+v, unexpected order/content", h.events)
		}
	})

	t.Run("unknown relation fails", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		msg := &pglogrepl.TruncateMessage{RelationIDs: []uint32{99}}
		if err := c.dispatchTruncate(context.Background(), msg, relations, 1); !errors.Is(err, ErrUnknownRelation) {
			t.Fatalf("dispatchTruncate() error = %v, want ErrUnknownRelation", err)
		}
		if len(h.events) != 0 {
			t.Errorf("events = %d, want 0", len(h.events))
		}
	})

	t.Run("stops at first handler error, leaving later IDs undelivered", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("boom")
		h := &recordingHandler{failAt: 1, failOn: wantErr} // fails on the 2nd delivered event
		c := &Consumer{handler: h.handle}
		msg := &pglogrepl.TruncateMessage{RelationIDs: []uint32{1, 2}}
		err := c.dispatchTruncate(context.Background(), msg, relations, 1)
		if !errors.Is(err, wantErr) {
			t.Fatalf("dispatchTruncate() error = %v, want %v", err, wantErr)
		}
		if len(h.events) != 1 {
			t.Errorf("events = %d, want 1 (first relation delivered before the failing second)", len(h.events))
		}
	})
}

func TestConfigValidationRejectsUnsafeReplicationNames(t *testing.T) {
	t.Parallel()
	valid := Config{Slot: "slot_01", Publication: "tenant orders", StandbyHz: 10}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for _, slot := range []string{"", "UPPER", "slot-name", "slot' INJECT", strings.Repeat("a", 64)} {
		cfg := valid
		cfg.Slot = slot
		if err := cfg.validate(); err == nil {
			t.Errorf("slot %q accepted", slot)
		}
	}
	for _, publication := range []string{"", "bad\x00name", strings.Repeat("a", 64)} {
		cfg := valid
		cfg.Publication = publication
		if err := cfg.validate(); err == nil {
			t.Errorf("publication %q accepted", publication)
		}
	}
}

func TestPublicationPluginArgQuotesOneIdentifier(t *testing.T) {
	t.Parallel()
	const name = "pub', proto_version '9"
	const want = `publication_names '"pub'', proto_version ''9"'`
	if got := publicationPluginArg(name); got != want {
		t.Fatalf("publicationPluginArg() = %q, want %q", got, want)
	}
}

func TestReceiveContextUsesRuntimeDeadline(t *testing.T) {
	t.Parallel()
	before := time.Now()
	ctx, cancel := receiveContext(context.Background(), 100*time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("receive context has no deadline")
	}
	remaining := deadline.Sub(before)
	if remaining < 50*time.Millisecond || remaining > 150*time.Millisecond {
		t.Fatalf("runtime deadline offset = %v, want about 100ms", remaining)
	}
}

func TestUpdateKeyTupleMapsOnlyReplicaIdentityColumns(t *testing.T) {
	t.Parallel()
	relation := relationWithColumns(
		&pglogrepl.RelationMessageColumn{Name: "tenant"},
		&pglogrepl.RelationMessageColumn{Flags: 1, Name: "id"},
		&pglogrepl.RelationMessageColumn{Name: "payload"},
	)
	message := &pglogrepl.UpdateMessage{
		RelationID:   relation.RelationID,
		OldTupleType: pglogrepl.UpdateMessageTupleTypeKey,
		OldTuple:     textTuple("42"),
		NewTuple:     textTuple("acme", "42", "new"),
	}
	event, err := updateEvent(message, map[uint32]*pglogrepl.RelationMessage{relation.RelationID: relation}, 7)
	if err != nil {
		t.Fatalf("updateEvent: %v", err)
	}
	if len(event.OldValues) != 1 || event.Old["id"] != "42" {
		t.Fatalf("old key tuple = %#v / %#v, want only id=42", event.OldValues, event.Old)
	}
	if _, exists := event.OldValues["tenant"]; exists {
		t.Fatal("key tuple was mapped to the first non-key relation column")
	}
}

func TestUpdateWithoutOldTuplePreservesAbsence(t *testing.T) {
	t.Parallel()
	relation := relationWithColumns(&pglogrepl.RelationMessageColumn{Name: "id"})
	event, err := updateEvent(
		&pglogrepl.UpdateMessage{
			RelationID: relation.RelationID,
			NewTuple:   textTuple("42"),
		},
		map[uint32]*pglogrepl.RelationMessage{relation.RelationID: relation},
		8,
	)
	if err != nil {
		t.Fatalf("updateEvent: %v", err)
	}
	if event.Old != nil || event.OldValues != nil {
		t.Fatalf("absent old tuple = %#v / %#v, want nil projections", event.OldValues, event.Old)
	}
}

func TestTupleValuesPreserveNullBinaryAndUnchanged(t *testing.T) {
	t.Parallel()
	relation := relationWithColumns(
		&pglogrepl.RelationMessageColumn{Name: "null_value"},
		&pglogrepl.RelationMessageColumn{Name: "empty_text"},
		&pglogrepl.RelationMessageColumn{Name: "binary_value"},
		&pglogrepl.RelationMessageColumn{Name: "unchanged_value"},
	)
	binaryData := []byte{0, 1, 2}
	tuple := &pglogrepl.TupleData{Columns: []*pglogrepl.TupleDataColumn{
		{DataType: pglogrepl.TupleDataTypeNull},
		{DataType: pglogrepl.TupleDataTypeText, Data: []byte{}},
		{DataType: pglogrepl.TupleDataTypeBinary, Data: binaryData},
		{DataType: pglogrepl.TupleDataTypeToast},
	}}
	event, err := insertEvent(
		&pglogrepl.InsertMessage{RelationID: relation.RelationID, Tuple: tuple},
		map[uint32]*pglogrepl.RelationMessage{relation.RelationID: relation},
		9,
	)
	if err != nil {
		t.Fatalf("insertEvent: %v", err)
	}
	if event.NewValues["null_value"].Kind != ColumnValueNull ||
		event.NewValues["empty_text"].Kind != ColumnValueText ||
		event.NewValues["binary_value"].Kind != ColumnValueBinary ||
		event.NewValues["unchanged_value"].Kind != ColumnValueUnchanged {
		t.Fatalf("canonical values lost tuple states: %#v", event.NewValues)
	}
	binaryData[0] = 9
	if event.NewValues["binary_value"].Data[0] != 0 {
		t.Fatal("canonical binary value aliases pgoutput input bytes")
	}
	if event.New["null_value"] != "" || event.New["empty_text"] != "" {
		t.Fatalf("legacy text projection changed: %#v", event.New)
	}
	if _, exists := event.New["binary_value"]; exists {
		t.Fatal("legacy text projection unexpectedly coerced binary data")
	}
}

func TestTupleValuesRejectMalformedShapes(t *testing.T) {
	t.Parallel()
	relation := relationWithColumns(&pglogrepl.RelationMessageColumn{Name: "id"})
	if _, err := tupleToValues(textTuple("1", "extra"), relation, false); err == nil {
		t.Fatal("tupleToValues accepted extra tuple columns")
	}
	unknown := &pglogrepl.TupleData{Columns: []*pglogrepl.TupleDataColumn{{DataType: 'x'}}}
	if _, err := tupleToValues(unknown, relation, false); err == nil {
		t.Fatal("tupleToValues accepted unknown column encoding")
	}
}

func relationWithColumns(columns ...*pglogrepl.RelationMessageColumn) *pglogrepl.RelationMessage {
	return &pglogrepl.RelationMessage{
		RelationID: 17, Namespace: "public", RelationName: "records", Columns: columns,
	}
}

func textTuple(values ...string) *pglogrepl.TupleData {
	columns := make([]*pglogrepl.TupleDataColumn, 0, len(values))
	for _, value := range values {
		columns = append(columns, &pglogrepl.TupleDataColumn{
			DataType: pglogrepl.TupleDataTypeText,
			Data:     []byte(value),
		})
	}
	return &pglogrepl.TupleData{Columns: columns}
}
