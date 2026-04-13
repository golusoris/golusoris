// Package runtime detects the container/process runtime (Kubernetes,
// Docker, Podman, systemd-managed, or plain binary) and exposes a
// unified [Info] so the rest of the framework can attach identity
// metadata to logs/metrics/traces without caring where the process
// runs.
//
// This package is the runtime-agnostic successor to [k8s/podinfo]:
//
//   - On Kubernetes it reads the downward-API env vars (POD_NAME,
//     POD_NAMESPACE, POD_IP, NODE_NAME, SERVICE_ACCOUNT) plus the
//     container ID from cgroups.
//   - On Docker / Podman it reads the container ID from cgroups +
//     HOSTNAME.
//   - On systemd it reads INVOCATION_ID + NOTIFY_SOCKET presence to
//     identify the unit.
//   - On bare Linux it falls back to os.Hostname().
//
// `k8s/podinfo` remains as a k8s-only view for packages that want just
// the k8s fields; new code should prefer `container/runtime`.
package runtime

import (
	"bufio"
	"os"
	"regexp"
	"strings"

	"go.uber.org/fx"
)

// Runtime identifies the container/process platform.
type Runtime string

// Runtime values.
const (
	RuntimeK8s     Runtime = "kubernetes"
	RuntimeDocker  Runtime = "docker"
	RuntimePodman  Runtime = "podman"
	RuntimeSystemd Runtime = "systemd"
	RuntimeBare    Runtime = "bare"
)

// Info is the unified identity of the running process.
type Info struct {
	// Runtime is the detected platform.
	Runtime Runtime
	// Hostname is os.Hostname(). Always populated.
	Hostname string
	// ContainerID is the 12+ char container ID from /proc/self/cgroup,
	// or empty if the runtime doesn't expose it.
	ContainerID string

	// PodName, Namespace, PodIP, NodeName, ServiceAccount, ContainerName,
	// ContainerImage: populated on Kubernetes via the downward API.
	PodName        string
	Namespace      string
	PodIP          string
	NodeName       string
	ServiceAccount string
	ContainerName  string
	ContainerImage string

	// SystemdUnit is populated when Runtime == RuntimeSystemd, from the
	// SYSTEMD_UNIT env var (if set by the unit file) or falls back to the
	// INVOCATION_ID.
	SystemdUnit string
}

// Detect inspects the process environment + /proc + well-known files and
// returns a populated [Info]. Never errors: unknown details are left empty.
func Detect() Info {
	info := Info{Hostname: hostname()}
	switch {
	case isK8s():
		info.Runtime = RuntimeK8s
		info.PodName = os.Getenv("POD_NAME")
		info.Namespace = os.Getenv("POD_NAMESPACE")
		info.PodIP = os.Getenv("POD_IP")
		info.NodeName = os.Getenv("NODE_NAME")
		info.ServiceAccount = os.Getenv("SERVICE_ACCOUNT")
		info.ContainerName = os.Getenv("CONTAINER_NAME")
		info.ContainerImage = os.Getenv("CONTAINER_IMAGE")
		info.ContainerID = containerIDFromCgroup()
	case isPodman():
		info.Runtime = RuntimePodman
		info.ContainerID = containerIDFromCgroup()
	case isDocker():
		info.Runtime = RuntimeDocker
		info.ContainerID = containerIDFromCgroup()
	case isSystemd():
		info.Runtime = RuntimeSystemd
		info.SystemdUnit = systemdUnit()
	default:
		info.Runtime = RuntimeBare
	}
	return info
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func isK8s() bool {
	_, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token")
	return err == nil
}

// isDocker detects Docker via the /.dockerenv marker file + cgroup inspection.
// The marker file is always present in Docker containers; cgroup fallback
// handles edge cases (e.g. privileged containers that mask the root).
func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return cgroupContains("docker")
}

// isPodman detects Podman via its well-known container-marker file.
func isPodman() bool {
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return cgroupContains("libpod")
}

// isSystemd detects a systemd-managed process via the NOTIFY_SOCKET or
// INVOCATION_ID env vars (both set by systemd when spawning a unit).
func isSystemd() bool {
	return os.Getenv("NOTIFY_SOCKET") != "" || os.Getenv("INVOCATION_ID") != ""
}

func systemdUnit() string {
	if v := os.Getenv("SYSTEMD_UNIT"); v != "" {
		return v
	}
	// No dedicated unit-name env — fall back to invocation ID.
	return os.Getenv("INVOCATION_ID")
}

// cgroupContains reports whether /proc/self/cgroup contains the given
// substring. Graceful on non-Linux / missing file (returns false).
func cgroupContains(s string) bool {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.Contains(sc.Text(), s) {
			return true
		}
	}
	return false
}

// containerIDRegex matches Docker/containerd/cri-o/podman ID segments in a
// cgroup line. Accepts both the cgroup v1 path suffix (`/docker-<id>.scope`,
// `/<id>`) and v2 (`/docker-<id>.scope`). Matches a 64-char hex ID.
var containerIDRegex = regexp.MustCompile(`([0-9a-f]{64})`)

// containerIDFromCgroup scans /proc/self/cgroup for a 64-char hex container
// ID. Returns the first match or "".
func containerIDFromCgroup() string {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := containerIDRegex.FindString(sc.Text()); m != "" {
			return m
		}
	}
	return ""
}

// Module provides [Info] via fx.
var Module = fx.Module("golusoris.container.runtime",
	fx.Provide(Detect),
)
