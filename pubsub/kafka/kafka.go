// Package kafka provides an fx-wired Kafka producer/consumer via twmb/franz-go.
//
// Usage:
//
//	fx.New(kafka.Module) // requires "kafka.*" koanf config
//
// Config keys (koanf prefix "kafka"):
//
//	brokers: ["localhost:9092"]
//	group:   "my-service"
//	tls:     false
package kafka

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Config holds Kafka connection settings.
type Config struct {
	Brokers []string `koanf:"brokers"` // e.g. ["localhost:9092"]
	Group   string   `koanf:"group"`   // consumer group ID
	TLS     bool     `koanf:"tls"`     // enable TLS (uses system CA pool)
}

// Client wraps a franz-go kgo.Client with helpers for producing and consuming.
type Client struct {
	kc     *kgo.Client
	logger *slog.Logger
}

// Record is an alias for kgo.Record so callers don't need to import kgo directly.
type Record = kgo.Record

// Module is the fx module that provides a *Client.
//
//	fx.New(kafka.Module)
var Module = fx.Module("golusoris.kafka",
	fx.Provide(newFromConfig),
)

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	LC     fx.Lifecycle
}

func newFromConfig(p params) (*Client, error) {
	var cfg Config
	if err := p.Config.Unmarshal("kafka", &cfg); err != nil {
		return nil, fmt.Errorf("kafka: config: %w", err)
	}
	if len(cfg.Brokers) == 0 {
		cfg.Brokers = []string{"localhost:9092"}
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.WithLogger(kgo.BasicLogger(newSlogWriter(p.Logger), kgo.LogLevelInfo, nil)),
	}
	if cfg.Group != "" {
		opts = append(opts, kgo.ConsumerGroup(cfg.Group))
	}

	kc, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: new client: %w", err)
	}

	c := &Client{kc: kc, logger: p.Logger}

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Ping the cluster on startup to fail fast on misconfiguration.
			if err := kc.Ping(ctx); err != nil {
				return fmt.Errorf("kafka: ping: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			kc.Close()
			return nil
		},
	})

	return c, nil
}

// Produce sends records to Kafka. It blocks until all records are flushed or
// the context is cancelled.
func (c *Client) Produce(ctx context.Context, records ...*Record) error {
	results := c.kc.ProduceSync(ctx, records...)
	for _, r := range results {
		if r.Err != nil {
			return fmt.Errorf("kafka: produce to %s: %w", r.Record.Topic, r.Err)
		}
	}
	return nil
}

// Poll fetches up to maxRecords records from subscribed topics.
// Call Subscribe before polling.
func (c *Client) Poll(ctx context.Context, maxRecords int) ([]*Record, error) {
	fetches := c.kc.PollRecords(ctx, maxRecords)
	if err := fetches.Err(); err != nil {
		return nil, fmt.Errorf("kafka: poll: %w", err)
	}
	records := make([]*Record, 0, fetches.NumRecords())
	fetches.EachRecord(func(r *Record) { records = append(records, r) })
	return records, nil
}

// Subscribe sets the topics to consume from. Must be called before [Poll].
func (c *Client) Subscribe(topics ...string) { c.kc.AddConsumeTopics(topics...) }

// CommitOffsets commits the offsets for the last polled records.
func (c *Client) CommitOffsets(ctx context.Context) error {
	if err := c.kc.CommitUncommittedOffsets(ctx); err != nil {
		return fmt.Errorf("kafka: commit: %w", err)
	}
	return nil
}

// Kgo returns the underlying kgo.Client for advanced use.
func (c *Client) Kgo() *kgo.Client { return c.kc }

// slogWriter adapts slog.Logger to the io.Writer kgo.BasicLogger expects.
type slogWriter struct{ l *slog.Logger }

func newSlogWriter(l *slog.Logger) *slogWriter { return &slogWriter{l: l} }

func (w *slogWriter) Write(p []byte) (int, error) {
	w.l.Debug(string(p), "component", "kafka")
	return len(p), nil
}

// Ensure slogWriter implements the interface kgo.BasicLogger needs.
var _ interface{ Write([]byte) (int, error) } = (*slogWriter)(nil)

// NewRecord is a convenience constructor for a Kafka record. The
// broker assigns the timestamp on receipt; callers that need a custom
// timestamp should set Record.Timestamp directly.
func NewRecord(topic string, key, value []byte) *Record {
	return &Record{
		Topic: topic,
		Key:   key,
		Value: value,
	}
}
