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
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	StatusUnknown Status = "unknown"
)

// CheckFunc is the user-supplied evaluation. Return nil for "up"; a
// non-nil error for "down" with the message surfaced.
type CheckFunc func(ctx context.Context) error

// Check describes a single registered check.
type Check struct {
	Name string
	Fn   CheckFunc
}

// Result is the latest state of a check.
type Result struct {
	Name    string    `json:"name"`
	Status  Status    `json:"status"`
	Message string    `json:"message,omitempty"`
	Latency string    `json:"latency,omitempty"`
	At      time.Time `json:"at"`
}

// Registry holds the checks + caches the last result. Thread-safe.
type Registry struct {
	mu      sync.RWMutex
	checks  []Check
	results map[string]Result
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

// Run evaluates every check serially, refreshing the cached results.
// Returns the fresh result set. ctx cancellation interrupts in-flight checks.
func (r *Registry) Run(ctx context.Context) []Result {
	r.mu.RLock()
	checks := append([]Check(nil), r.checks...)
	r.mu.RUnlock()

	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, r.runOne(ctx, c))
	}
	r.mu.Lock()
	for _, res := range out {
		r.results[res.Name] = res
	}
	r.mu.Unlock()
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
	if err != nil {
		return Result{
			Name:    c.Name,
			Status:  StatusDown,
			Message: err.Error(),
			Latency: latency.String(),
			At:      r.clk.Now(),
		}
	}
	return Result{
		Name:    c.Name,
		Status:  StatusUp,
		Latency: latency.String(),
		At:      r.clk.Now(),
	}
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

func overallStatus(results []Result) string {
	for _, r := range results {
		if r.Status == StatusDown {
			return string(StatusDown)
		}
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
