package sockmap

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// cgroupV2Mount is the unified-hierarchy mount point. The SOCK_OPS attach
// point must live under here; cgroup v1 is unsupported (the BPF program is
// v2-only).
const cgroupV2Mount = "/sys/fs/cgroup"

// cgroupV2Marker exists only on a cgroup v2 unified hierarchy. Its absence is
// the loud signal that we are on cgroup v1 (or a hybrid) and must refuse.
const cgroupV2Marker = cgroupV2Mount + "/cgroup.controllers"

// resolveCgroupPath returns the cgroup v2 directory the SOCK_OPS program
// attaches to. An explicit override wins; otherwise it auto-detects the
// process's own cgroup v2 path from /proc/self/cgroup. It fails loudly when
// /sys/fs/cgroup is not a cgroup v2 unified hierarchy, since the redirect is
// v2-only.
func resolveCgroupPath(override string) (string, error) {
	if err := requireCgroupV2(); err != nil {
		return "", err
	}
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("sockmap: cgroup: override path %q: %w", override, err)
		}
		return override, nil
	}
	rel, err := selfCgroupV2()
	if err != nil {
		return "", err
	}
	full := cgroupV2Mount + rel
	if _, err := os.Stat(full); err != nil {
		return "", fmt.Errorf("sockmap: cgroup: resolved path %q: %w", full, err)
	}
	return full, nil
}

// requireCgroupV2 returns an error unless /sys/fs/cgroup is a cgroup v2
// unified hierarchy.
func requireCgroupV2() error {
	if _, err := os.Stat(cgroupV2Marker); err != nil {
		return fmt.Errorf(
			"sockmap: cgroup: %s missing — sockmap requires a cgroup v2 unified hierarchy "+
				"(cgroup v1 / hybrid is unsupported): %w",
			cgroupV2Marker, err,
		)
	}
	return nil
}

// selfCgroupV2 parses the cgroup v2 path of the current process from
// /proc/self/cgroup. On a unified hierarchy the relevant line has the form
// "0::<path>".
func selfCgroupV2() (string, error) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("sockmap: cgroup: open /proc/self/cgroup: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		// "hierarchy-ID:controller-list:cgroup-path"; v2 is "0::<path>".
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			if rel == "" {
				return "/", nil
			}
			return rel, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("sockmap: cgroup: scan /proc/self/cgroup: %w", err)
	}
	return "", errors.New("sockmap: cgroup: no cgroup v2 (0::) line in /proc/self/cgroup")
}
