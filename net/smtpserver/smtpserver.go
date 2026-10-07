// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
//	smtp.tls_cert_file     # PEM certificate; pair with tls_key_file for STARTTLS
//	smtp.tls_key_file      # PEM private key
//	smtp.implicit_tls      # use implicit TLS instead of STARTTLS
//	smtp.allow_insecure_auth # permit AUTH before TLS (default false)
package smtpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
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
	Addr              string        `koanf:"addr"`
	Domain            string        `koanf:"domain"`
	MaxMessageBytes   int64         `koanf:"max_message_bytes"`
	MaxRecipients     int           `koanf:"max_recipients"`
	ReadTimeout       time.Duration `koanf:"read_timeout"`
	WriteTimeout      time.Duration `koanf:"write_timeout"`
	TLSCertFile       string        `koanf:"tls_cert_file"`
	TLSKeyFile        string        `koanf:"tls_key_file"`
	ImplicitTLS       bool          `koanf:"implicit_tls"`
	AllowInsecureAuth bool          `koanf:"allow_insecure_auth"`
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
var Module = fx.Module(
	"golusoris.net.smtpserver",
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
	if c.TLSCertFile == "" {
		c.TLSCertFile = cfg.String("smtp.tls.cert.file")
	}
	if c.TLSKeyFile == "" {
		c.TLSKeyFile = cfg.String("smtp.tls.key.file")
	}
	if !c.ImplicitTLS {
		c.ImplicitTLS = cfg.Bool("smtp.implicit.tls")
	}
	if !c.AllowInsecureAuth {
		c.AllowInsecureAuth = cfg.Bool("smtp.allow.insecure.auth")
	}
	return c.withDefaults(), nil
}

func startServer(p params) error {
	srv, err := newServer(p.Backend, p.Cfg)
	if err != nil {
		return err
	}

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, listenErr := listen(ctx, p.Cfg, srv.TLSConfig)
			if listenErr != nil {
				return listenErr
			}
			ready := &readyListener{Listener: listener, ready: make(chan struct{})}
			serveDone := make(chan error, 1)
			serveCtx := context.WithoutCancel(ctx)
			go func() {
				serveErr := srv.Serve(ready)
				if serveErr != nil {
					p.Logger.ErrorContext(serveCtx, "smtpserver: serve", "err", serveErr)
				}
				serveDone <- serveErr
			}()
			select {
			case <-ready.ready:
				p.Logger.InfoContext(ctx, "smtpserver: listening", "addr", listener.Addr())
				return nil
			case serveErr := <-serveDone:
				return fmt.Errorf("smtpserver: serve before startup: %w", serveErr)
			case <-ctx.Done():
				if closeErr := listener.Close(); closeErr != nil {
					return errors.Join(ctx.Err(), fmt.Errorf("smtpserver: close startup listener: %w", closeErr))
				}
				return fmt.Errorf("smtpserver: start: %w", ctx.Err())
			}
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
	return nil
}

type readyListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept() //nolint:wrapcheck // Preserve net.Error for go-smtp retry classification.
}

func listen(ctx context.Context, cfg Config, tlsConfig *tls.Config) (net.Listener, error) {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.withDefaults().Addr)
	if err != nil {
		return nil, fmt.Errorf("smtpserver: listen: %w", err)
	}
	if cfg.ImplicitTLS {
		return tls.NewListener(listener, tlsConfig), nil
	}
	return listener, nil
}

func newServer(backend gosmtp.Backend, cfg Config) (*gosmtp.Server, error) {
	cfg = cfg.withDefaults()
	certSet := cfg.TLSCertFile != ""
	keySet := cfg.TLSKeyFile != ""
	if certSet != keySet {
		return nil, errors.New("smtpserver: tls_cert_file and tls_key_file must both be set")
	}
	if cfg.ImplicitTLS && !certSet {
		return nil, errors.New("smtpserver: implicit TLS requires tls_cert_file and tls_key_file")
	}
	var tlsConfig *tls.Config
	if certSet {
		var err error
		tlsConfig, err = loadTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, err
		}
	}
	srv := gosmtp.NewServer(backend)
	srv.Addr = cfg.Addr
	srv.Domain = cfg.Domain
	srv.MaxMessageBytes = cfg.MaxMessageBytes
	srv.MaxRecipients = cfg.MaxRecipients
	srv.ReadTimeout = cfg.ReadTimeout
	srv.WriteTimeout = cfg.WriteTimeout
	srv.TLSConfig = tlsConfig
	srv.AllowInsecureAuth = cfg.AllowInsecureAuth
	return srv, nil
}

func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("smtpserver: load TLS certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
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
	if s.handler == nil {
		return errors.New("smtpserver: nil handler")
	}
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
