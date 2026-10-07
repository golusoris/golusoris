// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/fx"

	dbcdc "github.com/golusoris/golusoris/db/cdc"
	"github.com/golusoris/golusoris/outbox"
)

type recordingSink struct {
	events []outbox.Event
	err    error
}

type markerExecutorFunc func(context.Context, string, ...any) (pgconn.CommandTag, error)

func (f markerExecutorFunc) Exec(
	ctx context.Context,
	query string,
	args ...any,
) (pgconn.CommandTag, error) {
	return f(ctx, query, args...)
}

func TestProvideSinkGroupsSink(t *testing.T) {
	t.Parallel()
	want := &recordingSink{}
	var got []Sink
	app := fx.New(
		fx.NopLogger,
		ProvideSink(want),
		fx.Invoke(func(in struct {
			fx.In
			Sinks []Sink `group:"cdc_sinks"`
		},
		) {
			got = in.Sinks
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New(): %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("grouped sinks = %#v, want [%p]", got, want)
	}
}

func TestProvideSinkFnBuildsGroupedSink(t *testing.T) {
	t.Parallel()
	want := &recordingSink{}
	var got []Sink
	app := fx.New(
		fx.NopLogger,
		fx.Supply(want),
		ProvideSinkFn(func(sink *recordingSink) Sink { return sink }),
		fx.Invoke(func(in struct {
			fx.In
			Sinks []Sink `group:"cdc_sinks"`
		},
		) {
			got = in.Sinks
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New(): %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("grouped sinks = %#v, want [%p]", got, want)
	}
}

func TestProvideSinkRejectsTypedNil(t *testing.T) {
	t.Parallel()
	var sink *recordingSink
	app := fx.New(
		fx.NopLogger,
		ProvideSink(sink),
		fx.Invoke(func(in struct {
			fx.In
			Sinks []Sink `group:"cdc_sinks"`
		}) {
		}),
	)
	if !errors.Is(app.Err(), ErrNilSink) {
		t.Fatalf("fx.New() error = %v, want %v", app.Err(), ErrNilSink)
	}
}

type recordingNATSPublisher struct {
	ctx     context.Context
	subject string
	data    []byte
	err     error
}

func (p *recordingNATSPublisher) PublishSync(ctx context.Context, subject string, data []byte) error {
	p.ctx = ctx
	p.subject = subject
	p.data = append([]byte(nil), data...)
	return p.err
}

func (s *recordingSink) Send(_ context.Context, event outbox.Event) error {
	s.events = append(s.events, event)
	return s.err
}

func TestHandleRejectsMalformedOutboxRows(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	drainer := &Drainer{cfg: DefaultConfig(), sinks: []Sink{sink}}
	tests := []struct {
		name string
		row  map[string]string
	}{
		{name: "missing id", row: validRow()},
		{name: "zero id", row: withRowValue(validRow(), "id", "0")},
		{name: "missing kind", row: withRowValue(validRow(), "kind", "")},
		{name: "invalid payload", row: withRowValue(validRow(), "payload", "{")},
		{name: "invalid created time", row: withRowValue(validRow(), "created_at", "yesterday")},
	}
	delete(tests[0].row, "id")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := drainer.handle(context.Background(), dbcdc.Event{
				Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert, New: test.row,
			})
			if err == nil {
				t.Fatal("handle() accepted malformed row")
			}
		})
	}
	if len(sink.events) != 0 {
		t.Fatalf("malformed rows delivered %d events", len(sink.events))
	}
}

func TestHandleUsesCanonicalColumnStates(t *testing.T) {
	t.Parallel()
	row := validRow()
	values := make(map[string]dbcdc.ColumnValue, len(row))
	for name, value := range row {
		values[name] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueText, Data: []byte(value)}
	}
	values["id"] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueNull}
	sink := &recordingSink{}
	drainer := &Drainer{cfg: DefaultConfig(), sinks: []Sink{sink}}
	err := drainer.handle(context.Background(), dbcdc.Event{
		Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert,
		New: row, NewValues: values,
	})
	if err == nil || !strings.Contains(err.Error(), "expected text, got null") {
		t.Fatalf("handle canonical NULL error = %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("canonical NULL row reached sink through legacy text fallback")
	}
}

