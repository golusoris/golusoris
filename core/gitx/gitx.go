// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package gitx runs git with the bounds the framework requires of every
// subprocess: an explicit context deadline, argument validation, and captured
// output of bounded size — never streamed to the process stdout. It is the
// shared seam for tooling that needs repository facts (remote URL, HEAD,
// dirtiness) or worktree management (see gitx/worktree) without shelling out
// ad hoc. Capability key: git.worktree (with the worktree sub-package).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Bounds (Power-of-10 rule 2).
const (
	// DefaultTimeout applies to every Run whose context has no deadline.
	DefaultTimeout = 30 * time.Second
	// MaxOutput caps captured stdout/stderr per invocation.
	MaxOutput = 16 << 20
)

// Sentinel errors. Compare with errors.Is.
var (
	ErrInvalidArg   = errors.New("gitx: invalid argument")
	ErrOutputTooBig = errors.New("gitx: output exceeds limit")
)

// Runner executes git in a fixed directory.
type Runner struct {
	dir     string
	bin     string
	timeout time.Duration
	maxOut  int64
}

// Option configures a Runner.
type Option func(*Runner)

// WithTimeout overrides DefaultTimeout for contexts without a deadline.
func WithTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithBinary overrides the git executable (default: "git" on PATH).
func WithBinary(path string) Option {
	return func(r *Runner) {
		if path != "" {
			r.bin = path
		}
	}
}

// WithMaxOutput overrides MaxOutput.
func WithMaxOutput(n int64) Option {
	return func(r *Runner) {
		if n > 0 {
			r.maxOut = n
		}
	}
}

// New returns a Runner rooted at dir ("" means the process working directory).
func New(dir string, opts ...Option) *Runner {
	r := &Runner{dir: dir, bin: "git", timeout: DefaultTimeout, maxOut: MaxOutput}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Dir returns the directory git runs in.
func (r *Runner) Dir() string { return r.dir }

// Run executes `git <args...>` and returns stdout. Arguments containing NUL
// are rejected; stderr is folded into the returned error.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return nil, fmt.Errorf("%w: NUL byte in %q", ErrInvalidArg, a)
		}
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	full := make([]string, 0, len(args)+2)
	full = append(full, "-c", "core.longpaths=true")
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, r.bin, full...) // #nosec G204 -- no shell: binary is git unless the caller-owned WithBinary option overrides it; args are NUL-checked and caller-owned
	cmd.Dir = r.dir
	stdout := &limitedBuffer{max: r.maxOut}
	stderr := &limitedBuffer{max: r.maxOut}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("%w (%d bytes): git %s", ErrOutputTooBig, r.maxOut, strings.Join(args, " "))
	}
	if runErr != nil {
		return nil, fmt.Errorf("gitx: git %s: %w: %s", strings.Join(args, " "), runErr, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Output runs git and returns trimmed stdout as a string.
func (r *Runner) Output(ctx context.Context, args ...string) (string, error) {
	out, err := r.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TopLevel returns the repository root (`rev-parse --show-toplevel`).
func (r *Runner) TopLevel(ctx context.Context) (string, error) {
	return r.Output(ctx, "rev-parse", "--show-toplevel")
}

// Head returns the full HEAD commit SHA.
func (r *Runner) Head(ctx context.Context) (string, error) {
	return r.Output(ctx, "rev-parse", "HEAD")
}

// Branch returns the current branch name, or "HEAD" when detached.
func (r *Runner) Branch(ctx context.Context) (string, error) {
	return r.Output(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// RemoteURL returns the fetch URL of the named remote.
func (r *Runner) RemoteURL(ctx context.Context, remote string) (string, error) {
	if !ValidRef(remote) {
		return "", fmt.Errorf("%w: remote %q", ErrInvalidArg, remote)
	}
	return r.Output(ctx, "remote", "get-url", remote)
}

// IsDirty reports whether the worktree has uncommitted changes (tracked or
// untracked).
func (r *Runner) IsDirty(ctx context.Context) (bool, error) {
	out, err := r.Output(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// refBadChars mirrors the git-check-ref-format forbidden set.
const refBadChars = " ~^:?*[\\\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f"

// ValidRef reports whether s is a plausible ref, branch, or remote name: non
// empty, no leading "-", no "..", "@{", trailing "/", ".lock", or control /
// glob characters.
func ValidRef(s string) bool {
	switch {
	case s == "", strings.HasPrefix(s, "-"), strings.HasPrefix(s, "/"), strings.HasSuffix(s, "/"):
		return false
	case strings.Contains(s, ".."), strings.Contains(s, "@{"), strings.HasSuffix(s, ".lock"), strings.Contains(s, "//"):
		return false
	case strings.ContainsAny(s, refBadChars):
		return false
	}
	return true
}

// remoteRE matches https://host/owner/repo(.git), ssh://git@host/owner/repo,
// and scp-like git@host:owner/repo(.git).
var remoteRE = regexp.MustCompile(`^(?:[a-z]+://(?:[^@/]+@)?[^/:]+(?::\d+)?/|[^@/:]+@[^/:]+:)(?:/?)([^/]+)/([^/]+?)(?:\.git)?/?$`)

// ParseRemote extracts owner and repository name from a remote URL.
func ParseRemote(url string) (owner, repo string, ok bool) {
	m := remoteRE.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// limitedBuffer is a bounded byte sink. It deliberately does NOT embed
// bytes.Buffer: the promoted ReadFrom would let io.Copy bypass Write and the
// bound with it.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int64
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.max {
		b.overflow = true
		return 0, ErrOutputTooBig
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *limitedBuffer) String() string { return b.buf.String() }
