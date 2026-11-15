// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
)

// ErrLimited indicates that a provider quota cannot be acquired within MaxWait.
var ErrLimited = errors.New("outbound rate limit exceeded")

// HostRate overrides the default rate for an exact hostname without a port.
type HostRate struct {
	Host string `koanf:"host"`
	Rate string `koanf:"rate"`
}

// TransportOptions configures http.outbound.ratelimit; an empty rate is unlimited.
type TransportOptions struct {
	Rate    string        `koanf:"rate"`
	Hosts   []HostRate    `koanf:"hosts"`
	MaxWait time.Duration `koanf:"max_wait"`
	Store   limiter.Store `koanf:"-"`
}

// Outbound enforces per-host quotas independently of inbound middleware.
type Outbound struct {
	fallback *limiter.Limiter
	hosts    map[string]*limiter.Limiter
	maxWait  time.Duration
	clock    clock.Clock
}

// NewOutbound validates opts and constructs an outbound quota wrapper.
func NewOutbound(opts TransportOptions, clk clock.Clock) (*Outbound, error) {
	if opts.MaxWait < 0 || validate.IsNil(clk) {
		return nil, errors.New("httpx/ratelimit: nonnegative max_wait and clock required")
	}
	if opts.Store != nil && validate.IsNil(opts.Store) {
		return nil, errors.New("httpx/ratelimit: store is typed nil")
	}
	out := &Outbound{hosts: make(map[string]*limiter.Limiter, len(opts.Hosts)), maxWait: opts.MaxWait, clock: clk}
	if opts.Rate != "" {
		fallback, err := newLimiter(opts.Rate, opts.Store)
		if err != nil {
			return nil, err
		}
		out.fallback = fallback
	}
	for _, entry := range opts.Hosts {
		if err := out.addHost(entry, opts.Store); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (o *Outbound) addHost(entry HostRate, store limiter.Store) error {
	host := strings.ToLower(entry.Host)
	if err := validate.New().Var(host, "hostname_rfc1123|ip"); err != nil {
		return fmt.Errorf("httpx/ratelimit: invalid host: %w", err)
	}
	if _, exists := o.hosts[host]; exists {
		return fmt.Errorf("httpx/ratelimit: duplicate host %q", host)
	}
	o.hosts[host] = nil
	if entry.Rate != "" {
		lim, err := newLimiter(entry.Rate, store)
		if err != nil {
			return err
		}
		o.hosts[host] = lim
	}
	return nil
}

func newLimiter(formatted string, store limiter.Store) (*limiter.Limiter, error) {
	rate, err := limiter.NewRateFromFormatted(formatted)
	if err != nil {
		return nil, fmt.Errorf("httpx/ratelimit: parse rate %q: %w", formatted, err)
	}
	if store == nil {
		store = memory.NewStore()
	}
	return limiter.New(store, rate), nil
}

type outboundTransport struct {
	owner *Outbound
	next  http.RoundTripper
}

// Wrap returns a transport; a nil upstream produces an error on RoundTrip.
func (o *Outbound) Wrap(next http.RoundTripper) http.RoundTripper {
	return &outboundTransport{owner: o, next: next}
}

func (t *outboundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if validate.IsNil(t.next) {
		return nil, closeRequestBody(req, errors.New("httpx/ratelimit: next transport required"))
	}
	host := strings.ToLower(req.URL.Hostname())
	lim, configured := t.owner.hosts[host]
	if !configured {
		lim = t.owner.fallback
	}
	if lim != nil {
		if err := t.owner.acquire(req, host, lim); err != nil {
			return nil, closeRequestBody(req, err)
		}
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("httpx/ratelimit: upstream: %w", err)
	}
	return resp, nil
}

func (o *Outbound) acquire(req *http.Request, host string, lim *limiter.Limiter) error {
	for attempt := range 2 {
		quota, err := lim.Get(req.Context(), "outbound:"+host)
		if err != nil {
			return fmt.Errorf("httpx/ratelimit: quota: %w", err)
		}
		if !quota.Reached {
			return nil
		}
		wait := max(time.Unix(quota.Reset+1, 0).Sub(o.clock.Now()), 0)
		if attempt == 1 || o.maxWait == 0 || wait > o.maxWait {
			break
		}
		timer := o.clock.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return fmt.Errorf("httpx/ratelimit: wait: %w", req.Context().Err())
		case <-timer.Chan():
		}
	}
	return fmt.Errorf("httpx/ratelimit: %s: %w", host, ErrLimited)
}

func loadTransportOptions(cfg *config.Config) (TransportOptions, error) {
	var opts TransportOptions
	if err := cfg.Unmarshal("http.outbound.ratelimit", &opts); err != nil {
		return TransportOptions{}, fmt.Errorf("httpx/ratelimit: load outbound options: %w", err)
	}
	return opts, nil
}

// OutboundModule provides an Outbound independently of Module.
var OutboundModule = fx.Module("golusoris.httpx.ratelimit.outbound", fx.Provide(loadTransportOptions, NewOutbound))

func closeRequestBody(req *http.Request, err error) error {
	if req.Body != nil {
		if closeErr := req.Body.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("httpx/ratelimit: close request: %w", closeErr))
		}
	}
	return err
}
