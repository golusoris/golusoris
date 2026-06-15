// Package statuspage exposes an HTML + JSON "/status" endpoint driven by a
// check registry. Checks are periodic functions reporting up/down + detail;
// the page also shows process uptime.
//
// The check registry is shared with k8s/health (Step 6) so /livez and
// /readyz and /status all read from the same source.
//
// Accept: text/html → renders a small HTML page; Accept: application/json
// or ?format=json → returns JSON. Default is HTML for human browsers.
package statuspage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golusoris/golusoris/clock"
)

// Status is a single check's current state.
type Status string

// Status values.
const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded" // still serving, but an optional dependency is impaired
	StatusDown     Status = "down"
	StatusUnknown  Status = "unknown"
)

// CheckFunc is the user-supplied evaluation. Return nil for "up"; a
// non-nil error for "down" with the message surfaced; or a [Degraded] error
// for "degraded" (still serving).
type CheckFunc func(ctx context.Context) error

// degradedError marks a check degraded (still serving) rather than down.
type degradedError struct{ msg string }

func (e *degradedError) Error() string { return e.msg }

// Degraded returns an error a CheckFunc can return to mark itself
// [StatusDegraded] instead of down — e.g. an optional dependency is disabled
// but the service still serves. A degraded check does NOT fail readiness.
func Degraded(msg string) error { return &degradedError{msg: msg} }

// Check describes a single registered check.
type Check struct {
	Name string
	// Tags categorize the check. k8s/health filters by tag to surface
	// liveness vs readiness vs startup checks on separate endpoints.
	// Untagged checks are visible on /status (full registry view) but
	// excluded from the k8s probe endpoints. Common tag values:
	// "liveness", "readiness", "startup" (defined in k8s/health).
	Tags []string
	Fn   CheckFunc
	// Details, if set, returns structured metadata merged into the check's
	// Result regardless of status (e.g. DB pool stats). Keep it cheap — it
	// runs on every evaluation.
	Details func(ctx context.Context) map[string]any
}

// HasTag reports whether the check carries the given tag.
func (c Check) HasTag(tag string) bool {
	for _, t := range c.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Result is the latest state of a check.
type Result struct {
	Name    string         `json:"name"`
	Status  Status         `json:"status"`
	Message string         `json:"message,omitempty"`
	Latency string         `json:"latency,omitempty"`
	Details map[string]any `json:"details,omitempty"`
	At      time.Time      `json:"at"`
}

// RunHook is invoked after every Run / RunTagged with the freshly-evaluated
// results. Subscribers can mirror results into Prometheus gauges, an OTel
// meter, etc. Hooks run synchronously — keep them quick.
type RunHook func(ctx context.Context, results []Result)

// Registry holds the checks + caches the last result. Thread-safe.
type Registry struct {
	mu      sync.RWMutex
	checks  []Check
	results map[string]Result
	hooks   []RunHook
	started time.Time
	clk     clock.Clock
	timeout time.Duration
}

// NewRegistry returns an empty registry started at clk.Now(). Each check
// runs with a per-call timeout (default 2s).
func NewRegistry(clk clock.Clock) *Registry {
	return &Registry{
		results: make(map[string]Result),
		started: clk.Now(),
		clk:     clk,
		timeout: 2 * time.Second,
	}
}

// Register adds a check. Safe to call at any time.
func (r *Registry) Register(c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks = append(r.checks, c)
}

// OnRun subscribes fn to every Run / RunTagged invocation. Multiple hooks
// run in registration order. Used by k8s/metrics/prom to mirror check
// status into Prometheus gauges.
func (r *Registry) OnRun(fn RunHook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, fn)
}

// Run evaluates every check serially, refreshing the cached results.
// Returns the fresh result set. ctx cancellation interrupts in-flight checks.
func (r *Registry) Run(ctx context.Context) []Result {
	return r.run(ctx, "")
}

// RunTagged evaluates only checks carrying tag, refreshing the cached
// results for that subset. Empty tag is equivalent to [Run].
func (r *Registry) RunTagged(ctx context.Context, tag string) []Result {
	return r.run(ctx, tag)
}

