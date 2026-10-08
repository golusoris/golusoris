// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package server_test

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
	"github.com/golusoris/golusoris/httpx/server"
)

func tlsModuleOptions(t *testing.T, opts server.TLSOptions, extra ...fx.Option) []fx.Option {
	t.Helper()
	cfg, err := config.New(config.Options{EnvPrefix: "TESTHTTPTLS_"})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return append([]fx.Option{
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		}),
		server.Module,
		fx.Decorate(func(o server.Options) server.Options { o.Addr, o.TLS = addr, opts; return o }),
	}, extra...)
}

// TestModule_FileTLS serves HTTPS from http.tls.* through the fx module.
func TestModule_FileTLS(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	files := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1"))
	var srv *http.Server
	app := fxtest.New(t, append(
		tlsModuleOptions(t, server.TLSOptions{Cert: files.Cert, Key: files.Key}),
		fx.Populate(&srv),
	)...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.RequireStart()
	defer app.RequireStop()

	cli, err := tlsx.NewReloader(tlsx.Files{CA: files.CA}, tlsx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{TLSClientConfig: cli.ClientConfig("")}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+srv.Addr+"/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatalf("GET over file TLS: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.TLS == nil {
		t.Fatalf("status = %d, tls = %v", resp.StatusCode, resp.TLS != nil)
	}
}

// TestModule_FileTLSConflictsWithAutotls refuses two TLS sources.
func TestModule_FileTLSConflictsWithAutotls(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	files := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1"))
	autotls := fx.Provide(func() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS13} })
	app := fx.New(append(
		tlsModuleOptions(t, server.TLSOptions{Cert: files.Cert, Key: files.Key}, autotls),
		fx.Invoke(func(*http.Server) {}),
	)...)
	if err := app.Err(); err == nil || !strings.Contains(err.Error(), "conflicts with an autotls") {
		t.Fatalf("app.Err() = %v, want autotls conflict", err)
	}
}
