// Package smtpserver provides an fx-wired inbound SMTP server using
// [emersion/go-smtp].
//
// Apps implement [smtp.Backend] / [smtp.Session] to process incoming messages,
// or use the built-in [HandlerBackend] which delivers to a simple callback.
//
// Usage:
//
//	fx.New(
//	    smtpserver.Module,
//	    fx.Provide(func() smtp.Backend {
//	        return smtpserver.NewHandlerBackend(func(env smtpserver.Envelope) error {
//	            slog.Info("mail received", "from", env.From, "to", env.To)
//	            return nil
//	        })
//	    }),
//	)
//
// Config keys (env: APP_SMTP_*):
//
//	smtp.addr              # listen address (default: :2525)
//	smtp.domain            # server EHLO domain (default: localhost)
//	smtp.max_message_bytes # max message size in bytes (default: 10 MiB)
//	smtp.max_recipients    # max recipients per message (default: 50)
//	smtp.read_timeout      # per-command read timeout (default: 60s)
//	smtp.write_timeout     # per-command write timeout (default: 60s)
package smtpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

const (
	defaultAddr            = ":2525"
	defaultDomain          = "localhost"
	defaultMaxMessageBytes = 10 << 20 // 10 MiB
	defaultMaxRecipients   = 50
	defaultTimeout         = 60 * time.Second
)

// Config holds SMTP server configuration.
type Config struct {
	Addr             string        `koanf:"addr"`
	Domain           string        `koanf:"domain"`
	MaxMessageBytes  int64         `koanf:"max_message_bytes"`
	MaxRecipients    int           `koanf:"max_recipients"`
	ReadTimeout      time.Duration `koanf:"read_timeout"`
	WriteTimeout     time.Duration `koanf:"write_timeout"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() Config {
	return Config{
		Addr:            defaultAddr,
		Domain:          defaultDomain,
		MaxMessageBytes: defaultMaxMessageBytes,
		MaxRecipients:   defaultMaxRecipients,
		ReadTimeout:     defaultTimeout,
		WriteTimeout:    defaultTimeout,
	}
}

func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = defaultAddr
	}
	if c.Domain == "" {
		c.Domain = defaultDomain
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
	if c.MaxRecipients <= 0 {
		c.MaxRecipients = defaultMaxRecipients
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = defaultTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaultTimeout
	}
	return c
}

// Module provides the SMTP server into the fx graph.
// Requires *config.Config, smtp.Backend, and *slog.Logger.
var Module = fx.Module("golusoris.net.smtpserver",
	fx.Provide(loadConfig),
	fx.Invoke(startServer),
)

type params struct {
	fx.In
	LC      fx.Lifecycle
	Cfg     Config
	Backend gosmtp.Backend
	Logger  *slog.Logger
}

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("smtp", &c); err != nil {
		return Config{}, fmt.Errorf("smtpserver: load config: %w", err)
	}
	return c.withDefaults(), nil
}

func startServer(p params) {
	srv := gosmtp.NewServer(p.Backend)
	srv.Addr = p.Cfg.Addr
	srv.Domain = p.Cfg.Domain
	srv.MaxMessageBytes = p.Cfg.MaxMessageBytes
	srv.MaxRecipients = p.Cfg.MaxRecipients
	srv.ReadTimeout = p.Cfg.ReadTimeout
	srv.WriteTimeout = p.Cfg.WriteTimeout
	srv.AllowInsecureAuth = true // TLS is opt-in via TLSConfig

	p.LC.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			go func() {
				if err := srv.ListenAndServe(); err != nil {
					p.Logger.Error("smtpserver: serve", "err", err)
				}
			}()
			p.Logger.Info("smtpserver: listening", "addr", p.Cfg.Addr)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
}

// ---------------------------------------------------------------------------
// Built-in handler backend
// ---------------------------------------------------------------------------

// Envelope holds a received email message.
type Envelope struct {
	// From is the MAIL FROM address.
	From string
	// To is the list of RCPT TO addresses.
	To []string
	// Data is the raw message bytes (RFC 5322).
	Data []byte
}

// MessageHandler is called for each received message.
type MessageHandler func(env Envelope) error

// HandlerBackend is a [gosmtp.Backend] that delivers to a [MessageHandler].
type HandlerBackend struct {
	handler MessageHandler
}

// NewHandlerBackend returns a Backend that calls h for every received message.
func NewHandlerBackend(h MessageHandler) *HandlerBackend {
	return &HandlerBackend{handler: h}
}

// NewSession implements [gosmtp.Backend].
func (b *HandlerBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &handlerSession{handler: b.handler}, nil
}

type handlerSession struct {
	handler MessageHandler
	env     Envelope
}

func (s *handlerSession) Mail(from string, _ *gosmtp.MailOptions) error {
	s.env.From = from
	return nil
}

func (s *handlerSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	s.env.To = append(s.env.To, to)
	return nil
}

func (s *handlerSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("smtpserver: read data: %w", err)
	}
	s.env.Data = data
	return s.handler(s.env)
}

func (s *handlerSession) Reset() {
	s.env = Envelope{}
}

func (s *handlerSession) Logout() error { return nil }