func (r *Registry) run(ctx context.Context, tag string) []Result {
	r.mu.RLock()
	checks := make([]Check, 0, len(r.checks))
	for _, c := range r.checks {
		if tag == "" || c.HasTag(tag) {
			checks = append(checks, c)
		}
	}
	r.mu.RUnlock()

	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, r.runOne(ctx, c))
	}
	r.mu.Lock()
	for _, res := range out {
		r.results[res.Name] = res
	}
	hooks := append([]RunHook(nil), r.hooks...)
	r.mu.Unlock()
	for _, fn := range hooks {
		fn(ctx, out)
	}
	return out
}

// Cached returns the latest known results (whatever Run last wrote). For
// endpoints that must respond instantly even if checks are slow.
func (r *Registry) Cached() []Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Result, 0, len(r.results))
	for _, v := range r.results {
		out = append(out, v)
	}
	return out
}

// Uptime returns the duration since the registry was constructed.
func (r *Registry) Uptime() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clk.Since(r.started)
}

func (r *Registry) runOne(ctx context.Context, c Check) Result {
	start := r.clk.Now()
	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	err := c.Fn(cctx)
	latency := r.clk.Since(start)
	res := Result{
		Name:    c.Name,
		Status:  StatusUp,
		Latency: latency.String(),
		At:      r.clk.Now(),
	}
	if err != nil {
		var de *degradedError
		if errors.As(err, &de) {
			res.Status = StatusDegraded
		} else {
			res.Status = StatusDown
		}
		res.Message = err.Error()
	}
	if c.Details != nil {
		res.Details = c.Details(cctx)
	}
	return res
}

// Handler returns an http.Handler that renders HTML or JSON based on the
// request's Accept header / ?format= query.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		results := r.Run(req.Context())
		if wantsJSON(req) {
			writeJSON(w, results, r.Uptime())
			return
		}
		writeHTML(w, results, r.Uptime())
	})
}

func wantsJSON(r *http.Request) bool {
	if r.URL.Query().Get("format") == "json" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

type jsonPayload struct {
	Status string   `json:"status"`
	Uptime string   `json:"uptime"`
	Checks []Result `json:"checks"`
}

func writeJSON(w http.ResponseWriter, results []Result, uptime time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	payload := jsonPayload{
		Status: overallStatus(results),
		Uptime: uptime.String(),
		Checks: results,
	}
	if payload.Status == string(StatusDown) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeHTML(w http.ResponseWriter, results []Result, uptime time.Duration) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	overall := overallStatus(results)
	if overall == string(StatusDown) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct {
		Overall string
		Uptime  string
		Checks  []Result
	}{Overall: overall, Uptime: uptime.String(), Checks: results}); err != nil {
		http.Error(w, fmt.Sprintf("render: %v", err), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// overallStatus is down if any check is down; else degraded if any is degraded;
// else up. Only "down" maps to a 503 — degraded still serves.
func overallStatus(results []Result) string {
	degraded := false
	for _, r := range results {
		switch r.Status {
		case StatusDown:
			return string(StatusDown)
		case StatusDegraded:
			degraded = true
		case StatusUp, StatusUnknown:
			// up, or not-yet-evaluated on this informational view — neither
			// forces the overall to degraded.
		}
	}
	if degraded {
		return string(StatusDegraded)
	}
	return string(StatusUp)
}

var tmpl = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Status</title>
<style>
body{font-family:system-ui,sans-serif;max-width:720px;margin:3rem auto;padding:0 1rem;color:#333}
h1{margin:0 0 1rem}
.up{color:#1a7}.down{color:#c22}.unknown{color:#888}
table{width:100%;border-collapse:collapse;margin-top:1rem}
td,th{padding:.4rem .6rem;border-bottom:1px solid #eee;text-align:left}
small{color:#888}
</style>
</head>
<body>
<h1 class="{{.Overall}}">System: {{.Overall}}</h1>
<p><small>Uptime: {{.Uptime}}</small></p>
<table>
<thead><tr><th>Check</th><th>Status</th><th>Latency</th><th>Message</th></tr></thead>
<tbody>
{{range .Checks}}
<tr>
<td>{{.Name}}</td>
<td class="{{.Status}}">{{.Status}}</td>
<td>{{.Latency}}</td>
<td>{{.Message}}</td>
</tr>
{{else}}
<tr><td colspan="4"><em>No checks registered.</em></td></tr>
{{end}}
</tbody>
</table>
</body>
</html>
`))
