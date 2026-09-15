// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package registry wraps [github.com/google/go-containerregistry] for
// talking to OCI/Docker image registries: parsing references, resolving a
// tag to its content digest, fetching a manifest, listing tags, and copying
// an image (or index) between registries.
//
// [Client] is deliberately thin — it configures auth and transport once and
// forwards to [remote.Puller] / [remote.Pusher] for the actual registry
// calls. Every network method takes a [context.Context] and is additionally
// bounded by the client's own timeout (Options.Timeout, default
// [DefaultTimeout]), so a caller that forgets a deadline still gets one.
//
// # Usage
//
//	c := registry.New(registry.Options{}, nil, nil) // authn.DefaultKeychain + http.DefaultTransport
//
//	digest, err := c.Resolve(ctx, "gcr.io/distroless/static:nonroot")
//	man, err    := c.Manifest(ctx, "gcr.io/distroless/static@"+digest.DigestStr())
//	tags, err   := c.ListTags(ctx, "gcr.io/distroless/static")
//	err          = c.Copy(ctx, "gcr.io/distroless/static:nonroot", "my-registry.example.com/static:nonroot")
//
// # Authentication
//
// The default [authn.Keychain] ([authn.DefaultKeychain]) resolves credentials
// the same way `docker`/`crane` do: `~/.docker/config.json`, plus registered
// cloud credential helpers (ECR, GCR, ACR) when their `pkg/authn/*` blank
// imports are wired by the app. Pass an explicit [authn.Keychain] to override
// it — tests in this package use [authn.NewMultiKeychain]() (always resolves
// to [authn.Anonymous]) against an unauthenticated registry.
//
// # Transport
//
// A nil Transport falls back to [http.DefaultTransport]. Inject a custom
// [http.RoundTripper] for mTLS, a proxy, or (as this package's own tests do)
// to simulate registry faults such as a corrupted response body.
package registry

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// DefaultTimeout bounds a single registry call (resolve, manifest fetch, tag
// list, or copy) when Options.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// defaultUserAgent identifies this client to registries when Options.UserAgent
// is empty.
const defaultUserAgent = "golusoris-container-registry"

// Options is the koanf-bound config for [Client], under the "container.registry"
// prefix. Keychain and transport are wired separately (see [New] and [Module])
// since they are Go values, not scalar config.
type Options struct {
	// UserAgent sent with every registry request. Empty uses a stable default.
	UserAgent string `koanf:"user_agent"`
	// Timeout bounds a single registry call. Zero uses [DefaultTimeout].
	Timeout time.Duration `koanf:"timeout"`
}

// Client talks to OCI/Docker image registries. The zero value is not usable;
// build one with [New]. A *Client is safe for concurrent use — it holds no
// mutable state, only configuration.
type Client struct {
	keychain  authn.Keychain
	transport http.RoundTripper
	userAgent string
	timeout   time.Duration
}

// New builds a [Client]. keychain defaults to [authn.DefaultKeychain] when
// nil (the same resolution `docker`/`crane` use); transport defaults to
// [http.DefaultTransport] when nil.
func New(opts Options, keychain authn.Keychain, transport http.RoundTripper) *Client {
	if keychain == nil {
		keychain = authn.DefaultKeychain
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{keychain: keychain, transport: transport, userAgent: ua, timeout: timeout}
}

// ParseReference parses a docker/OCI image reference string ("nginx",
// "nginx:1.27", "gcr.io/proj/img@sha256:...") into a structured
// [name.Reference]. It performs no network I/O.
func ParseReference(ref string) (name.Reference, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("registry: parse reference %q: %w", ref, err)
	}
	return r, nil
}

// remoteOptions builds the [remote.Option] set every call uses: the
// configured keychain, transport, and user agent.
func (c *Client) remoteOptions() []remote.Option {
	return []remote.Option{
		remote.WithAuthFromKeychain(c.keychain),
		remote.WithTransport(c.transport),
		remote.WithUserAgent(c.userAgent),
	}
}

// bound derives a child context capped at the client's configured timeout.
// Callers must invoke the returned cancel func (HISS-02: explicit timeout on
// all I/O).
func (c *Client) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

// Resolve resolves ref to its content-addressed [name.Digest] via a manifest
// HEAD request — the canonical digest without downloading the manifest body.
func (c *Client) Resolve(ctx context.Context, ref string) (name.Digest, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return name.Digest{}, err
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return name.Digest{}, fmt.Errorf("registry: build puller: %w", err)
	}
	desc, err := puller.Head(ctx, r)
	if err != nil {
		return name.Digest{}, fmt.Errorf("registry: resolve %q: %w", ref, err)
	}
	return r.Context().Digest(desc.Digest.String()), nil
}

// Manifest is a fetched image (or index) manifest.
type Manifest struct {
	// Digest is the content digest of Raw.
	Digest v1.Hash
	// MediaType is the manifest's declared media type — an OCI/Docker image
	// manifest or an image index (multi-platform).
	MediaType types.MediaType
	// Size is len(Raw).
	Size int64
	// Raw is the exact manifest bytes as served by the registry.
	Raw []byte
}

// Manifest fetches the full manifest (or index) for ref. When ref carries an
// explicit digest, go-containerregistry validates the response against it and
// [Client.Manifest] returns that mismatch as an error — see the package doc's
// Transport section for how tests exercise this.
func (c *Client) Manifest(ctx context.Context, ref string) (*Manifest, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()
	puller, err := remote.NewPuller(c.remoteOptions()...)
	if err != nil {
		return nil, fmt.Errorf("registry: build puller: %w", err)
	}
	desc, err := puller.Get(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("registry: fetch manifest %q: %w", ref, err)
	}
	return &Manifest{
		Digest:    desc.Digest,
		MediaType: desc.MediaType,
		Size:      desc.Size,
		Raw:       desc.Manifest,
	}, nil
}
