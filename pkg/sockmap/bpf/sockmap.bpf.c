// SPDX-License-Identifier: GPL-2.0
//
// CO-RE eBPF program pair for golusoris's colocated-IPC sockmap redirect.
//
//   - sockmap_sockops (SOCK_OPS): on every passive/active established TCP
//     connection in the attached cgroup v2, inserts the socket into the
//     pinned sockhash, keyed by the connection 4-tuple. This is the kernel-side
//     populator the userspace loader cannot replace (userspace may only insert
//     established sockets, never listen sockets).
//
//   - sockmap_redirect (SK_MSG): for each message sent on a registered socket,
//     looks up the *peer* 4-tuple in the same sockhash and redirects the
//     payload straight into the peer's receive queue, bypassing the loopback
//     TCP/IP stack.
//
// The map is pinned by name ("golusoris_sockhash"); the Go loader pins it at
// the configured bpffs path and the external (sveltesentio) loader reads the
// same pin as a client.
//
// Build (committed .o is checked in; regenerate via `go generate ./pkg/sockmap`):
//   clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -c sockmap.bpf.c -o sockmap.bpf.o

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// sock_key is the connection 4-tuple used to key both directions. Storing the
// peer's key lets SK_MSG redirect to the other end.
struct sock_key {
	__u32 sip;
	__u32 dip;
	__u32 sport;
	__u32 dport;
};

struct {
	__uint(type, BPF_MAP_TYPE_SOCKHASH);
	__uint(max_entries, 1024);
	__type(key, struct sock_key);
	__type(value, __u32);
	__uint(pinning, LIBBPF_PIN_BY_NAME);
} golusoris_sockhash SEC(".maps");

static __always_inline void sock_key_from_ops(struct bpf_sock_ops *ops,
					      struct sock_key *key)
{
	key->sip = ops->local_ip4;
	key->dip = ops->remote_ip4;
	key->sport = ops->local_port;                 // host byte order
	key->dport = bpf_ntohl(ops->remote_port);     // network byte order
}

SEC("sockops")
int sockmap_sockops(struct bpf_sock_ops *ops)
{
	// Only IPv4 TCP on loopback colocation is in scope for this slice.
	if (ops->family != 2 /* AF_INET */)
		return 0;

	switch (ops->op) {
	case BPF_SOCK_OPS_PASSIVE_ESTABLISHED_CB:
	case BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB: {
		struct sock_key key = {};
		sock_key_from_ops(ops, &key);
		bpf_sock_hash_update(ops, &golusoris_sockhash, &key, BPF_NOEXIST);
		break;
	}
	default:
		break;
	}
	return 0;
}

SEC("sk_msg")
int sockmap_redirect(struct sk_msg_md *msg)
{
	// Build the *peer* key by swapping source/destination, then redirect the
	// message into that socket's receive queue.
	struct sock_key key = {
		.sip = msg->remote_ip4,
		.dip = msg->local_ip4,
		.sport = bpf_ntohl(msg->remote_port),
		.dport = msg->local_port,
	};
	return bpf_msg_redirect_hash(msg, &golusoris_sockhash, &key, BPF_F_INGRESS);
}

char _license[] SEC("license") = "GPL";
