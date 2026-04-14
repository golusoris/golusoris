// Package load provides load-testing helpers backed by tsenart/vegeta.
//
// Load tests are opt-in: guard them with testing.Short() or a custom flag so
// they don't run in normal CI.
//
// Usage:
//
//	func TestMyEndpoint_Load(t *testing.T) {
//	    if testing.Short() {
//	        t.Skip("load test skipped in short mode")
//	    }
//	    metrics := load.Attack(t, load.Options{
//	        Targeter: load.GET("http://localhost:8080/health"),
//	        Rate:     vegeta.Rate{Freq: 50, Per: time.Second},
//	        Duration: 5 * time.Second,
//	    })
//	    load.Assert(t, metrics,
//	        load.MaxErrorRate(0.01),
//	        load.MaxP99(100*time.Millisecond),
//	    )
//	}
package load

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
)

// Options configures a vegeta attack.
type Options struct {
	// Targeter provides requests for each attack tick.
	// Use [GET] or [POST] for simple cases.
	Targeter vegeta.Targeter
	// Rate is the request rate (e.g. vegeta.Rate{Freq: 50, Per: time.Second}).
	Rate vegeta.Pacer
	// Duration is how long the attack runs.
	Duration time.Duration
	// Name labels the attack; defaults to t.Name().
	Name string
}

// GET returns a Targeter that always sends a GET request to url.
func GET(url string) vegeta.Targeter {
	return vegeta.NewStaticTargeter(vegeta.Target{
		Method: http.MethodGet,
		URL:    url,
	})
}

// POST returns a Targeter that always sends a POST request to url with
// the given Content-Type and body bytes.
func POST(url, contentType string, body []byte) vegeta.Targeter {
	return vegeta.NewStaticTargeter(vegeta.Target{
		Method: http.MethodPost,
		URL:    url,
		Header: http.Header{"Content-Type": {contentType}},
		Body:   body,
	})
}

// ConstantRate is a convenience wrapper around vegeta.Rate for a fixed
// requests-per-second rate.
func ConstantRate(rps int) vegeta.Rate {
	return vegeta.Rate{Freq: rps, Per: time.Second}
}

// Attack runs a vegeta attack with opts and returns the aggregated Metrics.
// The test is not failed here — use [Assert] for threshold checks.
func Attack(t *testing.T, opts Options) *vegeta.Metrics {
	t.Helper()
	name := opts.Name
	if name == "" {
		name = t.Name()
	}
	attacker := vegeta.NewAttacker()
	var m vegeta.Metrics
	for res := range attacker.Attack(opts.Targeter, opts.Rate, opts.Duration, name) {
		m.Add(res)
	}
	m.Close()
	return &m
}

// Check is a predicate over Metrics. Return a non-empty failure message
// to indicate that the threshold was violated.
type Check func(*vegeta.Metrics) string

// MaxErrorRate returns a Check that fails when the non-success fraction
// exceeds max. max is a fraction in [0,1] (e.g. 0.01 = 1%).
func MaxErrorRate(max float64) Check {
	return func(m *vegeta.Metrics) string {
		errRate := 1 - m.Success
		if errRate > max {
			return fmt.Sprintf("error rate %.2f%% exceeds max %.2f%%", errRate*100, max*100)
		}
		return ""
	}
}

// MaxP99 returns a Check that fails when the 99th percentile latency
// exceeds limit.
func MaxP99(limit time.Duration) Check {
	return func(m *vegeta.Metrics) string {
		if p99 := m.Latencies.P99; p99 > limit {
			return fmt.Sprintf("p99 %s exceeds max %s", p99, limit)
		}
		return ""
	}
}

// MaxMean returns a Check that fails when the mean latency exceeds limit.
func MaxMean(limit time.Duration) Check {
	return func(m *vegeta.Metrics) string {
		if mean := m.Latencies.Mean; mean > limit {
			return fmt.Sprintf("mean %s exceeds max %s", mean, limit)
		}
		return ""
	}
}

// MinThroughput returns a Check that fails when the achieved throughput
// (req/s) falls below min.
func MinThroughput(min float64) Check {
	return func(m *vegeta.Metrics) string {
		if m.Throughput < min {
			return fmt.Sprintf("throughput %.2f req/s below min %.2f req/s", m.Throughput, min)
		}
		return ""
	}
}

// Assert applies all checks to m and calls t.Errorf for each failure.
func Assert(t *testing.T, m *vegeta.Metrics, checks ...Check) {
	t.Helper()
	for _, c := range checks {
		if msg := c(m); msg != "" {
			t.Errorf("load: %s", msg)
		}
	}
}
