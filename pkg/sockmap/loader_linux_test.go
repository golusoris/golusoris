//go:build linux

package sockmap

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestRequireCgroupV2(t *testing.T) {
	t.Parallel()
	err := requireCgroupV2()
	if _, statErr := os.Stat(cgroupV2Marker); statErr != nil {
		require.Error(t, err) // cgroup v1 / hybrid → loud failure
		return
	}
	require.NoError(t, err)
}

func TestSelfCgroupV2(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(cgroupV2Marker); err != nil {
		t.Skip("not a cgroup v2 unified hierarchy")
	}
	rel, err := selfCgroupV2()
	require.NoError(t, err)
	require.True(t, len(rel) > 0 && rel[0] == '/', "cgroup path must be absolute, got %q", rel)
}

func TestResolveCgroupPath_BadOverride(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(cgroupV2Marker); err != nil {
		t.Skip("not a cgroup v2 unified hierarchy")
	}
	_, err := resolveCgroupPath("/sys/fs/cgroup/does-not-exist-golusoris")
	require.Error(t, err)
}

func TestUnameRelease(t *testing.T) {
	t.Parallel()
	rel := unameRelease()
	require.NotEmpty(t, rel)
	_, _, err := parseKernelVersion(rel)
	require.NoError(t, err)
}

// TestListenerFromFD wraps a dup'd listener FD, the way socket activation hands
// a listen FD to the process. listenerFromFD owns the FD it is given.
func TestListenerFromFD(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	f, err := ln.(*net.TCPListener).File()
	require.NoError(t, err)
	dup, err := syscall.Dup(int(f.Fd()))
	require.NoError(t, f.Close())
	require.NoError(t, err)

	got, err := listenerFromFD(dup, "test-fd")
	require.NoError(t, err)
	require.NotNil(t, got)
	t.Cleanup(func() { _ = got.Close() })
	require.Equal(t, ln.Addr().String(), got.Addr().String())
}

// TestSockhashLifecycle is the privileged integration slice: create + pin a
// real SockHash, insert a live *established* TCP connection FD, verify the
// kernel map holds the entry, then run the pre-shutdown cleanup and assert the
// entry is gone before close. Skips without CAP_BPF / bpffs — mirroring the
// testcontainers SkipIfProviderIsNotHealthy pattern.
//
// Note: the kernel rejects inserting a *listening* socket from userspace
// (EOPNOTSUPP) — the listen socket is meant to be populated by the SOCK_OPS
// program from kernel context. We therefore exercise an established conn here.
func TestSockhashLifecycle(t *testing.T) {
	t.Parallel()
	requireBPF(t)

	dir := t.TempDir()
	// The pin must live on bpffs; t.TempDir() is not bpffs, so pin under the
	// real mount and clean up. Use a unique name to avoid cross-test clashes.
	pin := filepath.Join("/sys/fs/bpf", "golusoris-test-"+filepath.Base(dir))
	t.Cleanup(func() { _ = os.Remove(pin) })

	s := &Sockmap{
		opts: Options{
			Enabled:        true,
			PinPath:        pin,
			MapName:        "gtest_sockhash",
			MaxEntries:     16,
			MinKernelMajor: minKernelMajorDefault,
			MinKernelMinor: minKernelMinorDefault,
		},
		log: slog.New(slog.DiscardHandler),
		m:   newMetrics(prometheus.NewRegistry()),
	}

	require.NoError(t, s.loadSockhash(nil))
	t.Cleanup(func() { s.mu.Lock(); s.teardownLocked(); s.mu.Unlock() })

	conn := mustDialEstablished(t)
	s.mu.Lock()
	err := s.insertLocked(conn)
	s.mu.Unlock()
	require.NoError(t, err)
	require.Len(t, s.keys, 1)
	require.Equal(t, 1.0, testutilGauge(t, s.m.ActiveSockets))

	// Verify the key is present in the kernel map. Sockmaps don't support
	// reading the value (the kernel struct sock* can't be copied to
	// userspace — value lookup returns ENOSPC), so we iterate keys instead.
	require.Equal(t, 1, sockhashKeyCount(t, s.sockhash))

	// Pre-shutdown cleanup removes the entry before the socket closes.
	require.NoError(t, s.stop(context.Background()))
	require.Empty(t, s.keys)
	require.Equal(t, 0.0, testutilGauge(t, s.m.ActiveSockets))
}

