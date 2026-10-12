// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package runtime

import (
	"errors"
	"slices"
	"testing"
)

const (
	saToken  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	someID   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	k8sGroup = "0::/kubepods/burstable/pod1/cri-containerd-" + someID + ".scope"
)

// fakeHost plants a host shape: env vars, existing files, and cgroup text
// (empty cgroup means the file is absent).
func fakeHost(env map[string]string, files []string, cgroup string) probe {
	return probe{
		getenv: func(k string) string { return env[k] },
		exists: func(path string) bool {
			return slices.Contains(files, path)
		},
		readFile: func(string) ([]byte, error) {
			if cgroup == "" {
				return nil, errors.New("no such file")
			}
			return []byte(cgroup), nil
		},
	}
}

func TestDetectK8sReadsDownwardAPI(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"POD_NAME": "myapp-abc-123", "POD_NAMESPACE": "production", "POD_IP": "10.244.1.5",
		"NODE_NAME": "node-1", "SERVICE_ACCOUNT": "myapp", "CONTAINER_NAME": "app",
		"CONTAINER_IMAGE": "ghcr.io/x/app:1", "INVOCATION_ID": "ignored",
	}
	got := detect(fakeHost(env, []string{saToken, "/.dockerenv"}, k8sGroup))
	want := Info{
		Runtime: RuntimeK8s, Hostname: got.Hostname, ContainerID: someID,
		PodName: "myapp-abc-123", Namespace: "production", PodIP: "10.244.1.5", NodeName: "node-1",
		ServiceAccount: "myapp", ContainerName: "app", ContainerImage: "ghcr.io/x/app:1",
	}
	if got != want {
		t.Errorf("detect() = %+v, want %+v", got, want)
	}
}

func TestDetectOrder(t *testing.T) {
	t.Parallel()
	systemd := map[string]string{"INVOCATION_ID": "abc123def456"}
	cases := []struct {
		name   string
		env    map[string]string
		files  []string
		cgroup string
		want   Runtime
	}{
		{"podman marker beats docker", nil, []string{"/run/.containerenv", "/.dockerenv"}, "", RuntimePodman},
		{"podman by cgroup", nil, nil, "0::/machine.slice/libpod-" + someID + ".scope", RuntimePodman},
		{"docker marker", systemd, []string{"/.dockerenv"}, "", RuntimeDocker},
		{"docker by cgroup", nil, nil, "0::/docker/" + someID, RuntimeDocker},
		{"systemd by invocation id", systemd, nil, "0::/system.slice/app.service", RuntimeSystemd},
		{"systemd by notify socket", map[string]string{"NOTIFY_SOCKET": "/run/systemd/notify"}, nil, "", RuntimeSystemd},
		{"bare", nil, nil, "0::/user.slice", RuntimeBare},
		{"bare without cgroup file", nil, nil, "", RuntimeBare},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := detect(fakeHost(tc.env, tc.files, tc.cgroup)).Runtime; got != tc.want {
				t.Errorf("Runtime = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectSystemdUnit(t *testing.T) {
	t.Parallel()
	byID := detect(fakeHost(map[string]string{"INVOCATION_ID": "abc123def456"}, nil, ""))
	if byID.SystemdUnit != "abc123def456" {
		t.Errorf("SystemdUnit = %q, want the INVOCATION_ID", byID.SystemdUnit)
	}
	named := detect(fakeHost(map[string]string{"INVOCATION_ID": "abc", "SYSTEMD_UNIT": "app.service"}, nil, ""))
	if named.SystemdUnit != "app.service" {
		t.Errorf("SystemdUnit = %q, want SYSTEMD_UNIT to win", named.SystemdUnit)
	}
	if named.PodName != "" || named.ContainerID != "" {
		t.Errorf("systemd branch set container fields: %+v", named)
	}
}

func TestCgroupContains(t *testing.T) {
	t.Parallel()
	if cgroupContains(fakeHost(nil, nil, ""), "docker") {
		t.Error("missing cgroup file: want false")
	}
	host := fakeHost(nil, nil, "1:name=systemd:/user.slice\n0::/docker/"+someID)
	if !cgroupContains(host, "docker") {
		t.Error("docker on the second line: want true")
	}
	if cgroupContains(host, "libpod") {
		t.Error("libpod absent: want false")
	}
}

func TestContainerIDFromCgroup(t *testing.T) {
	t.Parallel()
	if got := containerIDFromCgroup(fakeHost(nil, nil, "")); got != "" {
		t.Errorf("missing cgroup file: got %q, want empty", got)
	}
	if got := containerIDFromCgroup(fakeHost(nil, nil, "0::/user.slice/"+someID[:63])); got != "" {
		t.Errorf("63 hex chars: got %q, want empty", got)
	}
	if got := containerIDFromCgroup(fakeHost(nil, nil, "1:cpu:/\n"+k8sGroup)); got != someID {
		t.Errorf("got %q, want %q", got, someID)
	}
}
