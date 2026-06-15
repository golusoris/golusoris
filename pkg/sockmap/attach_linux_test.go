//go:build linux

package sockmap

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// TestFullAttach loads the bundled CO-RE object and attaches both the SOCK_OPS
// program (to a freshly created child cgroup v2) and the SK_MSG program (to the
// pinned sockhash), then tears everything down cleanly. This exercises the
// real redirect-program attach path end-to-end — the part that is "scaffold
// only" without a BPF object. Skips without CAP_BPF / bpffs / writable cgroup
// v2.
func TestFullAttach(t *testing.T) {
	t.Parallel()
	requireBPF(t)
	cg := mkChildCgroup(t)

	dir := t.TempDir()
	pin := filepath.Join("/sys/fs/bpf", "golusoris-test-attach-"+filepath.Base(dir))
	t.Cleanup(func() { _ = os.Remove(pin) })

	s := &Sockmap{
		opts: Options{
			Enabled:        true,
			PinPath:        pin,
			MapName:        "gtest_attach",
			MaxEntries:     16,
			CgroupPath:     cg,
			SockOpsProg:    defaultSockOpsProg,
			SkMsgProg:      defaultSkMsgProg,
			MinKernelMajor: minKernelMajorDefault,
			MinKernelMinor: minKernelMinorDefault,
		},
		log:  slog.New(slog.DiscardHandler),
		prov: DefaultObjectProvider(),
		m:    newMetrics(prometheus.NewRegistry()),
	}

	ctx := context.Background()
	require.NoError(t, s.start(ctx))
	require.True(t, s.started)
	require.NotNil(t, s.sockOps, "SOCK_OPS link must be attached")
	require.True(t, s.skMsgAttd, "SK_MSG program must be attached")

	require.NoError(t, s.stop(ctx))
	require.False(t, s.started)
}

// mkChildCgroup creates a throwaway child cgroup v2 under the process's own
// cgroup and returns its path. Skips if the hierarchy is not v2 or not
// writable (delegated). Removed at test end.
func mkChildCgroup(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(cgroupV2Marker); err != nil {
		t.Skip("not a cgroup v2 unified hierarchy")
	}
	rel, err := selfCgroupV2()
	require.NoError(t, err)
	parent := cgroupV2Mount + rel
	child := filepath.Join(parent, "golusoris-test-"+filepath.Base(t.TempDir()))
	if err := os.Mkdir(child, 0o750); err != nil {
		t.Skipf("cannot create child cgroup (need delegated/writable cgroup v2): %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(child) })
	return child
}
