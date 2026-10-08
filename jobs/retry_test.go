// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river/rivertype"

	"github.com/golusoris/golusoris/core/clock"
)

func TestRetryPolicy_DelayDoublesUntilMax(t *testing.T) {
	t.Parallel()
	p, err := NewRetryPolicy(RetryOptions{Base: time.Second, Max: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestRetryPolicy_DelayBoundaries(t *testing.T) {
	t.Parallel()
	p, err := NewRetryPolicy(RetryOptions{Base: time.Nanosecond, Max: math.MaxInt64}, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	if got := p.Delay(0); got != time.Nanosecond {
		t.Errorf("Delay(0) = %v, want base", got)
	}
	if got := p.Delay(-5); got != time.Nanosecond {
		t.Errorf("Delay(-5) = %v, want base", got)
	}
	if got := p.Delay(10_000); got != time.Duration(1)<<maxBackoffShift {
		t.Errorf("Delay(10000) = %v, want 2^62ns without overflow", got)
	}
	same, err := NewRetryPolicy(RetryOptions{Base: time.Minute, Max: time.Minute}, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy base==max: %v", err)
	}
	if got := same.Delay(3); got != time.Minute {
		t.Errorf("Delay(3) with base==max = %v, want 1m", got)
	}
}

func TestRetryPolicy_DefaultMax(t *testing.T) {
	t.Parallel()
	p, err := NewRetryPolicy(RetryOptions{Base: time.Second}, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	if got := p.Delay(100); got != defaultRetryMax {
		t.Fatalf("Delay(100) = %v, want default max %v", got, defaultRetryMax)
	}
}

func TestNewRetryPolicy_RejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		opts RetryOptions
		want string
	}{
		"zero base":     {RetryOptions{}, "base must be positive"},
		"negative base": {RetryOptions{Base: -time.Second}, "negative"},
		"negative max":  {RetryOptions{Base: time.Second, Max: -1}, "negative"},
		"max below":     {RetryOptions{Base: time.Minute, Max: time.Second}, "below base"},
		"jitter > 1":    {RetryOptions{Base: time.Second, Jitter: 1.01}, "jitter"},
		"jitter < 0":    {RetryOptions{Base: time.Second, Jitter: -0.1}, "jitter"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, err := NewRetryPolicy(tc.opts, nil)
			if p != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewRetryPolicy = %v, %v; want error %q", p, err, tc.want)
			}
		})
	}
}

func TestRetryPolicy_NextRetryUsesClockAndErrorCount(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	p, err := NewRetryPolicy(RetryOptions{Base: time.Second, Max: time.Hour}, clk)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	job := &rivertype.JobRow{Errors: []rivertype.AttemptError{{}, {}}}
	if got, want := p.NextRetry(job), clk.Now().UTC().Add(4*time.Second); !got.Equal(want) {
		t.Fatalf("NextRetry = %v, want %v (third attempt)", got, want)
	}
	if got, want := p.NextRetry(nil), clk.Now().UTC().Add(time.Second); !got.Equal(want) {
		t.Fatalf("NextRetry(nil) = %v, want first-attempt delay", got)
	}
}

func TestRetryPolicy_JitterStaysWithinBounds(t *testing.T) {
	t.Parallel()
	p, err := NewRetryPolicy(RetryOptions{Base: 10 * time.Second, Max: 15 * time.Second, Jitter: 0.5}, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	p.unit = func() float64 { return 0 }
	if got := p.jitter(10 * time.Second); got != 5*time.Second {
		t.Errorf("jitter low = %v, want 5s", got)
	}
	p.unit = func() float64 { return 0.5 }
	if got := p.jitter(10 * time.Second); got != 10*time.Second {
		t.Errorf("jitter centre = %v, want 10s", got)
	}
	p.unit = func() float64 { return 0.999 }
	if got := p.jitter(12 * time.Second); got != 15*time.Second {
		t.Errorf("jitter high = %v, want cap 15s", got)
	}
}

func TestCryptoUnit_InRange(t *testing.T) {
	t.Parallel()
	for range 1000 {
		if u := cryptoUnit(); u < 0 || u >= 1 {
			t.Fatalf("cryptoUnit = %v, want [0,1)", u)
		}
	}
}

func TestOptionsValidate_RejectsBadStopAndRetry(t *testing.T) {
	t.Parallel()
	if err := (Options{Stop: StopOptions{Soft: -1}}).validate(); err == nil {
		t.Error("validate accepted negative stop.soft")
	}
	if err := (Options{Retry: RetryOptions{Base: time.Second, Jitter: 2}}).validate(); err == nil {
		t.Error("validate accepted jitter 2")
	}
	if err := (Options{}).validate(); err != nil {
		t.Errorf("validate zero options: %v", err)
	}
}
