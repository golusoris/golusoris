// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package grpc provides an fx-wired gRPC server and client connection factory
// with OpenTelemetry tracing, panic recovery, and structured logging built in.
//
// Server-side — register services via fx.Invoke after adding the module:
//
//	fx.Invoke(func(s *grpc.Server) {
//	    mypb.RegisterMyServiceServer(s, &myImpl{})
//	})
//
// The server listens on Config.Listen on fx Start and stops gracefully on
// fx Stop.
//
// Client-side — inject [*ConnFactory] and dial with automatic OTel tracing:
//
//	fx.Invoke(func(cf *grpc.ConnFactory) {
//	    conn, err := cf.Dial(ctx, "payment-service:9090")
//	    mypb.NewPaymentClient(conn)
//	})
//
// Config keys (env: APP_GRPC_*; keys with underscores need [CompoundKeys]):
//
//	grpc.listen         # server bind address (default: :9090)
//	grpc.tls            # enable server TLS (default: false)
//	grpc.cert_file      # TLS cert path (reloaded on rotation)
//	grpc.key_file       # TLS key path
//	grpc.ca_file        # CA bundle that verifies client certificates (mTLS)
//	grpc.client_auth    # none|request|require_any|verify_if_given|require_and_verify
//	                    # (default: require_and_verify with ca_file, else none)
//	grpc.max_recv_size  # max incoming message size in bytes (default: 4 MiB)
//	grpc.max_send_size  # max outgoing message size in bytes (default: 4 MiB)
//	grpc.health         # register grpc.health.v1 fed by readiness checks
//	grpc.keepalive.max_connection_age        # default 2m; negative = never
//	grpc.keepalive.max_connection_age_grace  # default 5s; negative = unlimited
//	grpc.keepalive.time                      # server ping interval (default 1m)
//	grpc.keepalive.timeout                   # ping ack timeout (default 20s)
//	grpc.keepalive.min_time                  # min client ping interval (default 5m)
//	grpc.keepalive.permit_without_stream     # allow client pings without RPCs
//
// Client keys (Module's *ConnFactory; zero = plaintext, no pings, no retries):
//
//	grpc.client.tls                  # TLS 1.3 to the server
//	grpc.client.cert_file            # client certificate for mTLS (reloaded)
//	grpc.client.key_file             # client key
//	grpc.client.ca_file              # CA bundle that verifies the server
//	grpc.client.server_name          # authority override for TLS verification
//	grpc.client.keepalive.time       # ping after idle period (0 = off)
//	grpc.client.keepalive.timeout    # ping ack timeout
//	grpc.client.keepalive.permit_without_stream
//	grpc.client.retry.max_attempts   # >1 retries UNAVAILABLE (grpc-go caps at 5)
//	grpc.client.retry.initial_backoff     # default 100ms
//	grpc.client.retry.max_backoff         # default 1s
//	grpc.client.retry.backoff_multiplier  # default 2
package grpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	grpclogging "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpchealth "google.golang.org/grpc/health"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/drain"
	"github.com/golusoris/golusoris/observability/statuspage"
)

const (
	defaultListen      = ":9090"
	defaultMaxMsgBytes = 4 << 20 // 4 MiB
)

// Config holds gRPC server configuration.
type Config struct {
	// Listen is the TCP address the server binds to (default: ":9090").
	Listen string `koanf:"listen"`
	// TLS enables mutual/one-way TLS. Requires CertFile + KeyFile.
	TLS bool `koanf:"tls"`
	// CertFile is the path to the TLS certificate PEM.
	CertFile string `koanf:"cert_file"`
	// KeyFile is the path to the TLS private key PEM.
	KeyFile string `koanf:"key_file"`
	// MaxRecvSize caps the maximum incoming message in bytes (default: 4 MiB).
	MaxRecvSize int `koanf:"max_recv_size"`
	// MaxSendSize caps the maximum outgoing message in bytes (default: 4 MiB).
	MaxSendSize int `koanf:"max_send_size"`
	// CAFile is the PEM bundle that verifies client certificates (mTLS).
	CAFile string `koanf:"ca_file"`
	// ClientAuth is the client-certificate policy (core/tlsx ParseClientAuth modes).
	ClientAuth string `koanf:"client_auth"`
	// Keepalive tunes keepalive pings and connection rotation.
	Keepalive KeepaliveConfig `koanf:"keepalive"`
	// Health registers grpc.health.v1, fed by readiness-tagged checks when a
	// *statuspage.Registry is in the graph. Off by default: apps that register
	// their own health service would otherwise collide.
	Health bool `koanf:"health"`
	// Client configures the Module's *ConnFactory.
	Client ClientConfig `koanf:"client"`
}

