// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dnsserver

import (
	"context"
	"errors"
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

// TCP binds first because TIME_WAIT TCP ports refuse a listener and a UDP-chosen port may be one.
func pickTCPFirst(
	listenTCP func(addr string) (net.Listener, error),
	listenUDP func(addr string) (net.PacketConn, error),
) (net.Listener, string, error) {
	ln, err := listenTCP("127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	addr := ln.Addr().String()
	probe, err := listenUDP(addr)
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	_ = probe.Close()
	return ln, addr, nil
}

// UDP binds first because Windows can reserve a UDP range over the consecutive TCP ports it hands out (#969).
func pickUDPFirst(
	listenTCP func(addr string) (net.Listener, error),
	listenUDP func(addr string) (net.PacketConn, error),
) (net.Listener, string, error) {
	packet, err := listenUDP("127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	addr := packet.LocalAddr().String()
	_ = packet.Close()

	ln, err := listenTCP(addr)
	if err != nil {
		return nil, "", err
	}
	probe, err := listenUDP(addr)
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	_ = probe.Close()
	return ln, addr, nil
}

// pickTCPHeldUDPFree alternates both orders so neither failure mode exhausts the attempts.
func pickTCPHeldUDPFree(
	attempts int,
	listenTCP func(addr string) (net.Listener, error),
	listenUDP func(addr string) (net.PacketConn, error),
) (net.Listener, string, error) {
	if attempts <= 0 {
		return nil, "", errors.New("dnsserver: pickTCPHeldUDPFree: attempts must be positive")
	}
	var errs []error
	for i := range attempts {
		var (
			ln   net.Listener
			addr string
			err  error
		)
		if i%2 == 0 {
			ln, addr, err = pickTCPFirst(listenTCP, listenUDP)
		} else {
			ln, addr, err = pickUDPFirst(listenTCP, listenUDP)
		}
		if err == nil {
			return ln, addr, nil
		}
		errs = append(errs, err)
	}
	return nil, "", errors.Join(errs...)
}

func occupyTCPWithFreeUDP(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, addr, err := pickTCPHeldUDPFree(
		20,
		func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) },
		func(addr string) (net.PacketConn, error) { return net.ListenPacket("udp", addr) },
	)
	if err != nil {
		t.Fatalf("no port with TCP held and UDP free within 20 attempts: %v", err)
	}
	return ln, addr
}

func TestRegister_TCPBindFailureReturnsAndCleansUDP(t *testing.T) {
	t.Parallel()
	occupied, addr := occupyTCPWithFreeUDP(t)
	t.Cleanup(func() { _ = occupied.Close() })

	lifecycle := &lifecycleRecorder{}
	register(params{
		LC:     lifecycle,
		Cfg:    Config{Addr: addr, UDPSize: defaultUDPSize},
		Mux:    dns.NewServeMux(),
		Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	startErr := lifecycle.hook.OnStart(ctx)

	probe, probeErr := net.ListenPacket("udp", addr)
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

type fakeAddr string

func (a fakeAddr) Network() string { return "test" }
func (a fakeAddr) String() string  { return string(a) }

type fakeListener struct {
	addr   string
	closed bool
}

func (f *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (f *fakeListener) Close() error              { f.closed = true; return nil }
func (f *fakeListener) Addr() net.Addr            { return fakeAddr(f.addr) }

type fakePacketConn struct {
	addr   string
	closed bool
}

func (f *fakePacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }
func (f *fakePacketConn) WriteTo([]byte, net.Addr) (int, error)  { return 0, net.ErrClosed }
func (f *fakePacketConn) Close() error                           { f.closed = true; return nil }
func (f *fakePacketConn) LocalAddr() net.Addr                    { return fakeAddr(f.addr) }
func (f *fakePacketConn) SetDeadline(time.Time) error            { return nil }
func (f *fakePacketConn) SetReadDeadline(time.Time) error        { return nil }
func (f *fakePacketConn) SetWriteDeadline(time.Time) error       { return nil }

func TestPickTCPHeldUDPFree_WindowsRange(t *testing.T) {
	t.Parallel()
	var (
		tcpListeners []*fakeListener
		udpConns     []*fakePacketConn
		udpEphemeral int
	)
	listenTCP := func(addr string) (net.Listener, error) {
		if addr == "127.0.0.1:0" {
			addr = "127.0.0.1:10001"
		}
		ln := &fakeListener{addr: addr}
		tcpListeners = append(tcpListeners, ln)
		return ln, nil
	}
	listenUDP := func(addr string) (net.PacketConn, error) {
		if addr == "127.0.0.1:0" {
			udpEphemeral++
			conn := &fakePacketConn{addr: "127.0.0.1:20002"}
			udpConns = append(udpConns, conn)
			return conn, nil
		}
		if addr == "127.0.0.1:10001" {
			return nil, errors.New("access permissions (UDP excluded range)")
		}
		conn := &fakePacketConn{addr: addr}
		udpConns = append(udpConns, conn)
		return conn, nil
	}

	ln, addr, err := pickTCPHeldUDPFree(2, listenTCP, listenUDP)
	if err != nil {
		t.Fatalf("pickTCPHeldUDPFree failed: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if addr != "127.0.0.1:20002" {
		t.Fatalf("addr = %q, want %q", addr, "127.0.0.1:20002")
	}
	if udpEphemeral != 1 {
		t.Errorf("udpEphemeral = %d, want 1", udpEphemeral)
	}
	if len(tcpListeners) != 2 {
		t.Fatalf("len(tcpListeners) = %d, want 2", len(tcpListeners))
	}
	if !tcpListeners[0].closed {
		t.Error("attempt 1 TCP listener leaked")
	}
	if tcpListeners[1].closed {
		t.Error("successful listener was closed prematurely")
	}
	for i, conn := range udpConns {
		if !conn.closed {
			t.Errorf("UDP conn %d leaked", i)
		}
	}
}

func TestPickTCPHeldUDPFree_Normal(t *testing.T) {
	t.Parallel()
	var (
		tcpListeners []*fakeListener
		udpConns     []*fakePacketConn
		udpEphemeral int
	)
	listenTCP := func(addr string) (net.Listener, error) {
		if addr == "127.0.0.1:0" {
			addr = "127.0.0.1:10001"
		}
		ln := &fakeListener{addr: addr}
		tcpListeners = append(tcpListeners, ln)
		return ln, nil
	}
	listenUDP := func(addr string) (net.PacketConn, error) {
		if addr == "127.0.0.1:0" {
			udpEphemeral++
		}
		conn := &fakePacketConn{addr: addr}
		udpConns = append(udpConns, conn)
		return conn, nil
	}

	ln, addr, err := pickTCPHeldUDPFree(2, listenTCP, listenUDP)
	if err != nil {
		t.Fatalf("pickTCPHeldUDPFree failed: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if addr != "127.0.0.1:10001" {
		t.Fatalf("addr = %q, want %q", addr, "127.0.0.1:10001")
	}
	if udpEphemeral != 0 {
		t.Errorf("udpEphemeral = %d, want 0", udpEphemeral)
	}
	if len(tcpListeners) != 1 {
		t.Fatalf("len(tcpListeners) = %d, want 1", len(tcpListeners))
	}
	if tcpListeners[0].closed {
		t.Error("successful listener was closed prematurely")
	}
	if len(udpConns) != 1 || !udpConns[0].closed {
		t.Error("UDP probe was not closed")
	}
}

func TestPickTCPHeldUDPFree_AllBindsFail(t *testing.T) {
	t.Parallel()
	errTCP := errors.New("tcp bind refused")
	errUDP := errors.New("udp bind refused")

	listenTCP := func(string) (net.Listener, error) { return nil, errTCP }
	listenUDP := func(string) (net.PacketConn, error) { return nil, errUDP }

	ln, addr, err := pickTCPHeldUDPFree(2, listenTCP, listenUDP)
	if ln != nil {
		t.Error("expected nil listener on failure")
		_ = ln.Close()
	}
	if addr != "" {
		t.Errorf("addr = %q, want empty", addr)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tcp bind refused") {
		t.Errorf("error %q does not contain tcp bind error", err)
	}
	if !strings.Contains(err.Error(), "udp bind refused") {
		t.Errorf("error %q does not contain udp bind error", err)
	}
}

func TestPickTCPHeldUDPFree_BoundaryAttemptsOne(t *testing.T) {
	t.Parallel()
	var (
		tcpListeners []*fakeListener
		udpEphemeral int
	)
	listenTCP := func(addr string) (net.Listener, error) {
		if addr == "127.0.0.1:0" {
			addr = "127.0.0.1:10001"
		}
		ln := &fakeListener{addr: addr}
		tcpListeners = append(tcpListeners, ln)
		return ln, nil
	}
	listenUDP := func(addr string) (net.PacketConn, error) {
		if addr == "127.0.0.1:0" {
			udpEphemeral++
			return &fakePacketConn{addr: "127.0.0.1:20002"}, nil
		}
		return nil, errors.New("udp probe refused")
	}

	ln, addr, err := pickTCPHeldUDPFree(1, listenTCP, listenUDP)
	if ln != nil {
		t.Error("expected nil listener")
		_ = ln.Close()
	}
	if addr != "" {
		t.Errorf("addr = %q, want empty", addr)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if udpEphemeral != 0 {
		t.Errorf("odd path was reached: udpEphemeral = %d, want 0", udpEphemeral)
	}
	if len(tcpListeners) != 1 || !tcpListeners[0].closed {
		t.Error("TCP listener opened on failed attempt was not closed")
	}
}

func TestPickTCPHeldUDPFree_BoundaryAttemptsZero(t *testing.T) {
	t.Parallel()
	var tcpCalls, udpCalls int
	listenTCP := func(string) (net.Listener, error) {
		tcpCalls++
		return nil, errors.New("unexpected tcp call")
	}
	listenUDP := func(string) (net.PacketConn, error) {
		udpCalls++
		return nil, errors.New("unexpected udp call")
	}

	ln, addr, err := pickTCPHeldUDPFree(0, listenTCP, listenUDP)
	if ln != nil {
		t.Error("expected nil listener")
		_ = ln.Close()
	}
	if addr != "" {
		t.Errorf("addr = %q, want empty", addr)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if tcpCalls != 0 || udpCalls != 0 {
		t.Errorf("bind called: tcp=%d, udp=%d, want 0", tcpCalls, udpCalls)
	}
}