func TestRowToEventAcceptsPostgresTimestamptzText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want time.Time
	}{
		{
			name: "whole seconds and hour offset",
			text: "2026-09-19 14:00:00+02",
			want: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "fraction and minute offset",
			text: "2026-09-19 14:00:00.123456+02:30",
			want: time.Date(2026, time.September, 19, 11, 30, 0, 123456000, time.UTC),
		},
		{
			name: "RFC3339 compatibility",
			text: "2026-09-19T12:00:00Z",
			want: time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := withRowValue(validRow(), "created_at", test.text)
			event, err := rowToEvent(row)
			if err != nil {
				t.Fatalf("rowToEvent(): %v", err)
			}
			if !event.CreatedAt.Equal(test.want) {
				t.Fatalf("created_at = %s, want %s", event.CreatedAt, test.want)
			}
		})
	}
}

func TestHandlePropagatesSinkFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("unavailable")
	first := &recordingSink{}
	second := &recordingSink{err: want}
	drainer := &Drainer{cfg: DefaultConfig(), sinks: []Sink{first, second}}
	err := drainer.handle(context.Background(), dbcdc.Event{
		Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert, New: validRow(),
	})
	if !errors.Is(err, want) {
		t.Fatalf("handle() error = %v, want %v", err, want)
	}
	if len(first.events) != 1 || len(second.events) != 1 {
		t.Fatalf("sink calls = %d, %d; want 1, 1", len(first.events), len(second.events))
	}
}

func TestHandleRequiresSink(t *testing.T) {
	t.Parallel()
	drainer := &Drainer{cfg: DefaultConfig()}
	err := drainer.handle(context.Background(), dbcdc.Event{
		Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert, New: validRow(),
	})
	if !errors.Is(err, ErrNoSinks) {
		t.Fatalf("handle() error = %v; want ErrNoSinks", err)
	}
}

func TestHandleMarksRowOnlyAfterAllSinksSucceed(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	var markedID int64
	drainer := &Drainer{
		cfg:   DefaultConfig(),
		sinks: []Sink{sink},
		markDispatched: func(_ context.Context, id int64) error {
			markedID = id
			return nil
		},
	}
	err := drainer.handle(context.Background(), dbcdc.Event{
		Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert, New: validRow(),
	})
	if err != nil {
		t.Fatalf("handle(): %v", err)
	}
	if len(sink.events) != 1 || markedID != 42 {
		t.Fatalf("sink events = %d, marked ID = %d; want 1 and 42", len(sink.events), markedID)
	}
}

func TestHandlePropagatesDispatchMarkerFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("database unavailable")
	drainer := &Drainer{
		cfg:   DefaultConfig(),
		sinks: []Sink{&recordingSink{}},
		markDispatched: func(context.Context, int64) error {
			return want
		},
	}
	err := drainer.handle(context.Background(), dbcdc.Event{
		Schema: "public", Table: "golusoris_outbox", Op: dbcdc.OpInsert, New: validRow(),
	})
	if !errors.Is(err, want) {
		t.Fatalf("handle() error = %v; want marker failure", err)
	}
}

func TestConfiguredDispatchMarkerUsesWatchedRelation(t *testing.T) {
	t.Parallel()
	var query string
	executor := markerExecutorFunc(
		func(_ context.Context, gotQuery string, _ ...any) (pgconn.CommandTag, error) {
			query = gotQuery
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	)
	marker := configuredDispatchMarker(executor, Config{Schema: "tenant", Table: "custom_outbox"})
	if err := marker(context.Background(), 42); err != nil {
		t.Fatalf("configured marker: %v", err)
	}
	const want = `UPDATE "tenant"."custom_outbox" SET dispatched_at = now() WHERE id = $1`
	if query != want {
		t.Fatalf("query = %q, want %q", query, want)
	}
}

func TestNewDrainerDefaultsEmptyRelation(t *testing.T) {
	t.Parallel()
	consumer := &dbcdc.Consumer{}
	drainer, err := newDrainer(params{
		Cfg:      Config{},
		Consumer: consumer,
		Sinks:    []Sink{&recordingSink{}},
	})
	if err != nil {
		t.Fatalf("newDrainer(): %v", err)
	}
	if drainer.cfg != DefaultConfig() {
		t.Fatalf("config = %#v, want %#v", drainer.cfg, DefaultConfig())
	}
}

func TestNewDrainerRejectsInvalidRelation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		cfg  Config
	}{
		{name: "schema NUL", cfg: Config{Schema: "bad\x00schema", Table: outbox.DefaultTable}},
		{name: "table NUL", cfg: Config{Schema: outbox.DefaultSchema, Table: "bad\x00table"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := newDrainer(params{Cfg: test.cfg, Sinks: []Sink{&recordingSink{}}})
			if !errors.Is(err, outbox.ErrInvalidRelationIdentifier) {
				t.Fatalf("newDrainer() error = %v, want ErrInvalidRelationIdentifier", err)
			}
		})
	}
}

