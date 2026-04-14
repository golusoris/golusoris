package runtime

import (
	"testing"
)

func TestCgroupContains_missing(t *testing.T) {
	t.Parallel()
	// On non-Linux or when /proc/self/cgroup is absent, returns false — never panics.
	_ = cgroupContains("docker")
}

func TestContainerIDFromCgroup_missing(t *testing.T) {
	t.Parallel()
	// On non-Linux or when /proc/self/cgroup is absent, returns "" — never panics.
	_ = containerIDFromCgroup()
}
