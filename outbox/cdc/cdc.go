// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cdc provides a CDC-based drain for the transactional outbox.
//
// Instead of polling the outbox table (as [outbox.Drainer] does), this drain
// subscribes to the PostgreSQL WAL via [db/cdc.Consumer] and forwards each
// committed INSERT on golusoris_outbox to one or more [Sink] implementations.
//
// This is an alternative (lower-latency, push-based) delivery path.  Choose it
// over the polling drainer when sub-second delivery is required and the Postgres
// logical-replication prerequisites are met.
//
// Sink implementations provided:
//   - [KafkaSink] — publishes to a Kafka topic via [pubsub/kafka.Client]
//   - [NATSSink]  — publishes to a NATS subject via [pubsub/nats.Client]
//   - [GCPSink]   — publishes to a GCP Pub/Sub topic via [pubsub/gcp.Client]
//   - [WebhookSink] — HTTP POST to a URL (no external dep)
//
// Usage:
//
//	fx.New(
//	    dbcdc.Module,           // db/cdc: sets cdc.dsn + creates slot
//	    outboxcdc.Module,       // outbox/cdc: wires Drainer into fx
//	    outboxcdc.ProvideSinkFn(func(k *kafka.Client) outboxcdc.Sink {
//	        return outboxcdc.NewKafkaSink(k, "outbox-events")
//	    }),
//	)
//
// Config keys (env: APP_OUTBOX_CDC_*):
//
//	outbox.cdc.table     # outbox table name to watch (default: golusoris_outbox)
//	outbox.cdc.schema    # outbox schema (default: public)
package cdc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	dbcdc "github.com/golusoris/golusoris/db/cdc"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/pubsub/gcp"
	"github.com/golusoris/golusoris/pubsub/kafka"
	"github.com/golusoris/golusoris/pubsub/nats"
)

const (
	defaultWebhookTimeout = 10 * time.Second
)

var (
	// ErrNoSinks prevents a configured CDC consumer from acknowledging events
	// that were delivered nowhere.
	ErrNoSinks = errors.New("outbox/cdc: at least one sink is required")
	// ErrNilSink rejects nil and typed-nil sink registrations.
	ErrNilSink = errors.New("outbox/cdc: sink is nil")
	// ErrWebhookRedirect reports a redirect rejected before forwarding payload.
	ErrWebhookRedirect  = errors.New("outbox/cdc: webhook redirect rejected")
	errNoDispatchMarker = errors.New("outbox/cdc: dispatch marker is not configured")
)

// Sink receives a decoded outbox event forwarded by the Drainer.
type Sink interface {
	Send(ctx context.Context, ev outbox.Event) error
}

// ProvideSink adds sink to the Fx value group consumed by [Module].
// Call it once for each sink an application wants to fan out to.
func ProvideSink(sink Sink) fx.Option {
	return fx.Provide(fx.Annotate(
		func() (Sink, error) {
			if validate.IsNil(sink) {
				return nil, ErrNilSink
			}
			return sink, nil
		},
		fx.ResultTags(`group:"cdc_sinks"`),
	))
}

// ProvideSinkFn adds an Fx constructor for a [Sink] to the value group
// consumed by [Module]. The constructor may use other graph dependencies.
func ProvideSinkFn(constructor any) fx.Option {
	return fx.Provide(fx.Annotate(
		constructor,
		fx.ResultTags(`group:"cdc_sinks"`),
	))
}

// Config configures which table/schema to watch.
type Config struct {
	Table  string `koanf:"table"`
	Schema string `koanf:"schema"`
}

// DefaultConfig returns safe defaults.
func DefaultConfig() Config {
	return Config{Table: outbox.DefaultTable, Schema: outbox.DefaultSchema}
}

func (c Config) withDefaults() Config {
	if c.Table == "" {
		c.Table = outbox.DefaultTable
	}
	if c.Schema == "" {
		c.Schema = outbox.DefaultSchema
	}
	return c
}

// Drainer wires the db/cdc Consumer to an ordered set of Sinks.
type Drainer struct {
	cfg            Config
	sinks          []Sink
	markDispatched func(context.Context, int64) error
}

// Module provides *Drainer into the fx graph.
// Requires *config.Config, *dbcdc.Consumer, and []Sink (fx.Group "cdc_sinks").
var Module = fx.Module(
	"golusoris.outbox.cdc",
	fx.Provide(loadConfig),
	fx.Provide(newDrainer),
)

type params struct {
	fx.In
	Cfg      Config
	Consumer *dbcdc.Consumer
	Pool     *pgxpool.Pool
	Sinks    []Sink `group:"cdc_sinks"`
}

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("outbox.cdc", &c); err != nil {
		return Config{}, fmt.Errorf("outbox/cdc: load config: %w", err)
	}
	c = c.withDefaults()
	if err := outbox.ValidateRelation(c.Schema, c.Table); err != nil {
		return Config{}, fmt.Errorf("outbox/cdc: validate config: %w", err)
	}
	return c, nil
}

