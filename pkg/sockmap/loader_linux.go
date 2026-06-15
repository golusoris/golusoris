//go:build linux

package sockmap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"go.uber.org/fx"
	"golang.org/x/sys/unix"
)

// sockKey is the connection 4-tuple keying the sockhash. It mirrors the
// `struct sock_key` in bpf/sockmap.bpf.c byte-for-byte so userspace inserts and
// the SK_MSG redirect agree on the key. All fields are host byte order, except
// the BPF side normalizes ports the same way (see the C source).
type sockKey struct {
	SrcIP   uint32
	DstIP   uint32
	SrcPort uint32
	DstPort uint32
}

// Sockmap owns the pinned sockhash and the attached SOCK_OPS + SK_MSG
// programs. It is the golusoris-side owner of the redirect: the SOCK_OPS
// program populates the sockhash from kernel context, and golusoris removes any
// entries it inserted before close. Methods are goroutine-safe.
type Sockmap struct {
	opts   Options
	log    *slog.Logger
	prov   ObjectProvider
	m      *Metrics
	cgroup string

	mu        sync.Mutex
	started   bool
	pending   []syscall.Conn // conns registered before Start
	keys      []sockKey      // sockhash keys we inserted (for cleanup)
	sockhash  *ebpf.Map
	coll      *ebpf.Collection
	sockOps   link.Link
	skMsgAttd bool
}

// sockmapParams are the fx dependencies of the module.
type sockmapParams struct {
	fx.In
	LC       fx.Lifecycle
	Options  Options
	Metrics  *Metrics
	Logger   *slog.Logger
	Provider ObjectProvider `optional:"true"`
}

// newSockmap constructs the (not-yet-started) Sockmap from the fx graph.
func newSockmap(p sockmapParams) *Sockmap {
	return &Sockmap{
		opts: p.Options,
		log:  p.Logger,
		prov: p.Provider,
		m:    p.Metrics,
	}
}

// startSockmap wires the load (OnStart) and the pre-shutdown cleanup
// (OnStop) into the fx lifecycle. A no-op when sockmap.enabled = false.
func startSockmap(lc fx.Lifecycle, s *Sockmap) {
	if !s.opts.Enabled {
		s.log.Debug("sockmap: disabled (sockmap.enabled = false)")
		return
	}
	lc.Append(fx.Hook{
		OnStart: s.start,
		OnStop:  s.stop,
	})
}

// start validates the platform, loads/pins the sockhash, attaches the
// programs, and inserts any listeners registered before Start.
func (s *Sockmap) start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := checkKernelRelease(unameRelease(), s.opts.MinKernelMajor, s.opts.MinKernelMinor); err != nil {
		return err
	}
	cg, err := resolveCgroupPath(s.opts.CgroupPath)
	if err != nil {
		return err
	}
	s.cgroup = cg

	// Load the BPF spec first (if a provider is wired) so the sockhash is
	// sized to match the object's map declaration — operator MaxEntries only
	// governs scaffold mode, where the program isn't the source of truth.
	spec, err := s.loadSpec()
	if err != nil {
		return err
	}
	if err := s.loadSockhash(spec); err != nil {
		return err
	}
	if err := s.attachPrograms(spec); err != nil {
		s.teardownLocked()
		return err
	}
	for _, c := range s.pending {
		if err := s.insertLocked(c); err != nil {
			s.teardownLocked()
			return err
		}
	}
	s.pending = nil
	s.started = true
	s.log.InfoContext(
		ctx, "sockmap: started",
		slog.String("pin_path", s.opts.PinPath),
		slog.String("cgroup", s.cgroup),
		slog.Int("sockets", len(s.keys)),
	)
	return nil
}

// loadSpec returns the parsed BPF collection spec, or (nil, nil) when no
// ObjectProvider is wired (scaffold mode).
func (s *Sockmap) loadSpec() (*ebpf.CollectionSpec, error) {
	if s.prov == nil {
		return nil, nil //nolint:nilnil // (nil, nil) is the documented "no object" sentinel
	}
	data, err := s.prov()
	if err != nil {
		return nil, fmt.Errorf("sockmap: object provider: %w", err)
	}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("sockmap: load collection spec: %w", err)
	}
	return spec, nil
}

