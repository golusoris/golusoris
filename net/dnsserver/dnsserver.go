// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/miekg/dns"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
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
var Module = fx.Module(
	"golusoris.net.dnsserver",
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
		OnStart: func(ctx context.Context) error { return startServers(ctx, p.Logger, p.Cfg.Addr, udp, tcp) },
		OnStop:  func(ctx context.Context) error { return stopServers(ctx, p.Logger, udp, tcp) },
	})
}

type serveResult struct {
	network string
	err     error
}

func startServers(ctx context.Context, logger *slog.Logger, addr string, udp, tcp *dns.Server) error {
	packetConn, listener, err := bindServers(ctx, addr)
	if err != nil {
		return err
	}
	udp.PacketConn = packetConn
	tcp.Listener = listener

	udpReady := make(chan struct{})
	tcpReady := make(chan struct{})
	udp.NotifyStartedFunc = func() { close(udpReady) }
	tcp.NotifyStartedFunc = func() { close(tcpReady) }
	serveDone := make(chan serveResult, 2)
	serveCtx := context.WithoutCancel(ctx)
	go serve(logger, serveCtx, "udp", udp, serveDone)
	go serve(logger, serveCtx, "tcp", tcp, serveDone)

	for range 2 {
		select {
		case <-udpReady:
			udpReady = nil
		case <-tcpReady:
			tcpReady = nil
		case result := <-serveDone:
			return startupServeError(result, packetConn, listener)
		case <-ctx.Done():
			return errors.Join(
				fmt.Errorf("dnsserver: start: %w", ctx.Err()),
				closeBoundServers(packetConn, listener),
			)
		}
	}
	logger.InfoContext(ctx, "dnsserver: listening", "addr", addr)
	return nil
}

func bindServers(ctx context.Context, configured string) (net.PacketConn, net.Listener, error) {
	var listenConfig net.ListenConfig
	packetConn, err := listenConfig.ListenPacket(ctx, "udp", configured)
	if err != nil {
		return nil, nil, fmt.Errorf("dnsserver: udp listen: %w", err)
	}
	listener, err := listenConfig.Listen(ctx, "tcp", matchingTCPAddress(configured, packetConn.LocalAddr()))
	if err == nil {
		return packetConn, listener, nil
	}
	return nil, nil, errors.Join(
		fmt.Errorf("dnsserver: tcp listen: %w", err),
		wrapCloseError("udp startup listener", packetConn.Close()),
	)
}

func serve(logger *slog.Logger, ctx context.Context, network string, server *dns.Server, done chan<- serveResult) {
	err := server.ActivateAndServe()
	if err != nil {
		logger.ErrorContext(ctx, "dnsserver: serve", "network", network, "err", err)
	}
	done <- serveResult{network: network, err: err}
}

func startupServeError(result serveResult, packetConn net.PacketConn, listener net.Listener) error {
	if result.err == nil {
		result.err = errors.New("server stopped before readiness")
	}
	return errors.Join(
		fmt.Errorf("dnsserver: %s serve before startup: %w", result.network, result.err),
		closeBoundServers(packetConn, listener),
	)
}

func stopServers(ctx context.Context, logger *slog.Logger, udp, tcp *dns.Server) error {
	var shutdownErrs []error
	if err := udp.ShutdownContext(ctx); err != nil {
		logger.WarnContext(ctx, "dnsserver: udp shutdown", "err", err)
		shutdownErrs = append(shutdownErrs, fmt.Errorf("dnsserver: udp shutdown: %w", err))
	}
	if err := tcp.ShutdownContext(ctx); err != nil {
		logger.WarnContext(ctx, "dnsserver: tcp shutdown", "err", err)
		shutdownErrs = append(shutdownErrs, fmt.Errorf("dnsserver: tcp shutdown: %w", err))
	}
	return errors.Join(shutdownErrs...)
}

func matchingTCPAddress(configured string, udpAddr net.Addr) string {
	_, port, err := net.SplitHostPort(configured)
	if err == nil && port == "0" {
		return udpAddr.String()
	}
	return configured
}

func closeBoundServers(packetConn net.PacketConn, listener net.Listener) error {
	return errors.Join(
		wrapCloseError("udp startup listener", packetConn.Close()),
		wrapCloseError("tcp startup listener", listener.Close()),
	)
}

func wrapCloseError(resource string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dnsserver: close %s: %w", resource, err)
}