func TestNATSSinkFlushesBeforeSuccess(t *testing.T) {
	t.Parallel()
	publisher := &recordingNATSPublisher{}
	sink := &NATSSink{client: publisher, subject: "events.outbox"}
	ctx := context.Background()
	event := outbox.Event{ID: 42, Kind: "order.created", Payload: json.RawMessage(`{"id":1}`)}
	if err := sink.Send(ctx, event); err != nil {
		t.Fatalf("Send(): %v", err)
	}
	if publisher.ctx != ctx || publisher.subject != "events.outbox" || len(publisher.data) == 0 {
		t.Fatalf("PublishSync call = ctx:%v subject:%q bytes:%d", publisher.ctx, publisher.subject, len(publisher.data))
	}
}

func TestBuiltInSinksRejectMissingClients(t *testing.T) {
	t.Parallel()
	event := outbox.Event{Kind: "event", Payload: json.RawMessage(`{}`)}

	t.Run("kafka", func(t *testing.T) {
		t.Parallel()
		if err := NewKafkaSink(nil, "events").Send(t.Context(), event); err == nil {
			t.Fatal("missing Kafka client should return an error")
		}
	})

	t.Run("nats", func(t *testing.T) {
		t.Parallel()
		if err := NewNATSSink(nil, "events").Send(t.Context(), event); err == nil {
			t.Fatal("missing NATS client should return an error")
		}
	})

	t.Run("gcp", func(t *testing.T) {
		t.Parallel()
		if err := NewGCPSink(nil, "events").Send(t.Context(), event); err == nil {
			t.Fatal("missing GCP client should return an error")
		}
	})
}

func TestNewWebhookSinkIgnoresNilOption(t *testing.T) {
	t.Parallel()
	sink := NewWebhookSink("https://example.test/hook", nil)
	if sink.hc == nil {
		t.Fatal("NewWebhookSink() left HTTP client nil")
	}
}

func TestNewWebhookSinkBoundsAndClonesInjectedClient(t *testing.T) {
	t.Parallel()
	transport := http.DefaultTransport
	injected := &http.Client{Transport: transport}
	sink := NewWebhookSink("https://example.test/hook", WithWebhookHTTPClient(injected))
	if sink.hc == injected {
		t.Fatal("NewWebhookSink() retained caller-owned client")
	}
	if sink.hc.Timeout != defaultWebhookTimeout {
		t.Fatalf("client timeout = %s, want %s", sink.hc.Timeout, defaultWebhookTimeout)
	}
	if sink.hc.Transport != transport {
		t.Fatal("NewWebhookSink() did not preserve injected transport")
	}
	if injected.Timeout != 0 {
		t.Fatalf("injected client timeout mutated to %s", injected.Timeout)
	}

	customTimeout := 30 * time.Second
	sink = NewWebhookSink("https://example.test/hook", WithWebhookHTTPClient(&http.Client{
		Timeout: customTimeout,
	}))
	if sink.hc.Timeout != customTimeout {
		t.Fatalf("custom client timeout = %s, want %s", sink.hc.Timeout, customTimeout)
	}
}

func validRow() map[string]string {
	return map[string]string{
		"id": "42", "kind": "order.created", "payload": `{"order_id":"O-1"}`,
		"created_at": time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}
}

func withRowValue(row map[string]string, key, value string) map[string]string {
	row[key] = value
	return row
}

func TestBoundedWebhookClientOwnsItsTransport(t *testing.T) {
	t.Parallel()
	for name, client := range map[string]*http.Client{
		"nil client":               nil,
		"client without transport": {Timeout: time.Second},
	} {
		bounded := boundedWebhookClient(client)
		if bounded.Transport == nil || bounded.Transport == http.DefaultTransport {
			t.Errorf("%s: transport = %v; want a clone of http.DefaultTransport", name, bounded.Transport)
		}
	}
	custom := &http.Transport{}
	if got := boundedWebhookClient(&http.Client{Transport: custom}).Transport; got != custom {
		t.Errorf("caller transport replaced: got %v", got)
	}
}
