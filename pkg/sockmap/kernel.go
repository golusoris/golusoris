package sockmap

import (
	"fmt"
	"strconv"
	"strings"
)

// parseKernelVersion extracts (major, minor) from a uname release string such
// as "6.7.0-arch1-1" or "5.10.0-21-amd64". Anything after the minor component
// is ignored. Returns an error on a release string with no parseable
// major.minor prefix.
func parseKernelVersion(release string) (major, minor int, err error) {
	// Strip the trailing "-..." / "+..." suffix before splitting on '.'.
	core := release
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("sockmap: kernel: unparseable release %q", release)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("sockmap: kernel: bad major in %q: %w", release, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("sockmap: kernel: bad minor in %q: %w", release, err)
	}
	return major, minor, nil
}

// kernelAtLeast reports whether (haveMajor, haveMinor) >= (wantMajor,
// wantMinor).
func kernelAtLeast(haveMajor, haveMinor, wantMajor, wantMinor int) bool {
	if haveMajor != wantMajor {
		return haveMajor > wantMajor
	}
	return haveMinor >= wantMinor
}

// checkKernelRelease validates a release string against the configured floor.
// Split out from the Uname call so it is testable without a syscall.
func checkKernelRelease(release string, wantMajor, wantMinor int) error {
	major, minor, err := parseKernelVersion(release)
	if err != nil {
		return err
	}
	if !kernelAtLeast(major, minor, wantMajor, wantMinor) {
		return fmt.Errorf(
			"sockmap: kernel %d.%d is below the required floor %d.%d (CO-RE/BTF baseline)",
			major, minor, wantMajor, wantMinor,
		)
	}
	return nil
}
