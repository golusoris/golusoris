package sockmap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/config"
)

// loadCfg builds a watch-disabled *config.Config from a YAML body.
func loadCfg(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := config.New(config.Options{Files: []string{path}, Watch: false})
	require.NoError(t, err)
	return cfg
}

func TestParseKernelVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		release   string
		wantMajor int
		wantMinor int
		wantErr   bool
	}{
		{name: "arch", release: "6.7.0-arch1-1", wantMajor: 6, wantMinor: 7},
		{name: "debian", release: "5.10.0-21-amd64", wantMajor: 5, wantMinor: 10},
		{name: "plus suffix", release: "5.15.0+", wantMajor: 5, wantMinor: 15},
		{name: "two-part", release: "6.1", wantMajor: 6, wantMinor: 1},
		{name: "empty", release: "", wantErr: true},
		{name: "single-part", release: "6", wantErr: true},
		{name: "bad major", release: "x.1", wantErr: true},
		{name: "bad minor", release: "6.x", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			major, minor, err := parseKernelVersion(tc.release)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantMajor, major)
			require.Equal(t, tc.wantMinor, minor)
		})
	}
}

func TestKernelAtLeast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                                       string
		haveMajor, haveMinor, wantMajor, wantMinor int
		want                                       bool
	}{
		{name: "equal", haveMajor: 5, haveMinor: 10, wantMajor: 5, wantMinor: 10, want: true},
		{name: "higher minor", haveMajor: 5, haveMinor: 15, wantMajor: 5, wantMinor: 10, want: true},
		{name: "higher major", haveMajor: 6, haveMinor: 0, wantMajor: 5, wantMinor: 10, want: true},
		{name: "lower minor", haveMajor: 5, haveMinor: 4, wantMajor: 5, wantMinor: 10, want: false},
		{name: "lower major", haveMajor: 4, haveMinor: 19, wantMajor: 5, wantMinor: 10, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, kernelAtLeast(tc.haveMajor, tc.haveMinor, tc.wantMajor, tc.wantMinor))
		})
	}
}

func TestCheckKernelRelease(t *testing.T) {
	t.Parallel()
	require.NoError(t, checkKernelRelease("6.7.0-arch1-1", 5, 10))
	require.Error(t, checkKernelRelease("5.4.0-generic", 5, 10))
	require.Error(t, checkKernelRelease("garbage", 5, 10))
}

func TestActivationNames(t *testing.T) {
	tests := []struct {
		name string
		env  string
		n    int
		want []string
	}{
		{name: "unset", env: "", n: 2, want: nil},
		{name: "two", env: "api:admin", n: 2, want: []string{"api", "admin"}},
		{name: "fewer-than-n", env: "api", n: 2, want: []string{"api"}},
		{name: "more-than-n-truncates", env: "a:b:c", n: 2, want: []string{"a", "b"}},
		{name: "empty-segment", env: "api::admin", n: 3, want: []string{"api", "", "admin"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LISTEN_FDNAMES", tc.env)
			require.Equal(t, tc.want, activationNames(tc.n))
		})
	}
}

func TestFdName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "api", fdName([]string{"api", "admin"}, 0, 3))
	require.Equal(t, "admin", fdName([]string{"api", "admin"}, 1, 4))
	require.Equal(t, "fd-5", fdName(nil, 0, 5))
	require.Equal(t, "fd-6", fdName([]string{""}, 0, 6))
}

func TestLoadOptions_Defaults(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	require.False(t, opts.Enabled)
	require.Equal(t, defaultPinPath, opts.PinPath)
	require.Equal(t, defaultMapName, opts.MapName)
	require.Equal(t, uint32(defaultMaxEntries), opts.MaxEntries)
	require.Equal(t, minKernelMajorDefault, opts.MinKernelMajor)
	require.Equal(t, minKernelMinorDefault, opts.MinKernelMinor)
}

func TestLoadOptions_CustomConfig(t *testing.T) {
	t.Parallel()
	cfg := loadCfg(t, "sockmap:\n  enabled: true\n  pin_path: /sys/fs/bpf/custom/sh\n  map_name: custom_sh\n  max_entries: 64\n  cgroup_path: /sys/fs/cgroup/app.slice\n  min_kernel_major: 6\n  min_kernel_minor: 1\n")
	opts, err := loadOptions(cfg)
	require.NoError(t, err)
	require.True(t, opts.Enabled)
	require.Equal(t, "/sys/fs/bpf/custom/sh", opts.PinPath)
	require.Equal(t, "custom_sh", opts.MapName)
	require.Equal(t, uint32(64), opts.MaxEntries)
	require.Equal(t, "/sys/fs/cgroup/app.slice", opts.CgroupPath)
	require.Equal(t, 6, opts.MinKernelMajor)
	require.Equal(t, 1, opts.MinKernelMinor)
}

func TestLoadOptions_PartialConfigRestoresDefaults(t *testing.T) {
	t.Parallel()
	// Only flips enabled; every other field must fall back to its default
	// rather than a useless zero value.
	cfg := loadCfg(t, "sockmap:\n  enabled: true\n")
	opts, err := loadOptions(cfg)
	require.NoError(t, err)
	require.True(t, opts.Enabled)
	require.Equal(t, defaultPinPath, opts.PinPath)
	require.Equal(t, defaultMapName, opts.MapName)
	require.Equal(t, uint32(defaultMaxEntries), opts.MaxEntries)
	require.Equal(t, defaultSockOpsProg, opts.SockOpsProg)
	require.Equal(t, defaultSkMsgProg, opts.SkMsgProg)
}

func TestProvideMetrics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m := provideMetrics(metricsParams{Registry: reg})
	require.NotNil(t, m)
	m.RedirectedBytes.Add(7)
	mfs, err := reg.Gather()
	require.NoError(t, err)
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "golusoris_sockmap_redirected_bytes_total" {
			found = true
			require.InEpsilon(t, 7.0, mf.GetMetric()[0].GetCounter().GetValue(), 1e-9)
		}
	}
	require.True(t, found)
}

//nolint:paralleltest // touches the global prometheus default registerer; must not run in parallel
func TestProvideMetrics_NilRegistryUsesDefault(t *testing.T) {
	m := provideMetrics(metricsParams{Registry: nil})
	require.NotNil(t, m.ActiveSockets)
}

func TestActivationCount(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    int
		wantErr bool
	}{
		{name: "unset", env: "", want: 0},
		{name: "two", env: "2", want: 2},
		{name: "bad", env: "x", wantErr: true},
		{name: "negative", env: "-1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LISTEN_FDS", tc.env)
			got, err := activationCount()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewMetrics_RegistersOnce(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m := newMetrics(reg)
	require.NotNil(t, m.RedirectedBytes)
	require.NotNil(t, m.ActiveSockets)
	require.NotNil(t, m.RedirectErrors)

	// Re-registering on the same registry must not panic (duplicate-tolerant).
	require.NotPanics(t, func() { _ = newMetrics(reg) })

	mfs, err := reg.Gather()
	require.NoError(t, err)
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	require.True(t, names["golusoris_sockmap_redirected_bytes_total"])
	require.True(t, names["golusoris_sockmap_active_sockets"])
	require.True(t, names["golusoris_sockmap_redirect_errors_total"])
}
