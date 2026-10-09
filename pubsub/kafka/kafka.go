// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
//	tls:     false       # TLS with the system CA pool
//	ca:      "ca.crt"    # PEM CA bundle; implies TLS
//	sasl:
//	  mechanism:    "SCRAM-SHA-512" # PLAIN, SCRAM-SHA-256, SCRAM-SHA-512
//	  user:         "svc"
//	  password:     ""              # or passwordfile
//	  passwordfile: "/var/run/secrets/kafka/password"
//
// Leaf keys are single words because the env mapping splits on every
// underscore (APP_KAFKA_SASL_PASSWORDFILE -> kafka.sasl.passwordfile).
package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/tlsx"
)

// SASL mechanism names accepted by SASLConfig.Mechanism, case-insensitively.
const (
	SASLPlain       = "PLAIN"
	SASLScramSHA256 = "SCRAM-SHA-256"
	SASLScramSHA512 = "SCRAM-SHA-512"
)

var (
	// ErrUnsupportedSASLMechanism reports a mechanism other than PLAIN or SCRAM.
	ErrUnsupportedSASLMechanism = errors.New("kafka: unsupported SASL mechanism")
	// ErrSASLCredentials reports missing, duplicated, or orphaned SASL credentials.
	ErrSASLCredentials = errors.New("kafka: invalid SASL credentials")
)

// Config holds Kafka connection settings.
type Config struct {
	Brokers []string   `koanf:"brokers"` // e.g. ["localhost:9092"]
	Group   string     `koanf:"group"`   // consumer group ID
	TLS     bool       `koanf:"tls"`     // enable TLS (uses system CA pool)
	CA      string     `koanf:"ca"`      // PEM CA bundle verifying brokers; implies TLS
	SASL    SASLConfig `koanf:"sasl"`
}

// SASLConfig selects SASL authentication. An empty Mechanism disables SASL.
// Password and PasswordFile are mutually exclusive; the file is read once at
// construction and trailing line breaks are trimmed.
type SASLConfig struct {
	Mechanism    string `koanf:"mechanism"`
	User         string `koanf:"user"`
	Password     string `koanf:"password"`
	PasswordFile string `koanf:"passwordfile"`
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
var Module = fx.Module(
	"golusoris.kafka",
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

	opts, err := clientOptions(cfg, p.Logger)
	if err != nil {
		return nil, err
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

func clientOptions(cfg Config, logger *slog.Logger) ([]kgo.Opt, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.WithLogger(kgo.BasicLogger(newSlogWriter(logger), kgo.LogLevelInfo, nil)),
	}
	if cfg.Group != "" {
		opts = append(opts, kgo.ConsumerGroup(cfg.Group))
	}
	secure := cfg.TLS || cfg.CA != ""
	if secure {
		tlsCfg, err := tlsx.LoadClientConfig(tlsx.Files{CA: cfg.CA})
		if err != nil {
			return nil, fmt.Errorf("kafka: tls: %w", err)
		}
		// Keep the TLS 1.2 floor brokers accept; tlsx alone floors at 1.3.
		tlsCfg.MinVersion = tls.VersionTLS12
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	saslOpts, err := saslOptions(cfg.SASL)
	if err != nil {
		return nil, err
	}
	if !secure && strings.EqualFold(cfg.SASL.Mechanism, SASLPlain) {
		logger.Warn("kafka: SASL PLAIN without TLS sends the password in clear text")
	}
	return append(opts, saslOpts...), nil
}

// saslOptions returns no option when SASL is disabled.
func saslOptions(cfg SASLConfig) ([]kgo.Opt, error) {
	if cfg.Mechanism == "" {
		if cfg.User != "" || cfg.Password != "" || cfg.PasswordFile != "" {
			return nil, fmt.Errorf("%w: credentials set without sasl.mechanism", ErrSASLCredentials)
		}
		return nil, nil
	}
	password, err := saslPassword(cfg)
	if err != nil {
		return nil, err
	}
	var mechanism sasl.Mechanism
	switch strings.ToUpper(cfg.Mechanism) {
	case SASLPlain:
		mechanism = plain.Auth{User: cfg.User, Pass: password}.AsMechanism()
	case SASLScramSHA256:
		mechanism = scram.Auth{User: cfg.User, Pass: password}.AsSha256Mechanism()
	case SASLScramSHA512:
		mechanism = scram.Auth{User: cfg.User, Pass: password}.AsSha512Mechanism()
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedSASLMechanism, cfg.Mechanism)
	}
	return []kgo.Opt{kgo.SASL(mechanism)}, nil
}

func saslPassword(cfg SASLConfig) (string, error) {
	switch {
	case cfg.User == "":
		return "", fmt.Errorf("%w: sasl.user is required", ErrSASLCredentials)
	case cfg.Password != "" && cfg.PasswordFile != "":
		return "", fmt.Errorf("%w: sasl.password and sasl.passwordfile are mutually exclusive", ErrSASLCredentials)
	case cfg.PasswordFile != "":
		raw, err := os.ReadFile(filepath.Clean(cfg.PasswordFile))
		if err != nil {
			return "", fmt.Errorf("kafka: read sasl.passwordfile: %w", err)
		}
		password := strings.TrimRight(string(raw), "\r\n")
		if password == "" {
			return "", fmt.Errorf("%w: sasl.passwordfile is empty", ErrSASLCredentials)
		}
		return password, nil
	case cfg.Password == "":
		return "", fmt.Errorf("%w: sasl.password or sasl.passwordfile is required", ErrSASLCredentials)
	default:
		return cfg.Password, nil
	}
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

// ClientFromKgo wraps an already-constructed *kgo.Client. Intended for tests
// that need a Client without the full fx lifecycle (e.g. testutil/kafka).
func ClientFromKgo(kc *kgo.Client) *Client {
	return &Client{kc: kc, logger: slog.Default()}
}

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