// DefaultConfig returns the opinionated default server config.
func DefaultConfig() Config {
	return Config{
		Listen:      defaultListen,
		MaxRecvSize: defaultMaxMsgBytes,
		MaxSendSize: defaultMaxMsgBytes,
		Keepalive:   defaultKeepalive(),
	}
}

func (c Config) withDefaults() Config {
	if c.Listen == "" {
		c.Listen = defaultListen
	}
	if c.MaxRecvSize == 0 {
		c.MaxRecvSize = defaultMaxMsgBytes
	}
	if c.MaxSendSize == 0 {
		c.MaxSendSize = defaultMaxMsgBytes
	}
	c.Keepalive = c.Keepalive.withDefaults()
	return c
}

// CompoundKeys lists the grpc.* keys whose names contain underscores. Pass
// them to config.Options.CompoundKeys so APP_GRPC_* variables reach them.
func CompoundKeys() []string {
	return []string{
		"grpc.cert_file", "grpc.key_file", "grpc.ca_file", "grpc.client_auth",
		"grpc.max_recv_size", "grpc.max_send_size",
		"grpc.keepalive.max_connection_age", "grpc.keepalive.max_connection_age_grace",
		"grpc.keepalive.min_time", "grpc.keepalive.permit_without_stream",
		"grpc.client.cert_file", "grpc.client.key_file", "grpc.client.ca_file", "grpc.client.server_name",
		"grpc.client.keepalive.permit_without_stream",
		"grpc.client.retry.max_attempts", "grpc.client.retry.initial_backoff",
		"grpc.client.retry.max_backoff", "grpc.client.retry.backoff_multiplier",
	}
}

// ConnFactory creates client connections with OTel instrumentation.
type ConnFactory struct {
	dialOpts []grpc.DialOption
}

// Dial opens a gRPC connection to target.
// The connection inherits OTel trace propagation automatically.
func (f *ConnFactory) Dial(_ context.Context, target string, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts := append(f.dialOpts, extra...) //nolint:gocritic // appendAssign: safe — f.dialOpts not reused
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc: dial %s: %w", target, err)
	}
	return conn, nil
}

// Module provides *grpc.Server + *ConnFactory into the fx graph.
// Requires *config.Config and *slog.Logger.
var Module = fx.Module(
	"golusoris.grpc",
	fx.Provide(loadConfig),
	fx.Provide(newServer),
	fx.Provide(newConnFactory),
)

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("grpc", &c); err != nil {
		return Config{}, fmt.Errorf("grpc: load config: %w", err)
	}
	return c.withDefaults(), nil
}

// ProvideServerOption wires an app-supplied [grpc.ServerOption] into the server
// — including custom interceptors passed as grpc.ChainUnary/StreamInterceptor.
// Framework interceptors (OTel, logging, recovery) always run; app options are
// appended after them.
func ProvideServerOption(opt grpc.ServerOption) fx.Option {
	return fx.Provide(fx.Annotate(
		func() grpc.ServerOption { return opt },
		fx.ResultTags(`group:"grpc.serveropts"`),
	))
}

// ProvideServerOptionFn wires a constructor that builds a [grpc.ServerOption]
// from other fx-provided dependencies — for interceptors that depend on
// graph-constructed singletons (an auth middleware, a rate limiter, …) that a
// concrete [ProvideServerOption] can't reach. The constructor is any
// fx-compatible func whose parameters are injected from the graph and that
// returns a grpc.ServerOption (optionally with an error):
//
//	grpc.ProvideServerOptionFn(func(m *auth.Middleware) grpc.ServerOption {
//	    return grpc.ChainUnaryInterceptor(m.UnaryServerInterceptor)
//	})
func ProvideServerOptionFn(constructor any) fx.Option {
	return fx.Provide(fx.Annotate(
		constructor,
		fx.ResultTags(`group:"grpc.serveropts"`),
	))
}

// serverParams are the fx inputs to newServer.
type serverParams struct {
	fx.In
	LC      fx.Lifecycle
	Config  Config
	Logger  *slog.Logger
	Options []grpc.ServerOption `group:"grpc.serveropts"`
	// Gate, when k8s/health.Module is wired, holds GracefulStop until readiness has drained.
	Gate drain.Gate `optional:"true"`
	// Clock paces TLS file reloads; the wall clock when absent.
	Clock clock.Clock `optional:"true"`
	// Registry feeds the health service's readiness answer.
	Registry *statuspage.Registry `optional:"true"`
}

