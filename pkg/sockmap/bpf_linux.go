//go:build linux

package sockmap

import _ "embed"

//go:generate clang -O2 -g -Wall -target bpf -D__TARGET_ARCH_x86 -c bpf/sockmap.bpf.c -o bpf/sockmap.bpf.o
//go:generate llvm-strip -g bpf/sockmap.bpf.o

// bpfObject is the committed, CO-RE-enabled SOCK_OPS + SK_MSG object. It is
// checked in (like bpf2go output) so the package builds without a clang
// toolchain in CI; regenerate with `go generate ./pkg/sockmap`.
//
//go:embed bpf/sockmap.bpf.o
var bpfObject []byte

// DefaultObjectProvider serves the bundled SOCK_OPS + SK_MSG object. Wire it as
// the module's ObjectProvider to get the full redirect (rather than scaffold-
// only) behavior:
//
//	fx.Provide(sockmap.DefaultObjectProvider)
//
// The bundled object expects program names "sockmap_sockops" and
// "sockmap_redirect" — the Options defaults. Only available on Linux.
func DefaultObjectProvider() ObjectProvider {
	return BytesProvider(bpfObject)
}
