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
	"encoding/json"
	"net/http"

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
		ok := allUp(results)
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
				Status: statusString(ok),
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

func allUp(results []statuspage.Result) bool {
	for _, r := range results {
		if r.Status != statuspage.StatusUp {
			return false
		}
	}
	return true
}

func statusString(ok bool) string {
	if ok {
		return "up"
	}
	return "down"
}
