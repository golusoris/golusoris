// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pipeline

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golusoris/golusoris/media/img"
	"github.com/golusoris/golusoris/storage"
)

// tokenFromRequest extracts the signed token from the request. It prefers a chi
// URL param named "signed" (set when mounted as "/img/{signed}"), falling back
// to the last path segment so the handler also works under net/http's mux.
func tokenFromRequest(r *http.Request) string {
	if v := r.PathValue("signed"); v != "" {
		return v
	}
	path := r.URL.Path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// Handler returns an http.Handler that validates the signed token, fetches and
// resizes the source, and serves the variant with the correct Content-Type and
// Cache-Control. It is chi- and net/http-mux-friendly: mount it at
// "/img/{signed}".
//
// Status mapping:
//   - 200: variant served.
//   - 400: malformed token or invalid params ([ErrBadToken], [ErrInvalidParams]).
//   - 403: bad signature or expired token ([ErrBadSignature], [ErrExpired]).
//   - 404: source key not found.
//   - 405: method other than GET or HEAD.
//   - 415: resize backend unavailable (img.ErrCGORequired — no libvips).
//   - 500: source read / resize failure.
func (p *Pipeline) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		token := tokenFromRequest(r)
		key, t, exp, err := p.verify(token)
		if err != nil {
			p.writeVerifyError(w, r, err)
			return
		}

		out, err := p.render(r.Context(), key, t)
		if err != nil {
			p.writeRenderError(w, r, key, err)
			return
		}

		w.Header().Set("Content-Type", out.contentType)
		w.Header().Set("Cache-Control", cacheControlUntil(p.opts.CacheControl, exp.Sub(p.clk.Now())))
		w.Header().Set("Content-Length", strconv.Itoa(len(out.body)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			if _, werr := w.Write(out.body); werr != nil {
				// Headers are already sent; the client most likely went away.
				p.log.DebugContext(r.Context(), "pipeline: write body", slog.String("err", werr.Error()))
			}
		}
	})
}

// cacheControlUntil preserves configured directives while ensuring freshness
// never extends past the authenticated URL lifetime.
func cacheControlUntil(configured string, remaining time.Duration) string {
	maxAge := max(int64(remaining/time.Second), int64(0))
	directives := strings.Split(configured, ",")
	out := make([]string, 0, len(directives)+1)
	hasMaxAge := false
	hasSharedMaxAge := false
	for _, raw := range directives {
		directive := strings.TrimSpace(raw)
		name, value, found := strings.Cut(directive, "=")
		normalizedName := strings.TrimSpace(name)
		if strings.EqualFold(normalizedName, "max-age") {
			if !hasMaxAge {
				out = append(out, boundedCacheAge("max-age", value, found, maxAge))
				hasMaxAge = true
			}
			continue
		}
		if strings.EqualFold(normalizedName, "s-maxage") {
			if !hasSharedMaxAge {
				out = append(out, boundedCacheAge("s-maxage", value, found, maxAge))
				hasSharedMaxAge = true
			}
			continue
		}
		out = append(out, directive)
	}
	if !hasMaxAge {
		out = append(out, "max-age="+strconv.FormatInt(maxAge, 10))
	}
	return strings.Join(out, ", ")
}

func boundedCacheAge(name, value string, hasValue bool, ceiling int64) string {
	want := ceiling
	if hasValue {
		configured, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
		if err == nil && configured >= 0 {
			want = min(want, configured)
		}
	}
	return name + "=" + strconv.FormatInt(want, 10)
}

// writeVerifyError maps a Verify error to the right status. Malformed input is a
// client error (400); a failed auth check (wrong/forged signature or expiry) is
// 403 so a probing client cannot distinguish "almost valid" from "garbage".
func (p *Pipeline) writeVerifyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrBadSignature), errors.Is(err, ErrExpired):
		p.log.WarnContext(r.Context(), "pipeline: token rejected", slog.String("err", err.Error()))
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, ErrBadToken), errors.Is(err, ErrInvalidParams):
		http.Error(w, "bad request", http.StatusBadRequest)
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
	}
}

// writeRenderError maps a Render error: missing source is 404, an unavailable
// CGO backend is 415, anything else is 500.
func (p *Pipeline) writeRenderError(w http.ResponseWriter, r *http.Request, key string, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, img.ErrCGORequired):
		http.Error(w, "image backend unavailable", http.StatusUnsupportedMediaType)
	default:
		p.log.ErrorContext(r.Context(), "pipeline: render failed",
			slog.String("key", key), slog.String("err", err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
