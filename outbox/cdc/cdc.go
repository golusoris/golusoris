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
//   - [WebhookSink] — HTTP POST to a URL (no external dep)
//
// Usage:
//
//	fx.New(
//	    dbcdc.Module,           // db/cdc: sets cdc.dsn + creates slot
//	    outboxcdc.Module,       // outbox/cdc: wires Drainer into fx
//	    fx.Provide(func(k *kafka.Client) outboxcdc.Sink {
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
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	dbcdc "github.com/golusoris/golusoris/db/cdc"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/pubsub/kafka"
	"github.com/golusoris/golusoris/pubsub/nats"
)

const (
	defaultTable  = "golusoris_outbox"
	defaultSchema = "public"
)

// Sink receives a decoded outbox event forwarded by the Drainer.
type Sink interface {
	Send(ctx context.Context, ev outbox.Event) error
}

// Config configures which table/schema to watch.
type Config struct {
	Table  string `koanf:"table"`
	Schema string `koanf:"schema"`
}

// DefaultConfig returns safe defaults.
func DefaultConfig() Config {
	return Config{Table: defaultTable, Schema: defaultSchema}
}

func (c Config) withDefaults() Config {
	if c.Table == "" {
		c.Table = defaultTable
	}
	if c.Schema == "" {
		c.Schema = defaultSchema
	}
	return c
}

// Drainer wires the db/cdc Consumer to an ordered set of Sinks.
type Drainer struct {
	cfg    Config
	sinks  []Sink
	logger *slog.Logger
}

// Module provides *Drainer into the fx graph.
// Requires *config.Config, *dbcdc.Consumer, []Sink (fx.Group "cdc_sinks"), *slog.Logger.
var Module = fx.Module("golusoris.outbox.cdc",
	fx.Provide(loadConfig),
	fx.Provide(newDrainer),
)

type params struct {
	fx.In
	LC       fx.Lifecycle
	Cfg      Config
	Consumer *dbcdc.Consumer
	Sinks    []Sink `group:"cdc_sinks"`
	Logger   *slog.Logger
}

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("outbox.cdc", &c); err != nil {
		return Config{}, fmt.Errorf("outbox/cdc: load config: %w", err)
	}
	return c.withDefaults(), nil
}

func newDrainer(p params) *Drainer {
	d := &Drainer{cfg: p.Cfg, sinks: p.Sinks, logger: p.Logger}
	p.Consumer.SetHandler(d.handle)
	return d
}

// handle is the db/cdc.Handler installed on the Consumer.
func (d *Drainer) handle(ctx context.Context, ev dbcdc.Event) error {
	if ev.Schema != d.cfg.Schema || ev.Table != d.cfg.Table {
		return nil // not our table
	}
	if ev.Op != dbcdc.OpInsert {
		return nil // only new rows are actionable
	}
	oe, err := rowToEvent(ev.New)
	if err != nil {
		d.logger.WarnContext(ctx, "outbox/cdc: decode row", "err", err)
		return nil // skip malformed rows; they'll be picked up by polling drainer
	}
	for _, s := range d.sinks {
		if err := s.Send(ctx, oe); err != nil {
			return fmt.Errorf("outbox/cdc: sink %T: %w", s, err)
		}
	}
	return nil
}

// rowToEvent parses the text columns from a WAL tuple into an outbox.Event.
func rowToEvent(cols map[string]string) (outbox.Event, error) {
	var ev outbox.Event
	if v, ok := cols["id"]; ok {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return ev, fmt.Errorf("id: %w", err)
		}
		ev.ID = id
	}
	ev.Kind = cols["kind"]
	if p, ok := cols["payload"]; ok {
		ev.Payload = json.RawMessage(p)
	}
	if v, ok := cols["created_at"]; ok {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err == nil {
			ev.CreatedAt = t
		}
	}
	return ev, nil
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
	client  *nats.Client
	subject string
}

// NewNATSSink returns a Sink that publishes events to subject via client.
func NewNATSSink(client *nats.Client, subject string) *NATSSink {
	return &NATSSink{client: client, subject: subject}
}

// Send implements [Sink].
func (s *NATSSink) Send(_ context.Context, ev outbox.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("nats sink: marshal: %w", err)
	}
	if err := s.client.Publish(s.subject, data); err != nil {
		return fmt.Errorf("nats sink: publish: %w", err)
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
	s := &WebhookSink{url: url, hc: &http.Client{Timeout: 10 * time.Second}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Send implements [Sink].
func (s *WebhookSink) Send(ctx context.Context, ev outbox.Event) error {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook sink: unexpected status %d", resp.StatusCode)
	}
	return nil
}