func newDrainer(p params) (*Drainer, error) {
	cfg := p.Cfg.withDefaults()
	if err := outbox.ValidateRelation(cfg.Schema, cfg.Table); err != nil {
		return nil, fmt.Errorf("outbox/cdc: validate config: %w", err)
	}
	if len(p.Sinks) == 0 {
		return nil, ErrNoSinks
	}
	for _, sink := range p.Sinks {
		if validate.IsNil(sink) {
			return nil, ErrNilSink
		}
	}
	d := &Drainer{
		cfg:            cfg,
		sinks:          p.Sinks,
		markDispatched: configuredDispatchMarker(p.Pool, cfg),
	}
	p.Consumer.SetHandler(d.handle)
	return d, nil
}

func configuredDispatchMarker(executor outbox.Executor, cfg Config) func(context.Context, int64) error {
	return func(ctx context.Context, id int64) error {
		return outbox.MarkDispatchedIn(ctx, executor, cfg.Schema, cfg.Table, id)
	}
}

// handle is the db/cdc.Handler installed on the Consumer.
func (d *Drainer) handle(ctx context.Context, ev dbcdc.Event) error {
	if ev.Schema != d.cfg.Schema || ev.Table != d.cfg.Table {
		return nil // not our table
	}
	if ev.Op != dbcdc.OpInsert {
		return nil // only new rows are actionable
	}
	if len(d.sinks) == 0 {
		return ErrNoSinks
	}
	oe, err := decodeEventRow(ev)
	if err != nil {
		return fmt.Errorf("outbox/cdc: decode row: %w", err)
	}
	for _, s := range d.sinks {
		if sinkErr := s.Send(ctx, oe); sinkErr != nil {
			return fmt.Errorf("outbox/cdc: sink %T: %w", s, sinkErr)
		}
	}
	if d.markDispatched == nil {
		return errNoDispatchMarker
	}
	if err = d.markDispatched(ctx, oe.ID); err != nil {
		return fmt.Errorf("outbox/cdc: mark event %d dispatched: %w", oe.ID, err)
	}
	return nil
}

func decodeEventRow(event dbcdc.Event) (outbox.Event, error) {
	if event.NewValues != nil {
		return rowValuesToEvent(event.NewValues)
	}
	return rowToEvent(event.New)
}

// rowToEvent preserves the legacy text-map test and caller boundary.
func rowToEvent(cols map[string]string) (outbox.Event, error) {
	values := make(map[string]dbcdc.ColumnValue, len(cols))
	for name, value := range cols {
		values[name] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueText, Data: []byte(value)}
	}
	return rowValuesToEvent(values)
}

func rowValuesToEvent(cols map[string]dbcdc.ColumnValue) (outbox.Event, error) {
	var ev outbox.Event
	idText, err := requiredTextColumn(cols, "id")
	if err != nil {
		return ev, err
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return ev, fmt.Errorf("id %q: must be a positive integer", idText)
	}
	ev.ID = id
	ev.Kind, err = requiredTextColumn(cols, "kind")
	if err != nil {
		return ev, err
	}
	if ev.Kind == "" {
		return ev, errors.New("kind: required")
	}
	payloadText, err := requiredTextColumn(cols, "payload")
	if err != nil {
		return ev, err
	}
	payload := json.RawMessage(payloadText)
	if !json.Valid(payload) {
		return ev, errors.New("payload: invalid JSON")
	}
	ev.Payload = payload
	createdAtText, err := requiredTextColumn(cols, "created_at")
	if err != nil {
		return ev, err
	}
	createdAt, err := parsePostgresTimestamptz(createdAtText)
	if err != nil {
		return ev, fmt.Errorf("created_at %q: %w", createdAtText, err)
	}
	ev.CreatedAt = createdAt
	return ev, nil
}

func requiredTextColumn(cols map[string]dbcdc.ColumnValue, name string) (string, error) {
	value, ok := cols[name]
	if !ok {
		return "", fmt.Errorf("%s: required", name)
	}
	text, ok := value.Text()
	if !ok {
		return "", fmt.Errorf("%s: expected text, got %s", name, value.Kind)
	}
	return text, nil
}

func parsePostgresTimestamptz(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	var parsed pgtype.Timestamptz
	codec := pgtype.TimestamptzCodec{ScanLocation: time.UTC}
	plan := codec.PlanScan(nil, pgtype.TimestamptzOID, pgtype.TextFormatCode, &parsed)
	if err := plan.Scan([]byte(value), &parsed); err != nil {
		return time.Time{}, fmt.Errorf("parse PostgreSQL timestamptz: %w", err)
	}
	if !parsed.Valid || parsed.InfinityModifier != pgtype.Finite {
		return time.Time{}, errors.New("PostgreSQL timestamptz must be finite")
	}
	return parsed.Time, nil
}

