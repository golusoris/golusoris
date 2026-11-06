//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/golusoris/golusoris/core/clock"
)

const addrInUseMarker = "address already in use"

var errAppExited = errors.New("sizing: app exited before readiness")

// kubeconfigStub lets golusoris.K8s resolve a client outside a cluster; nothing dials it.
const kubeconfigStub = `apiVersion: v1
kind: Config
clusters:
- name: stub
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: stub
  context:
    cluster: stub
    user: stub
current-context: stub
users:
- name: stub
  user:
    token: stub
`

// lockedBuffer serialises the child's stdout and stderr copy goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

type runningApp struct {
	cmd    *exec.Cmd
	addr   string
	logs   *lockedBuffer
	exited chan struct{}
}

func buildApp(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("sizing: go toolchain not on PATH: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "minimal")
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("sizing: go build examples/minimal: %v\n%s", err, out)
	}
	return bin
}

// startApp launches the binary and retries on a fresh port when the child lost the freeAddr race.
func startApp(t *testing.T, clk clock.Clock, bin, dsn string) *runningApp {
	t.Helper()
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(kubeconfigStub), 0o600); err != nil {
		t.Fatalf("sizing: write kubeconfig: %v", err)
	}
	for attempt := 1; attempt <= maxStartAttempts; attempt++ {
		app := launchApp(t, bin, dir, kubeconfig, dsn)
		err := waitReady(t.Context(), clk, app, "http://"+app.addr+"/readyz")
		if err == nil {
			t.Cleanup(func() { stopApp(t, clk, app) })
			return app
		}
		stopApp(t, clk, app)
		if !retryable(err, app.logs.String()) || attempt == maxStartAttempts {
			t.Fatalf("sizing: attempt %d/%d: %v\n%s", attempt, maxStartAttempts, err, app.logs.String())
		}
		t.Logf("sizing: attempt %d/%d lost the port race on %s; retrying with a fresh port", attempt, maxStartAttempts, app.addr)
	}
	t.Fatal("sizing: start loop ended without a result")
	return nil
}

// retryable reports whether the child died because its port was taken.
func retryable(err error, logs string) bool {
	return errors.Is(err, errAppExited) && strings.Contains(logs, addrInUseMarker)
}

func launchApp(t *testing.T, bin, dir, kubeconfig, dsn string) *runningApp {
	t.Helper()
	addr := freeAddr(t)
	app := &runningApp{addr: addr, logs: &lockedBuffer{}, exited: make(chan struct{})}
	app.cmd = exec.Command(bin)
	// Hermetic env: no OTLP endpoint, no host kubeconfig, no drain wait at stop.
	app.cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"APP_HTTP_ADDR=" + addr,
		"APP_DB_DSN=" + dsn,
		"APP_K8S_KUBECONFIG=" + kubeconfig,
		"APP_HEALTH_DRAIN_DELAY=0",
	}
	app.cmd.Stdout, app.cmd.Stderr = app.logs, app.logs
	if err := app.cmd.Start(); err != nil {
		t.Fatalf("sizing: start app: %v", err)
	}
	go func() {
		_ = app.cmd.Wait()
		close(app.exited)
	}()
	return app
}

func stopApp(t *testing.T, clk clock.Clock, app *runningApp) {
	t.Helper()
	if err := app.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Logf("sizing: interrupt app: %v", err)
	}
	select {
	case <-app.exited:
	case <-clk.After(stopTimeout):
		if err := app.cmd.Process.Kill(); err != nil {
			t.Logf("sizing: kill app: %v", err)
		}
		<-app.exited
	}
}

// freeAddr reserves a loopback port and releases it; startApp retries if the child loses the race.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sizing: reserve port: %v", err)
	}
	defer func() {
		if cerr := ln.Close(); cerr != nil {
			t.Logf("sizing: close reservation: %v", cerr)
		}
	}()
	return ln.Addr().String()
}

// waitReady polls url until 200, the child exits, or readyTimeout passes.
func waitReady(parent context.Context, clk clock.Clock, app *runningApp, url string) error {
	ctx, cancel := context.WithTimeout(parent, readyTimeout)
	defer cancel()
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	defer client.CloseIdleConnections()
	var last error
	for range readyAttempts {
		if last = getOK(ctx, client, url); last == nil {
			return nil
		}
		select {
		case <-app.exited:
			return fmt.Errorf("%w: last probe: %w", errAppExited, last)
		case <-ctx.Done():
			return fmt.Errorf("sizing: %s not ready within %s: %w", url, readyTimeout, last)
		case <-clk.After(readyPoll):
		}
	}
	return fmt.Errorf("sizing: %s not ready after %d attempts: %w", url, readyAttempts, last)
}
