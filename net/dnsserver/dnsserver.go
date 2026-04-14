// Package dnsserver provides an fx-wired DNS server using [miekg/dns].
//
// Apps register handlers via [ServeMux] (a [dns.ServeMux]) or implement
// [dns.Handler] directly.  The server listens on both UDP and TCP.
//
// Usage:
//
//	fx.New(
//	    dnsserver.Module,
//	    fx.Invoke(func(mux *dns.ServeMux) {
//	        mux.HandleFunc("example.com.", func(w dns.ResponseWriter, r *dns.Msg) {
//	            m := new(dns.Msg).SetReply(r)
//	            m.Answer = append(m.Answer, &dns.A{
//	                Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
//	                A:   net.ParseIP("1.2.3.4"),
//	            })
//	            _ = w.WriteMsg(m)
//	        })
//	    }),
//	)
//
// Config keys (env: APP_DNS_*):
//
//	dns.addr      # listen address (default: :5353)
//	dns.udp_size  # max UDP message size in bytes (default: 4096)
package dnsserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/miekg/dns"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

const (
	defaultAddr    = ":5353"
	defaultUDPSize = 4096
)

// Config holds DNS server configuration.
type Config struct {
	Addr    string `koanf:"addr"`
	UDPSize int    `koanf:"udp_size"`
}

// DefaultConfig returns a safe default configuration.
func DefaultConfig() Config {
	return Config{Addr: defaultAddr, UDPSize: defaultUDPSize}
}

func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = defaultAddr
	}
	if c.UDPSize <= 0 {
		c.UDPSize = defaultUDPSize
	}
	return c
}

// Module provides *dns.ServeMux into the fx graph and starts the server.
// Requires *config.Config and *slog.Logger.
var Module = fx.Module("golusoris.net.dnsserver",
	fx.Provide(loadConfig),
	fx.Provide(newServeMux),
	fx.Invoke(register),
)

type params struct {
	fx.In
	LC     fx.Lifecycle
	Cfg    Config
	Mux    *dns.ServeMux
	Logger *slog.Logger
}

func loadConfig(cfg *config.Config) (Config, error) {
	c := Config{}
	if err := cfg.Unmarshal("dns", &c); err != nil {
		return Config{}, fmt.Errorf("dnsserver: load config: %w", err)
	}
	return c.withDefaults(), nil
}

func newServeMux() *dns.ServeMux { return dns.NewServeMux() }

func register(p params) {
	udp := &dns.Server{Addr: p.Cfg.Addr, Net: "udp", Handler: p.Mux, UDPSize: p.Cfg.UDPSize}
	tcp := &dns.Server{Addr: p.Cfg.Addr, Net: "tcp", Handler: p.Mux}

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			udpReady := make(chan struct{})
			tcpReady := make(chan struct{})
			udp.NotifyStartedFunc = func() { close(udpReady) }
			tcp.NotifyStartedFunc = func() { close(tcpReady) }

			go func() {
				if err := udp.ListenAndServe(); err != nil {
					p.Logger.Error("dnsserver: udp serve", "err", err)
				}
			}()
			go func() {
				if err := tcp.ListenAndServe(); err != nil {
					p.Logger.Error("dnsserver: tcp serve", "err", err)
				}
			}()

			select {
			case <-udpReady:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-tcpReady:
			case <-ctx.Done():
				return ctx.Err()
			}
			p.Logger.Info("dnsserver: listening", "addr", p.Cfg.Addr)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			_ = udp.ShutdownContext(ctx)
			_ = tcp.ShutdownContext(ctx)
			return nil
		},
	})
}