// TestStartStop_NoProvider drives the full Sockmap.start/stop lifecycle with no
// ObjectProvider: the sockhash is created, pinned, and torn down, but no BPF
// program is attached (the "scaffold" mode). It also inserts an established
// conn registered before Start to exercise the pending-insert path.
func TestStartStop_NoProvider(t *testing.T) {
	t.Parallel()
	requireBPF(t)
	if _, err := os.Stat(cgroupV2Marker); err != nil {
		t.Skip("not a cgroup v2 unified hierarchy")
	}

	dir := t.TempDir()
	pin := filepath.Join("/sys/fs/bpf", "golusoris-test-start-"+filepath.Base(dir))
	t.Cleanup(func() { _ = os.Remove(pin) })

	s := &Sockmap{
		opts: Options{
			Enabled:        true,
			PinPath:        pin,
			MapName:        "gtest_start",
			MaxEntries:     16,
			MinKernelMajor: minKernelMajorDefault,
			MinKernelMinor: minKernelMinorDefault,
		},
		log: slog.New(slog.DiscardHandler),
		m:   newMetrics(prometheus.NewRegistry()),
	}

	// Registered before Start → inserted during start's pending loop.
	require.NoError(t, s.registerConn(mustDialEstablished(t)))

	ctx := context.Background()
	require.NoError(t, s.start(ctx))
	require.True(t, s.started)
	require.Len(t, s.keys, 1)
	require.Equal(t, 1.0, testutilGauge(t, s.m.ActiveSockets))

	// Post-Start registration inserts immediately.
	require.NoError(t, s.registerConn(mustDialEstablished(t)))
	require.Len(t, s.keys, 2)

	require.NoError(t, s.stop(ctx))
	require.False(t, s.started)
	require.Empty(t, s.keys)
	require.Equal(t, 0.0, testutilGauge(t, s.m.ActiveSockets))
}

// TestStart_KernelGuard rejects an impossibly high kernel floor, proving the
// version guard fires before any kernel resource is touched.
func TestStart_KernelGuard(t *testing.T) {
	t.Parallel()
	s := &Sockmap{
		opts: Options{Enabled: true, MinKernelMajor: 99, MinKernelMinor: 0},
		log:  slog.New(slog.DiscardHandler),
		m:    newMetrics(prometheus.NewRegistry()),
	}
	require.Error(t, s.start(context.Background()))
}

// requireBPF skips unless the test can load eBPF maps (privileged + bpffs).
// Mirrors the testcontainers SkipIfProviderIsNotHealthy pattern: the test is
// inert without the capability rather than failing.
func requireBPF(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/sys/fs/bpf"); err != nil {
		t.Skip("bpffs not mounted at /sys/fs/bpf")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Skipf("cannot remove memlock rlimit (need CAP_SYS_RESOURCE/CAP_BPF): %v", err)
	}
	// Probe by creating a throwaway hash map; any error → skip (unprivileged).
	probe, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.Hash, KeySize: 4, ValueSize: 4, MaxEntries: 1})
	if err != nil {
		t.Skipf("eBPF unavailable (need CAP_BPF/CAP_SYS_ADMIN): %v", err)
	}
	_ = probe.Close()
}

// mustDialEstablished returns a live, established loopback TCP connection (the
// client end). The accepted server end is kept open until test end so the
// socket stays in TCP_ESTABLISHED — the only state the kernel lets userspace
// insert into a sockhash.
func mustDialEstablished(t *testing.T) *net.TCPConn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			accepted <- c
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	srv := <-accepted
	t.Cleanup(func() { _ = srv.Close() })

	tcp, ok := conn.(*net.TCPConn)
	require.True(t, ok)
	return tcp
}

// testutilGauge reads the current value of a prometheus.Gauge.
func testutilGauge(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, g.Write(&m))
	return m.GetGauge().GetValue()
}

// sockhashKeyCount walks the map keys (sockmap values are unreadable from
// userspace) and returns how many entries are present.
func sockhashKeyCount(t *testing.T, m *ebpf.Map) int {
	t.Helper()
	var cur, next sockKey
	if err := m.NextKey(nil, &next); err != nil {
		return 0
	}
	count := 1
	for {
		cur = next
		if err := m.NextKey(&cur, &next); err != nil {
			return count
		}
		count++
	}
}
