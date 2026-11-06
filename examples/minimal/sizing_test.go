//go:build sizing

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/testutil/pg"
)

// Budget defaults mirror the Fargate task in deploy/pulumi/app.go (taskCPU
// "256" = 0.25 vCPU, taskMemory "512" MiB); scripts/ci/pulumi-sizing-budget.sh
// fails when they diverge. Keep both constants on one line each.
const (
	defaultMemoryBudgetMiB = 512
	defaultCPUBudget       = 0.25
)

const (
	defaultRPS      = 200
	defaultDuration = 30 * time.Second
	// loadWorkers caps in-flight requests (HISS-06).
	loadWorkers    = 32
	requestTimeout = 2 * time.Second
	buildTimeout   = 5 * time.Minute
	readyTimeout   = 60 * time.Second
	stopTimeout    = 20 * time.Second
	// clockTicks is USER_HZ, the unit of utime/stime in /proc/<pid>/stat; it is
	// 100 on every Linux ABI (man 5 proc), so no sysconf call is needed.
	clockTicks = 100
	// probePath is served by k8s/health.Mount and answers without a DB round trip for /livez.
	probePath = "/livez"
)

type sizingConfig struct {
	rps       int
	duration  time.Duration
	memoryMiB float64
	cpuCores  float64
}

type procUsage struct {
	peakRSSKiB int64
	cpuSeconds float64
}

type loadResult struct {
	sent   int64
	failed int64
	wall   time.Duration
}

//nolint:paralleltest // WHY: CPU and RSS are measured per process; a parallel test would skew them.
func TestSizing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sizing reads /proc/<pid>/status and /proc/<pid>/stat; Linux only (HISS-21)")
	}
	cfg := loadSizingConfig(t)
	clk := clockwork.NewRealClock()

	dsn := databaseDSN(t)
	bin := buildApp(t)
	app := startApp(t, bin, dsn)
	waitReady(t, app, "http://"+app.addr+"/readyz")

	before := readProc(t, app.cmd.Process.Pid)
	res := runLoad(t, clk, "http://"+app.addr+probePath, cfg)
	after := readProc(t, app.cmd.Process.Pid)

	cores := (after.cpuSeconds - before.cpuSeconds) / res.wall.Seconds()
	rssMiB := float64(after.peakRSSKiB) / 1024
	t.Logf("sizing: peak RSS %.1f MiB (budget %.0f), avg CPU %.3f vCPU over %s (budget %.3f), %d requests at %d rps, %d failed, GOARCH %s",
		rssMiB, cfg.memoryMiB, cores, res.wall.Round(time.Millisecond), cfg.cpuCores, res.sent, cfg.rps, res.failed, runtime.GOARCH)

	assertBudgets(t, cfg, rssMiB, cores, res)
}

// databaseDSN returns SIZING_DB_DSN when set (a host where container port mapping is
// unavailable), else a testutil/pg container.
func databaseDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("SIZING_DB_DSN"); dsn != "" {
		return dsn
	}
	return pg.Start(t).Config().ConnString()
}

func assertBudgets(t *testing.T, cfg sizingConfig, rssMiB, cores float64, res loadResult) {
	t.Helper()
	if rssMiB > cfg.memoryMiB {
		t.Errorf("peak RSS %.1f MiB exceeds budget %.0f MiB", rssMiB, cfg.memoryMiB)
	}
	if cores > cfg.cpuCores {
		t.Errorf("avg CPU %.3f vCPU exceeds budget %.3f vCPU", cores, cfg.cpuCores)
	}
	if res.failed != 0 {
		t.Errorf("error rate %d/%d, want 0", res.failed, res.sent)
	}
}

func loadSizingConfig(t *testing.T) sizingConfig {
	t.Helper()
	rps := envNumber(t, "SIZING_RPS", defaultRPS, strconv.ParseFloat)
	dur := envNumber(t, "SIZING_DURATION", defaultDuration.Seconds(), func(v string, _ int) (float64, error) {
		d, err := time.ParseDuration(v)
		return d.Seconds(), err
	})
	return sizingConfig{
		rps:       int(rps),
		duration:  time.Duration(dur * float64(time.Second)),
		memoryMiB: envNumber(t, "SIZING_MEMORY_BUDGET_MIB", defaultMemoryBudgetMiB, strconv.ParseFloat),
		cpuCores:  envNumber(t, "SIZING_CPU_BUDGET", defaultCPUBudget, strconv.ParseFloat),
	}
}

// envNumber reads a positive number from env name, or def when unset; parse
// receives the raw value (and 64 as the float bit size where it applies).
func envNumber(t *testing.T, name string, def float64, parse func(string, int) (float64, error)) float64 {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := parse(v, 64)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q: want a positive number", name, v)
	}
	return n
}

func buildApp(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("sizing: go toolchain not on PATH: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "minimal")
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("sizing: go build examples/minimal: %v\n%s", err, out)
	}
	return bin
}

type runningApp struct {
	cmd  *exec.Cmd
	addr string
	logs *bytes.Buffer
}

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

