package torrent

import (
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options selects and tunes the torrent backend.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    torrent.Module, // provides torrent.Client
//	)
//
// Config keys live under the "torrent" prefix.
type Options struct {
	// Backend selects the torrent backend: "rtorrent", "qbittorrent" or
	// "transmission". Empty defaults to "transmission".
	Backend string `koanf:"backend"`
	// Timeout caps a single daemon request (HTTP/RPC round-trip). Applied to
	// every backend's HTTP client. 0 falls back to 30s.
	Timeout time.Duration `koanf:"timeout"`
	// RTorrent configures the rtorrent (XML-RPC) backend.
	RTorrent RTorrentOptions `koanf:"rtorrent"`
	// QBittorrent configures the qBittorrent (Web API v2) backend.
	QBittorrent QBittorrentOptions `koanf:"qbittorrent"`
	// Transmission configures the Transmission (RPC) backend.
	Transmission TransmissionOptions `koanf:"transmission"`
}

// RTorrentOptions configures the rtorrent backend.
type RTorrentOptions struct {
	// Addr is the XML-RPC endpoint, e.g. "https://host/RPC2" or
	// "http://host:5000/RPC2".
	Addr string `koanf:"addr"`
	// BasicUser/BasicPass set HTTP Basic auth (optional).
	BasicUser string `koanf:"basic_user"`
	BasicPass string `koanf:"basic_pass"`
	// InsecureSkipVerify disables TLS certificate verification (test only).
	InsecureSkipVerify bool `koanf:"insecure_skip_verify"`
}

// QBittorrentOptions configures the qBittorrent backend.
type QBittorrentOptions struct {
	// Host is the WebUI base URL, e.g. "http://localhost:8080".
	Host string `koanf:"host"`
	// Username/Password authenticate the WebUI session (cookie login).
	Username string `koanf:"username"`
	Password string `koanf:"password"`
	// InsecureSkipVerify disables TLS certificate verification (test only).
	InsecureSkipVerify bool `koanf:"insecure_skip_verify"`
}

// TransmissionOptions configures the Transmission backend.
type TransmissionOptions struct {
	// URL is the RPC endpoint, e.g. "http://user:pass@localhost:9091/transmission/rpc".
	// Credentials may be embedded in the URL userinfo.
	URL string `koanf:"url"`
	// InsecureSkipVerify disables TLS certificate verification (test only).
	InsecureSkipVerify bool `koanf:"insecure_skip_verify"`
}

// defaultTimeout is the per-request cap when Options.Timeout is unset.
const defaultTimeout = 30 * time.Second

func defaultOptions() Options {
	return Options{
		Backend: backendTransmission,
		Timeout: defaultTimeout,
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("torrent", &opts); err != nil {
		return Options{}, fmt.Errorf("torrent: load options: %w", err)
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	return opts, nil
}

const (
	backendRTorrent     = "rtorrent"
	backendQBittorrent  = "qbittorrent"
	backendTransmission = "transmission"
)

// newClient builds the configured backend. The qbittorrent backend registers a
// bounded OnStart login on lc; the others are stateless at construction.
func newClient(lc fx.Lifecycle, opts Options, logger *slog.Logger) (Client, error) {
	switch opts.Backend {
	case backendRTorrent:
		c, err := newRTorrentClient(opts, logger)
		if err != nil {
			return nil, fmt.Errorf("torrent: build rtorrent backend: %w", err)
		}
		logger.Debug(
			"torrent: started",
			slog.String("backend", backendRTorrent),
			slog.String("addr", opts.RTorrent.Addr),
		)
		return c, nil
	case backendQBittorrent:
		c, err := newQBittorrentClient(lc, opts, logger)
		if err != nil {
			return nil, fmt.Errorf("torrent: build qbittorrent backend: %w", err)
		}
		logger.Debug(
			"torrent: started",
			slog.String("backend", backendQBittorrent),
			slog.String("host", opts.QBittorrent.Host),
		)
		return c, nil
	case backendTransmission, "":
		c, err := newTransmissionClient(opts, logger)
		if err != nil {
			return nil, fmt.Errorf("torrent: build transmission backend: %w", err)
		}
		logger.Debug(
			"torrent: started",
			slog.String("backend", backendTransmission),
		)
		return c, nil
	default:
		return nil, fmt.Errorf("torrent: %w: %q", ErrUnsupportedBackend, opts.Backend)
	}
}

// Module provides torrent.Client to the fx graph.
var Module = fx.Module(
	"golusoris.torrent",
	fx.Provide(loadOptions),
	fx.Provide(newClient),
)