// loadSockhash opens the pinned sockhash or creates+pins a fresh one. When a
// spec is present the map is sized to match the object's declaration (so the
// MapReplacement binds); otherwise it uses Options.MaxEntries. Removing our FDs
// before close (in stop) keeps the pin free of stale entries.
func (s *Sockmap) loadSockhash(spec *ebpf.CollectionSpec) error {
	if m, err := ebpf.LoadPinnedMap(s.opts.PinPath, nil); err == nil {
		s.sockhash = m
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.opts.PinPath), 0o750); err != nil {
		return fmt.Errorf("sockmap: mkdir pin dir: %w", err)
	}
	maxEntries := s.opts.MaxEntries
	if spec != nil {
		if name, err := specSockhashName(spec); err == nil {
			maxEntries = spec.Maps[name].MaxEntries
		}
	}
	mapSpec := &ebpf.MapSpec{
		Name:       s.opts.MapName,
		Type:       ebpf.SockHash,
		KeySize:    16, // sockKey: 4 x u32 connection tuple
		ValueSize:  4,  // socket FD (u32)
		MaxEntries: maxEntries,
	}
	m, err := ebpf.NewMap(mapSpec)
	if err != nil {
		return fmt.Errorf("sockmap: create sockhash: %w", err)
	}
	if err := m.Pin(s.opts.PinPath); err != nil {
		_ = m.Close()
		return fmt.Errorf("sockmap: pin sockhash at %s: %w", s.opts.PinPath, err)
	}
	s.sockhash = m
	return nil
}

// attachPrograms instantiates the spec (binding its sockhash map to our pinned
// instance) and attaches the SOCK_OPS program to the cgroup and the SK_MSG
// program to the sockhash. With no spec, attach is skipped: the sockhash + FD
// lifecycle still work, but no kernel-side redirect happens ("scaffold" mode).
func (s *Sockmap) attachPrograms(spec *ebpf.CollectionSpec) error {
	if spec == nil {
		s.log.Warn("sockmap: no ObjectProvider — managing sockhash only, redirect programs not attached")
		return nil
	}
	// Bind the spec's sockhash map to the one we already created + pinned, so
	// the SK_MSG program and the userspace inserts share a single map (and the
	// LIBBPF_PIN_BY_NAME declaration in the object resolves to our pin). The
	// spec map name comes from the object, not Options.
	sockhashName, err := specSockhashName(spec)
	if err != nil {
		return err
	}
	opts := ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{sockhashName: s.sockhash},
	}
	coll, err := ebpf.NewCollectionWithOptions(spec, opts)
	if err != nil {
		return fmt.Errorf("sockmap: new collection: %w", err)
	}
	s.coll = coll
	if err := s.attachSockOps(); err != nil {
		return err
	}
	return s.attachSkMsg()
}

// specSockhashName returns the name of the SockHash/SockMap map declared in
// the spec, so we can replace it with our pinned instance regardless of the
// configured MapName.
func specSockhashName(spec *ebpf.CollectionSpec) (string, error) {
	for name, ms := range spec.Maps {
		if ms.Type == ebpf.SockHash || ms.Type == ebpf.SockMap {
			return name, nil
		}
	}
	return "", errors.New("sockmap: object declares no SockHash/SockMap map")
}

// attachSockOps links the SOCK_OPS program to the resolved cgroup v2 path so
// new connections in this cgroup populate the sockhash.
func (s *Sockmap) attachSockOps() error {
	prog := s.coll.Programs[s.opts.SockOpsProg]
	if prog == nil {
		return fmt.Errorf("sockmap: program %q not found in object", s.opts.SockOpsProg)
	}
	l, err := link.AttachCgroup(link.CgroupOptions{
		Path:    s.cgroup,
		Attach:  ebpf.AttachCGroupSockOps,
		Program: prog,
	})
	if err != nil {
		return fmt.Errorf("sockmap: attach sockops to cgroup %s: %w", s.cgroup, err)
	}
	s.sockOps = l
	return nil
}

// attachSkMsg attaches the SK_MSG verdict program to the sockhash so payloads
// are redirected between registered peer sockets.
func (s *Sockmap) attachSkMsg() error {
	prog := s.coll.Programs[s.opts.SkMsgProg]
	if prog == nil {
		return fmt.Errorf("sockmap: program %q not found in object", s.opts.SkMsgProg)
	}
	err := link.RawAttachProgram(link.RawAttachProgramOptions{
		Target:  s.sockhash.FD(),
		Program: prog,
		Attach:  ebpf.AttachSkMsgVerdict,
	})
	if err != nil {
		return fmt.Errorf("sockmap: attach sk_msg to sockhash: %w", err)
	}
	s.skMsgAttd = true
	return nil
}

// registerConn records (pre-Start) or immediately inserts (post-Start) a
// socket FD into the sockhash. It is the implementation behind the exported
// RegisterConn.
func (s *Sockmap) registerConn(c syscall.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.pending = append(s.pending, c)
		return nil
	}
	return s.insertLocked(c)
}

// insertLocked extracts the raw FD + 4-tuple from c and inserts the socket
// into the sockhash under its connection key (matching what the SK_MSG program
// computes). Caller holds s.mu.
func (s *Sockmap) insertLocked(c syscall.Conn) error {
	var (
		fd  uint32
		key sockKey
		err error
	)
	rc, scErr := c.SyscallConn()
	if scErr != nil {
		return fmt.Errorf("sockmap: conn syscall conn: %w", scErr)
	}
	cerr := rc.Control(func(f uintptr) {
		fd, key, err = sockFDAndKey(int(f)) // #nosec G115 -- f is a live socket fd from RawConn.Control, a small non-negative int
	})
	if cerr != nil {
		return fmt.Errorf("sockmap: conn control: %w", cerr)
	}
	if err != nil {
		return err
	}
	if err := s.sockhash.Update(&key, fd, ebpf.UpdateAny); err != nil {
		return fmt.Errorf("sockmap: insert socket into sockhash: %w", err)
	}
	s.keys = append(s.keys, key)
	if s.m != nil {
		s.m.ActiveSockets.Set(float64(len(s.keys)))
	}
	return nil
}