func newServer(p serverParams) (*grpc.Server, error) {
	cfg, logger := p.Config.withDefaults(), p.Logger
	serverOpts, err := frameworkServerOptions(cfg, logger, p.Clock)
	if err != nil {
		return nil, err
	}
	// App-supplied options (interceptors, custom server options) run after the
	// framework's.
	serverOpts = append(serverOpts, p.Options...)
	srv := grpc.NewServer(serverOpts...)
	hs := registerHealth(srv, cfg.Health, p.Registry)
	p.LC.Append(drain.Wrap(p.Gate, serverHook(srv, hs, cfg, logger)))
	return srv, nil
}

// frameworkServerOptions builds the framework's own server options: TLS,
// message-size limits, keepalive, and the OTel → logging → recovery chain.
func frameworkServerOptions(cfg Config, logger *slog.Logger, clk clock.Clock) ([]grpc.ServerOption, error) {
	if err := cfg.Keepalive.validate(); err != nil {
		return nil, err
	}
	var serverOpts []grpc.ServerOption
	if cfg.TLS {
		creds, err := serverCredentials(cfg, logger, clk)
		if err != nil {
			return nil, err
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	serverOpts = append(
		serverOpts,
		grpc.MaxRecvMsgSize(cfg.MaxRecvSize),
		grpc.MaxSendMsgSize(cfg.MaxSendSize),
		grpc.KeepaliveParams(cfg.Keepalive.serverParameters()),
		grpc.KeepaliveEnforcementPolicy(cfg.Keepalive.enforcementPolicy()),
	)

	// Interceptors: OTel → logging → recovery (outermost first).
	logAdapter := newLogAdapter(logger)
	serverOpts = append(
		serverOpts,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			grpclogging.UnaryServerInterceptor(logAdapter),
			grpcrecovery.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			grpclogging.StreamServerInterceptor(logAdapter),
			grpcrecovery.StreamServerInterceptor(),
		),
	)
	return serverOpts, nil
}

// newLogAdapter maps go-grpc-middleware log levels onto the slog logger.
func newLogAdapter(logger *slog.Logger) grpclogging.Logger {
	return grpclogging.LoggerFunc(func(ctx context.Context, lvl grpclogging.Level, msg string, fields ...any) {
		switch lvl {
		case grpclogging.LevelDebug:
			logger.DebugContext(ctx, msg, fields...)
		case grpclogging.LevelInfo:
			logger.InfoContext(ctx, msg, fields...)
		case grpclogging.LevelWarn:
			logger.WarnContext(ctx, msg, fields...)
		case grpclogging.LevelError:
			logger.ErrorContext(ctx, msg, fields...)
		default:
			logger.InfoContext(ctx, msg, fields...)
		}
	})
}

// serverHook binds listen/serve and the bounded graceful stop to fx lifecycle.
// A registered health service reports NOT_SERVING before the drain starts.
func serverHook(srv *grpc.Server, hs *grpchealth.Server, cfg Config, logger *slog.Logger) fx.Hook {
	return fx.Hook{
		OnStart: func(ctx context.Context) error {
			lc := &net.ListenConfig{}
			ln, err := lc.Listen(ctx, "tcp", cfg.Listen)
			if err != nil {
				return fmt.Errorf("grpc: listen %s: %w", cfg.Listen, err)
			}
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
					logger.ErrorContext(ctx, "grpc: serve error", "err", err)
				}
			}()
			logger.InfoContext(ctx, "grpc: serving", "addr", cfg.Listen)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if hs != nil {
				hs.Shutdown()
			}
			// Bound the graceful drain to the stop deadline, then hard-stop so
			// shutdown can't hang on a stuck in-flight RPC.
			done := make(chan struct{})
			go func() { srv.GracefulStop(); close(done) }()
			select {
			case <-done:
			case <-ctx.Done():
				srv.Stop()
				<-done
			}
			return nil
		},
	}
}

// clientParams are the fx inputs to newConnFactory.
type clientParams struct {
	fx.In
	Config Config
	Logger *slog.Logger
	Clock  clock.Clock `optional:"true"`
}

func newConnFactory(p clientParams) (*ConnFactory, error) {
	return newClientFactory(p.Config.Client, p.Logger, p.Clock)
}

// NewConnFactory returns a ConnFactory with OTel and insecure credentials.
// Override credentials with Dial(..., grpc.WithTransportCredentials(creds)),
// or build from config with [NewConnFactoryWithConfig].
func NewConnFactory() *ConnFactory {
	return &ConnFactory{
		dialOpts: []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}
}