func startApp(t *testing.T, bin, dsn string) *runningApp {
	t.Helper()
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(kubeconfigStub), 0o600); err != nil {
		t.Fatalf("sizing: write kubeconfig: %v", err)
	}
	addr := freeAddr(t)
	app := &runningApp{addr: addr, logs: &bytes.Buffer{}}
	app.cmd = exec.Command(bin)
	// Hermetic env: no OTLP endpoint (otel degrades to a no-op), no host kubeconfig, no drain wait at stop.
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
	t.Cleanup(func() { stopApp(t, app) })
	return app
}

func stopApp(t *testing.T, app *runningApp) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- app.cmd.Wait() }()
	if err := app.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Logf("sizing: interrupt app: %v", err)
	}
	select {
	case <-done:
	case <-time.After(stopTimeout):
		if err := app.cmd.Process.Kill(); err != nil {
			t.Logf("sizing: kill app: %v", err)
		}
		<-done
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
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

// waitReady polls url until 200; on timeout it fails with the app output.
func waitReady(t *testing.T, app *runningApp, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	client := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	defer client.CloseIdleConnections()
	const attempts = 240
	var last error
	for range attempts {
		if last = getOK(ctx, client, url); last == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("sizing: %s not ready within %s: %v\n%s", url, readyTimeout, last, app.logs.String())
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.Fatalf("sizing: %s not ready after %d attempts: %v\n%s", url, attempts, last, app.logs.String())
}

var errStatus = errors.New("unexpected status")

// getOK issues one GET bounded by requestTimeout and requires a 200.
func getOK(parent context.Context, client *http.Client, url string) error {
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("sizing: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sizing: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("sizing: drain body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sizing: GET %s: %w %d", url, errStatus, resp.StatusCode)
	}
	return nil
}

// runLoad fires cfg.rps GETs per second for cfg.duration through loadWorkers workers.
func runLoad(t *testing.T, clk clockwork.Clock, url string, cfg sizingConfig) loadResult {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: loadWorkers}}
	defer client.CloseIdleConnections()
	ctx := context.Background()
	jobs := make(chan struct{}, loadWorkers)
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range loadWorkers {
		wg.Go(func() {
			for range jobs {
				if err := getOK(ctx, client, url); err != nil {
					failed.Add(1)
				}
			}
		})
	}

	total := int64(cfg.rps) * int64(cfg.duration/time.Second)
	if total == 0 {
		total = int64(cfg.rps)
	}
	ticker := clk.NewTicker(time.Second / time.Duration(cfg.rps))
	defer ticker.Stop()
	start := clk.Now()
	var sent int64
	for sent < total {
		<-ticker.Chan()
		select {
		case jobs <- struct{}{}:
			sent++
		default:
			failed.Add(1) // workers saturated: the app cannot keep the fixed rate
			sent++
		}
	}
	close(jobs)
	wg.Wait()
	return loadResult{sent: sent, failed: failed.Load(), wall: clk.Since(start)}
}

// readProc samples peak RSS (VmHWM) and utime+stime of pid.
func readProc(t *testing.T, pid int) procUsage {
	t.Helper()
	root := filepath.Join("/proc", strconv.Itoa(pid))
	status, err := os.ReadFile(filepath.Join(root, "status"))
	if err != nil {
		t.Fatalf("sizing: read status: %v", err)
	}
	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		t.Fatalf("sizing: read stat: %v", err)
	}
	hwm, err := parseVMHWM(string(status))
	if err != nil {
		t.Fatalf("sizing: %v", err)
	}
	ticks, err := parseCPUTicks(string(stat))
	if err != nil {
		t.Fatalf("sizing: %v", err)
	}
	return procUsage{peakRSSKiB: hwm, cpuSeconds: float64(ticks) / clockTicks}
}

func parseVMHWM(status string) (int64, error) {
	for line := range strings.SplitSeq(status, "\n") {
		rest, ok := strings.CutPrefix(line, "VmHWM:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		kib, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmHWM %q: %w", rest, err)
		}
		return kib, nil
	}
	return 0, errors.New("VmHWM not found in /proc status")
}

// parseCPUTicks returns utime+stime (fields 14 and 15) of a /proc/<pid>/stat line.
func parseCPUTicks(stat string) (int64, error) {
	// comm (field 2) may contain spaces; fields resume after its closing parenthesis.
	idx := strings.LastIndexByte(stat, ')')
	if idx < 0 {
		return 0, errors.New("malformed /proc stat: no comm terminator")
	}
	fields := strings.Fields(stat[idx+1:])
	const utimeIdx, stimeIdx = 11, 12 // state is field 3 = index 0
	if len(fields) <= stimeIdx {
		return 0, fmt.Errorf("malformed /proc stat: %d fields after comm", len(fields))
	}
	utime, err := strconv.ParseInt(fields[utimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse utime: %w", err)
	}
	stime, err := strconv.ParseInt(fields[stimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse stime: %w", err)
	}
	return utime + stime, nil
}