// ---------------------------------------------------------------------------
// Built-in sinks
// ---------------------------------------------------------------------------

// KafkaSink sends outbox events to a Kafka topic as JSON.
type KafkaSink struct {
	client *kafka.Client
	topic  string
}

// NewKafkaSink returns a Sink that publishes events to topic via client.
func NewKafkaSink(client *kafka.Client, topic string) *KafkaSink {
	return &KafkaSink{client: client, topic: topic}
}

// Send implements [Sink].
func (s *KafkaSink) Send(ctx context.Context, ev outbox.Event) error {
	if s.client == nil {
		return errors.New("kafka sink: client is required")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("kafka sink: marshal: %w", err)
	}
	key := []byte(ev.Kind)
	rec := &kafka.Record{Topic: s.topic, Key: key, Value: data}
	if err := s.client.Produce(ctx, rec); err != nil {
		return fmt.Errorf("kafka sink: produce: %w", err)
	}
	return nil
}

// NATSSink publishes outbox events to a NATS subject as JSON.
type NATSSink struct {
	client  natsPublisher
	subject string
}

type natsPublisher interface {
	PublishSync(context.Context, string, []byte) error
}

// NewNATSSink returns a Sink that publishes events to subject via client.
func NewNATSSink(client *nats.Client, subject string) *NATSSink {
	var publisher natsPublisher
	if client != nil {
		publisher = client
	}
	return &NATSSink{client: publisher, subject: subject}
}

// Send implements [Sink].
func (s *NATSSink) Send(ctx context.Context, ev outbox.Event) error {
	if validate.IsNil(s.client) {
		return errors.New("nats sink: client is required")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("nats sink: marshal: %w", err)
	}
	if err := s.client.PublishSync(ctx, s.subject, data); err != nil {
		return fmt.Errorf("nats sink: publish: %w", err)
	}
	return nil
}

// GCPSink publishes outbox events to a Google Cloud Pub/Sub topic as JSON,
// with the event kind carried as a message attribute (mirroring the AGENTS.md
// usage example in pubsub/gcp).
type GCPSink struct {
	client  *gcp.Client
	topicID string
}

// NewGCPSink returns a Sink that publishes events to topicID via client.
func NewGCPSink(client *gcp.Client, topicID string) *GCPSink {
	return &GCPSink{client: client, topicID: topicID}
}

// Send implements [Sink].
func (s *GCPSink) Send(ctx context.Context, ev outbox.Event) error {
	if s.client == nil {
		return errors.New("gcp sink: client is required")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("gcp sink: marshal: %w", err)
	}
	attrs := map[string]string{"kind": ev.Kind}
	if _, err := s.client.Publish(ctx, s.topicID, data, attrs); err != nil {
		return fmt.Errorf("gcp sink: publish: %w", err)
	}
	return nil
}

// WebhookSink POSTs outbox events to an HTTP endpoint as JSON.
type WebhookSink struct {
	url    string
	secret string // if non-empty, added as X-Webhook-Secret header
	hc     *http.Client
}

// WebhookOption configures a [WebhookSink].
type WebhookOption func(*WebhookSink)

// WithWebhookSecret sets the shared secret sent as X-Webhook-Secret.
func WithWebhookSecret(secret string) WebhookOption {
	return func(s *WebhookSink) { s.secret = secret }
}

// WithWebhookHTTPClient overrides the default http.Client.
func WithWebhookHTTPClient(hc *http.Client) WebhookOption {
	return func(s *WebhookSink) { s.hc = hc }
}

// NewWebhookSink returns a Sink that HTTP POSTs events to url.
func NewWebhookSink(url string, opts ...WebhookOption) *WebhookSink {
	s := &WebhookSink{url: url}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	s.hc = boundedWebhookClient(s.hc)
	return s
}

func boundedWebhookClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: defaultWebhookTimeout}
	}
	bounded := *client
	if bounded.Timeout <= 0 {
		bounded.Timeout = defaultWebhookTimeout
	}
	// A nil Transport means the process-wide http.DefaultTransport, whose idle
	// connections any other code may close mid-request; own a clone instead.
	if bounded.Transport == nil {
		if base, ok := http.DefaultTransport.(*http.Transport); ok {
			bounded.Transport = base.Clone()
		}
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &bounded
}

// Send implements [Sink].
func (s *WebhookSink) Send(ctx context.Context, ev outbox.Event) (err error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("webhook sink: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook sink: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.secret != "" {
		req.Header.Set("X-Webhook-Secret", s.secret)
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("webhook sink: do: %w", err)
	}
	// Literal Close so bodyclose sees it; a close failure never masks the primary error.
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("webhook sink: close body: %w", cerr)
		}
	}()
	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return fmt.Errorf("%w: status %d", ErrWebhookRedirect, resp.StatusCode)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook sink: unexpected status %d", resp.StatusCode)
	}
	return nil
}
