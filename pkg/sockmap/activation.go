package sockmap

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// listenFDsStart is the first FD systemd passes via socket activation
// (SD_LISTEN_FDS_START). FDs 0/1/2 are stdin/stdout/stderr.
const listenFDsStart = 3

// ActivationListeners returns the listeners systemd passed via socket
// activation, honoring LISTEN_PID / LISTEN_FDS (and the optional
// LISTEN_FDNAMES). It is the unprivileged Go-side half of the SK_MSG handoff:
// the loader/supervisor holds CAP_BPF and owns the BPF program; golusoris just
// needs its own listen FD, which socket activation hands over directly.
//
// Returns (nil, nil) when the process was not socket-activated (LISTEN_FDS
// unset or addressed to a different PID), so callers can fall back to a normal
// net.Listen. names, when non-empty, are the FileDescriptorName values in FD
// order (empty string for unnamed FDs).
//
// Mirrors github.com/coreos/go-systemd/v22/activation without taking the dep:
// the protocol is three env vars and os.NewFile.
func ActivationListeners() (listeners []net.Listener, names []string, err error) {
	pidEnv := os.Getenv("LISTEN_PID")
	if pidEnv == "" {
		return nil, nil, nil
	}
	pid, perr := strconv.Atoi(pidEnv)
	if perr != nil {
		return nil, nil, fmt.Errorf("sockmap: activation: bad LISTEN_PID %q: %w", pidEnv, perr)
	}
	if pid != os.Getpid() {
		// FDs were addressed to another process; not for us.
		return nil, nil, nil
	}
	n, err := activationCount()
	if err != nil {
		return nil, nil, err
	}
	if n == 0 {
		return nil, nil, nil
	}
	fdNames := activationNames(n)
	listeners = make([]net.Listener, 0, n)
	names = make([]string, 0, n)
	for i := range n {
		fd := listenFDsStart + i
		name := fdName(fdNames, i, fd)
		ln, lerr := listenerFromFD(fd, name)
		if lerr != nil {
			return nil, nil, lerr
		}
		listeners = append(listeners, ln)
		names = append(names, name)
	}
	return listeners, names, nil
}

// activationCount parses LISTEN_FDS into a bounded count.
func activationCount() (int, error) {
	v := os.Getenv("LISTEN_FDS")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("sockmap: activation: bad LISTEN_FDS %q: %w", v, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("sockmap: activation: negative LISTEN_FDS %d", n)
	}
	return n, nil
}

// activationNames splits LISTEN_FDNAMES on ':' into at most n entries.
func activationNames(n int) []string {
	raw := os.Getenv("LISTEN_FDNAMES")
	if raw == "" {
		return nil
	}
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(raw) && len(out) < n; i++ {
		if raw[i] == ':' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if len(out) < n {
		out = append(out, raw[start:])
	}
	return out
}

// fdName returns the systemd-provided name for the i-th FD, or a synthetic
// "fd-<n>" fallback when LISTEN_FDNAMES is absent or short.
func fdName(names []string, i, fd int) string {
	if i < len(names) && names[i] != "" {
		return names[i]
	}
	return "fd-" + strconv.Itoa(fd)
}

// listenerFromFD wraps a passed FD as a net.Listener. The FD is dup'd by
// net.FileListener, so the *os.File can be closed immediately after.
func listenerFromFD(fd int, name string) (net.Listener, error) {
	f := os.NewFile(uintptr(fd), name) //nolint:gosec // G115: fd is a socket file descriptor — non-negative, no overflow
	if f == nil {
		return nil, fmt.Errorf("sockmap: activation: invalid fd %d", fd)
	}
	ln, err := net.FileListener(f)
	closeErr := f.Close()
	if err != nil {
		return nil, fmt.Errorf("sockmap: activation: fd %d (%s): %w", fd, name, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("sockmap: activation: close dup fd %d: %w", fd, closeErr)
	}
	return ln, nil
}
