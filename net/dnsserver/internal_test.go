// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dnsserver

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

type lifecycleRecorder struct{ hook fx.Hook }

func (l *lifecycleRecorder) Append(hook fx.Hook) { l.hook = hook }

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q, want %q", c.Addr, defaultAddr)
	}
	if c.UDPSize != defaultUDPSize {
		t.Errorf("UDPSize = %d, want %d", c.UDPSize, defaultUDPSize)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	c := Config{Addr: ":5353", UDPSize: 512}.withDefaults()
	if c.Addr != ":5353" {
		t.Errorf("Addr = %q", c.Addr)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q", c.Addr)
	}
}

func TestNewServeMux(t *testing.T) {
	t.Parallel()
	if newServeMux() == nil {
		t.Error("newServeMux returned nil")
	}
}

func TestRegister_TCPBindFailureReturnsAndCleansUDP(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy TCP address: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	lifecycle := &lifecycleRecorder{}
	register(params{
		LC:     lifecycle,
		Cfg:    Config{Addr: occupied.Addr().String(), UDPSize: defaultUDPSize},
		Mux:    dns.NewServeMux(),
		Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	startErr := lifecycle.hook.OnStart(ctx)

	probe, probeErr := net.ListenPacket("udp", occupied.Addr().String())
	if probeErr == nil {
		_ = probe.Close()
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	_ = lifecycle.hook.OnStop(stopCtx)

	if startErr == nil || !strings.Contains(startErr.Error(), "tcp") {
		t.Fatalf("OnStart error = %v; want immediate TCP serve failure", startErr)
	}
	if probeErr != nil {
		t.Fatalf("UDP listener leaked after failed start: %v", probeErr)
	}
}