// stop removes our FDs from the sockhash *before* the sockets close, then
// detaches programs and frees resources. This ordering is the pre-shutdown
// cleanup hook the issue requires: no stale entry ever redirects to a
// destroyed socket.
func (s *Sockmap) stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.InfoContext(ctx, "sockmap: pre-shutdown cleanup", slog.Int("sockets", len(s.keys)))
	s.teardownLocked()
	s.started = false
	return nil
}

// teardownLocked deletes sockhash entries and frees links/maps. Best-effort:
// errors are logged, not returned, so shutdown always completes. Caller holds
// s.mu.
func (s *Sockmap) teardownLocked() {
	for i := range s.keys {
		key := s.keys[i]
		if err := s.sockhash.Delete(&key); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			s.log.Warn("sockmap: delete sockhash entry",
				slog.Uint64("src_port", uint64(key.SrcPort)),
				slog.Uint64("dst_port", uint64(key.DstPort)),
				slog.String("error", err.Error()))
		}
	}
	s.keys = nil
	if s.m != nil {
		s.m.ActiveSockets.Set(0)
	}
	if s.sockOps != nil {
		_ = s.sockOps.Close()
		s.sockOps = nil
	}
	if s.skMsgAttd && s.sockhash != nil && s.coll != nil {
		if prog := s.coll.Programs[s.opts.SkMsgProg]; prog != nil {
			_ = link.RawDetachProgram(link.RawDetachProgramOptions{
				Target:  s.sockhash.FD(),
				Program: prog,
				Attach:  ebpf.AttachSkMsgVerdict,
			})
		}
		s.skMsgAttd = false
	}
	if s.coll != nil {
		s.coll.Close()
		s.coll = nil
	}
	if s.sockhash != nil {
		// Close our handle; the pin keeps the map alive for the external
		// loader (it is a client of the same pinned sockhash).
		_ = s.sockhash.Close()
		s.sockhash = nil
	}
}

// sockFDAndKey returns the socket FD (as uint32, the sockhash value type) and
// its connection 4-tuple key, derived from getsockname/getpeername. The key
// layout matches `struct sock_key` in the BPF source so a userspace insert and
// the SK_MSG redirect agree. IPv4 TCP only. A kernel FD is a small non-negative
// int; the guard makes the narrowing provably lossless.
func sockFDAndKey(fd int) (uint32, sockKey, error) {
	if fd < 0 || fd > math.MaxUint32 {
		return 0, sockKey{}, fmt.Errorf("sockmap: fd %d out of uint32 range", fd)
	}
	local, err := unix.Getsockname(fd)
	if err != nil {
		return 0, sockKey{}, fmt.Errorf("sockmap: getsockname: %w", err)
	}
	remote, err := unix.Getpeername(fd)
	if err != nil {
		return 0, sockKey{}, fmt.Errorf("sockmap: getpeername (socket not connected?): %w", err)
	}
	l4, ok := local.(*unix.SockaddrInet4)
	if !ok {
		return 0, sockKey{}, errors.New("sockmap: only IPv4 sockets are supported")
	}
	r4, ok := remote.(*unix.SockaddrInet4)
	if !ok {
		return 0, sockKey{}, errors.New("sockmap: only IPv4 sockets are supported")
	}
	sport, err := portU32(l4.Port)
	if err != nil {
		return 0, sockKey{}, err
	}
	dport, err := portU32(r4.Port)
	if err != nil {
		return 0, sockKey{}, err
	}
	key := sockKey{
		SrcIP:   ipToHostU32(l4.Addr),
		DstIP:   ipToHostU32(r4.Addr),
		SrcPort: sport, // host byte order, matching the BPF side
		DstPort: dport,
	}
	return uint32(fd), key, nil
}

// portU32 narrows a kernel-supplied TCP/UDP port to uint32, erroring if it is
// outside the valid 0..65535 range — which makes the conversion provably
// lossless rather than merely assumed.
func portU32(p int) (uint32, error) {
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("sockmap: port %d out of range", p)
	}
	return uint32(p), nil
}

// ipToHostU32 packs a 4-byte IPv4 address into a host-byte-order uint32,
// matching how the kernel exposes local_ip4/remote_ip4 to the BPF program.
func ipToHostU32(b [4]byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// unameRelease returns the running kernel release string ("uname -r").
func unameRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unix.ByteSliceToString(u.Release[:])
}
