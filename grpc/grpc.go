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
// Config keys (env: APP_GRPC_*):
//
//	grpc.listen         # server bind address (default: :9090)
//	grpc.tls            # enable server TLS (default: false)
//	grpc.cert_file      # TLS cert path
//	grpc.key_file       # TLS key path
//	grpc.max_recv_size  # max incoming message size in bytes (default: 4 MiB)
//	grpc.max_send_size  # max outgoing message size in bytes (default: 4 MiB)
package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"time"

	grpclogging "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/golusoris/golusoris/config"
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
}

// DefaultConfig returns the opinionated default server config.
func DefaultConfig() Config {
	return Config{
		Listen:      defaultListen,
		MaxRecvSize: defaultMaxMsgBytes,
		MaxSendSize: defaultMaxMsgBytes,
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
	return c
}

// ConnFactory creates client connections with OTel instrumentation.
type ConnFactory struct {
	dialOpts []grpc.DialOption
}

// Dial opens a gRPC connection to target.
// The connection inherits OTel trace propagation automatically.
func (f *ConnFactory) Dial(ctx context.Context, target string, extra ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts := append(f.dialOpts, extra...) //nolint:gocritic // appendAssign: safe — f.dialOpts not reused
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc: dial %s: %w", target, err)
	}
	_ = ctx
	return conn, nil
}

// Module provides *grpc.Server + *ConnFactory into the fx graph.
// Requires *config.Config and *slog.Logger.
var Module = fx.Module("golusoris.grpc",
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

func newServer(lc fx.Lifecycle, cfg Config, logger *slog.Logger) (*grpc.Server, error) {
	var serverOpts []grpc.ServerOption

	// TLS
	if cfg.TLS {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("grpc: load tls cert: %w", err)
		}
		serverOpts = append(serverOpts, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		})))
	}

	// Message size limits.
	serverOpts = append(serverOpts,
		grpc.MaxRecvMsgSize(cfg.MaxRecvSize),
		grpc.MaxSendMsgSize(cfg.MaxSendSize),
	)

	// Keepalive — reasonable defaults for internal services.
	serverOpts = append(serverOpts, grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionAge:      2 * time.Minute,
		MaxConnectionAgeGrace: 5 * time.Second,
		Time:                  1 * time.Minute,
		Timeout:               20 * time.Second,
	}))

	// Interceptors: OTel → logging → recovery (outermost first).
	logAdapter := grpclogging.LoggerFunc(func(ctx context.Context, lvl grpclogging.Level, msg string, fields ...any) {
		switch lvl {
		case grpclogging.LevelDebug:
			logger.Debug(msg, fields...)
		case grpclogging.LevelWarn:
			logger.Warn(msg, fields...)
		case grpclogging.LevelError:
			logger.Error(msg, fields...)
		default:
			logger.Info(msg, fields...)
		}
	})

	serverOpts = append(serverOpts,
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

	srv := grpc.NewServer(serverOpts...)

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", cfg.Listen)
			if err != nil {
				return fmt.Errorf("grpc: listen %s: %w", cfg.Listen, err)
			}
			go func() {
				if err := srv.Serve(ln); err != nil && err != grpc.ErrServerStopped {
					logger.Error("grpc: serve error", "err", err)
				}
			}()
			logger.Info("grpc: serving", "addr", cfg.Listen)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			srv.GracefulStop()
			return nil
		},
	})
	return srv, nil
}

func newConnFactory() *ConnFactory { return NewConnFactory() }

// NewConnFactory returns a ConnFactory with OTel and insecure credentials.
// Override credentials with Dial(..., grpc.WithTransportCredentials(creds)).
func NewConnFactory() *ConnFactory {
	return &ConnFactory{
		dialOpts: []grpc.DialOption{
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}
}
