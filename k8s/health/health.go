// Package health serves the canonical Kubernetes probe endpoints —
// /livez, /readyz, /startupz — backed by a [statuspage.Registry].
//
// Apps register checks once on the shared Registry and tag them per
// purpose:
//
//	reg.Register(statuspage.Check{
//	    Name: "db",
//	    Tags: []string{health.TagReadiness, health.TagStartup},
//	    Fn:   func(ctx context.Context) error { return pool.Ping(ctx) },
//	})
//
// Probe semantics:
//
//	/livez    runs only checks tagged "liveness"   (process not deadlocked)
//	/readyz   runs only checks tagged "readiness"  (deps are reachable)
//	/startupz runs only checks tagged "startup"    (init complete)
//
// Untagged checks appear on /status (full registry view) but never on
// the probe endpoints — this prevents an expensive diagnostic from
// blocking k8s probes.
//
// Probe responses are intentionally terse — k8s only inspects the status
// code. Append `?verbose=1` for a JSON dump of the per-check results.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/observability/statuspage"
)

// Tag constants used by the probe handlers.
const (
	TagLiveness  = "liveness"
	TagReadiness = "readiness"
	TagStartup   = "startup"
)

// Mount attaches /livez, /readyz, /startupz to r, all backed by reg.
func Mount(r chi.Router, reg *statuspage.Registry) {
	r.Get("/livez", probeHandler(reg, TagLiveness))
	r.Get("/readyz", probeHandler(reg, TagReadiness))
	r.Get("/startupz", probeHandler(reg, TagStartup))
}

// MountMux attaches /livez, /readyz, /startupz to a net/http mux — for apps
// that don't use chi.
func MountMux(mux *http.ServeMux, reg *statuspage.Registry) {
	mux.Handle("/livez", LivezHandler(reg))
	mux.Handle("/readyz", ReadyzHandler(reg))
	mux.Handle("/startupz", StartupzHandler(reg))
}

// LivezHandler returns a handler for the liveness probe.
func LivezHandler(reg *statuspage.Registry) http.HandlerFunc {
	return probeHandler(reg, TagLiveness)
}

// ReadyzHandler returns a handler for the readiness probe.
func ReadyzHandler(reg *statuspage.Registry) http.HandlerFunc {
	return probeHandler(reg, TagReadiness)
}

// StartupzHandler returns a handler for the startup probe.
func StartupzHandler(reg *statuspage.Registry) http.HandlerFunc {
	return probeHandler(reg, TagStartup)
}

func probeHandler(reg *statuspage.Registry, tag string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := reg.RunTagged(r.Context(), tag)
		ok := serving(results)
		status := http.StatusOK
		body := "ok\n"
		if !ok {
			status = http.StatusServiceUnavailable
			body = "not ok\n"
		}
		if r.URL.Query().Get("verbose") == "1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(struct {
				Status string              `json:"status"`
				Tag    string              `json:"tag"`
				Checks []statuspage.Result `json:"checks"`
			}{
				Status: overall(results),
				Tag:    tag,
				Checks: results,
			})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// serving reports whether the probe should return 200: every check is up or
// degraded (degraded still serves). Any down or unknown check fails the probe.
func serving(results []statuspage.Result) bool {
	for _, r := range results {
		if r.Status != statuspage.StatusUp && r.Status != statuspage.StatusDegraded {
			return false
		}
	}
	return true
}

// overall summarizes the probe results: down if any check is down/unknown;
// else degraded if any is degraded; else up.
func overall(results []statuspage.Result) string {
	degraded := false
	for _, r := range results {
		switch r.Status {
		case statuspage.StatusDown, statuspage.StatusUnknown:
			return "down"
		case statuspage.StatusDegraded:
			degraded = true
		case statuspage.StatusUp:
			// healthy — no change
		}
	}
	if degraded {
		return "degraded"
	}
	return "up"
}

// StartupGate is a one-shot latch: its [Check] fails until [StartupGate.MarkComplete]
// is called, then passes forever. Register the Check so /startupz flips to 200
// once the app signals startup is done (caches warmed, migrations applied, …).
type StartupGate struct {
	done atomic.Bool
}

// NewStartupGate returns a gate that is initially incomplete.
func NewStartupGate() *StartupGate { return &StartupGate{} }

// MarkComplete latches the gate complete (idempotent).
func (g *StartupGate) MarkComplete() { g.done.Store(true) }

// Done reports whether the gate has been marked complete.
func (g *StartupGate) Done() bool { return g.done.Load() }

// Check returns a startup-tagged [statuspage.Check] that is down until
// [StartupGate.MarkComplete] is called.
func (g *StartupGate) Check(name string) statuspage.Check {
	return statuspage.Check{
		Name: name,
		Tags: []string{TagStartup},
		Fn: func(context.Context) error {
			if g.done.Load() {
				return nil
			}
			return errors.New("startup not complete")
		},
	}
}
