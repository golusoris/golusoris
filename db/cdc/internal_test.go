// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgproto3"

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

func TestWithDefaults_preservesExisting(t *testing.T) {
	t.Parallel()
	c := Config{Slot: "myslot", Publication: "mypub", StandbyHz: 5}.withDefaults()
	if c.Slot != "myslot" {
		t.Errorf("Slot = %q, want \"myslot\"", c.Slot)
	}
	if c.Publication != "mypub" {
		t.Errorf("Publication = %q, want \"mypub\"", c.Publication)
	}
	if c.StandbyHz != 5 {
		t.Errorf("StandbyHz = %d, want 5", c.StandbyHz)
	}
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

	t.Run("unknown relation is dropped", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchInsert(context.Background(), &pglogrepl.InsertMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if err != nil {
			t.Fatalf("dispatchInsert() error = %v", err)
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

	t.Run("unknown relation is dropped", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchUpdate(context.Background(), &pglogrepl.UpdateMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if err != nil {
			t.Fatalf("dispatchUpdate() error = %v", err)
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

	t.Run("unknown relation is dropped", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		err := c.dispatchDelete(context.Background(), &pglogrepl.DeleteMessage{RelationID: 99}, map[uint32]*pglogrepl.RelationMessage{}, 1)
		if err != nil {
			t.Fatalf("dispatchDelete() error = %v", err)
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

	t.Run("delivers one event per known relation, skips unknown", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		msg := &pglogrepl.TruncateMessage{RelationIDs: []uint32{1, 99, 2}}
		if err := c.dispatchTruncate(context.Background(), msg, relations, 5); err != nil {
			t.Fatalf("dispatchTruncate() error = %v", err)
		}
		if len(h.events) != 2 {
			t.Fatalf("events = %d, want 2 (unknown relation 99 must be skipped)", len(h.events))
		}
		if h.events[0].Table != "orders" || h.events[1].Table != "users" {
			t.Errorf("events = %+v, unexpected order/content", h.events)
		}
	})

	t.Run("no known relations is a no-op", func(t *testing.T) {
		t.Parallel()
		h := &recordingHandler{}
		c := &Consumer{handler: h.handle}
		msg := &pglogrepl.TruncateMessage{RelationIDs: []uint32{99}}
		if err := c.dispatchTruncate(context.Background(), msg, relations, 1); err != nil {
			t.Fatalf("dispatchTruncate() error = %v", err)
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
