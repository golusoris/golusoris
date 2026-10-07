// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river/rivertype"

	"github.com/golusoris/golusoris/core/clock"
)

const (
	defaultRetryMax = time.Hour
	// maxBackoffShift keeps Base<<shift inside int64 for any Base >= 1ns.
	maxBackoffShift = 62
	// float53 scales a 53-bit integer onto [0,1) without float rounding bias.
	float53 = 1 << 53
)

// RetryOptions configures exponential backoff for failed jobs. Zero Base keeps
// River's default attempt^4 policy, so existing deployments see no change.
type RetryOptions struct {
	// Base is the delay after the first failure; each further failure doubles it.
	Base time.Duration `koanf:"base"`
	// Max caps the delay (default 1h when Base is set).
	Max time.Duration `koanf:"max"`
	// Jitter spreads each delay by +/- this fraction (0 = none, max 1).
	Jitter float64 `koanf:"jitter"`
}

func (o RetryOptions) enabled() bool { return o.Base > 0 }

func (o RetryOptions) withDefaults() RetryOptions {
	if o.enabled() && o.Max == 0 {
		o.Max = max(defaultRetryMax, o.Base)
	}
	return o
}

func (o RetryOptions) validate() error {
	switch {
	case o.Base < 0 || o.Max < 0:
		return errors.New("jobs: retry: base and max must not be negative")
	case o.Jitter < 0 || o.Jitter > 1:
		return errors.New("jobs: retry: jitter must be within [0, 1]")
	case o.enabled() && o.Max > 0 && o.Max < o.Base:
		return errors.New("jobs: retry: max must not be below base")
	}
	return nil
}

// RetryPolicy is an exponential-backoff river.ClientRetryPolicy. River calls
// NextRetry after a failed attempt that still has attempts left.
type RetryPolicy struct {
	opts  RetryOptions
	clock clock.Clock
	unit  func() float64
}

// NewRetryPolicy validates opts and returns a policy reading "now" from clk
// (nil = wall clock). Base must be positive.
func NewRetryPolicy(opts RetryOptions, clk clock.Clock) (*RetryPolicy, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if !opts.enabled() {
		return nil, errors.New("jobs: retry: base must be positive")
	}
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	return &RetryPolicy{opts: opts.withDefaults(), clock: clk, unit: cryptoUnit}, nil
}

// Delay returns the un-jittered backoff before retry number attempt (1 = after
// the first failure): Base * 2^(attempt-1), capped at Max.
func (p *RetryPolicy) Delay(attempt int) time.Duration {
	shift := min(max(attempt-1, 0), maxBackoffShift)
	if p.opts.Base > p.opts.Max>>shift {
		return p.opts.Max
	}
	return p.opts.Base << shift
}

// NextRetry implements river.ClientRetryPolicy. Attempt count follows River's
// default policy: recorded errors + 1, so snoozes do not advance the backoff.
func (p *RetryPolicy) NextRetry(job *rivertype.JobRow) time.Time {
	attempt := 1
	if job != nil {
		attempt = len(job.Errors) + 1
	}
	return p.clock.Now().UTC().Add(p.jitter(p.Delay(attempt)))
}

func (p *RetryPolicy) jitter(d time.Duration) time.Duration {
	if p.opts.Jitter == 0 {
		return d
	}
	// Float math: d + spread can exceed int64 when Max is near its limit.
	total := float64(d) * (1 + p.opts.Jitter*(2*p.unit()-1))
	switch {
	case total >= float64(p.opts.Max):
		return p.opts.Max
	case total <= 0:
		return 0
	}
	return time.Duration(total)
}

// cryptoUnit returns a uniform float in [0,1); forbidigo bans math/rand here.
func cryptoUnit() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.5 // centre of the range = no jitter
	}
	return float64(binary.LittleEndian.Uint64(b[:])>>11) / float53
}
