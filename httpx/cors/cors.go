// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cors exposes an rs/cors-backed middleware as a golusoris module.
// Options are koanf-driven; defaults deny cross-origin traffic so CORS is
// opt-in per-app.
//
// Config keys (env: APP_HTTP_CORS_*):
//
//	http.cors.origins   # allowed origins, comma-separated; "*" wildcard
//	http.cors.methods   # allowed methods, comma-separated
//	http.cors.headers   # allowed request headers, comma-separated
//	http.cors.expose    # exposed response headers, comma-separated
//	http.cors.credentials # include Access-Control-Allow-Credentials
//	http.cors.maxage    # preflight cache, e.g. 5m
package cors

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	rscors "github.com/rs/cors"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options tunes the CORS middleware.
type Options struct {
	Origins     []string      `koanf:"origins"`
	Methods     []string      `koanf:"methods"`
	Headers     []string      `koanf:"headers"`
	Expose      []string      `koanf:"expose"`
	Credentials bool          `koanf:"credentials"`
	MaxAge      time.Duration `koanf:"maxage"`
}

// DefaultOptions denies cross-origin traffic. Apps must set Origins
// explicitly to enable CORS.
func DefaultOptions() Options {
	return Options{
		Methods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		Headers: []string{"Authorization", "Content-Type", "X-Request-ID"},
		MaxAge:  5 * time.Minute,
	}
}

// New returns a [middleware.Middleware] with the configured CORS policy.
// It rejects credentialed match-all policies because browsers cannot accept
// Access-Control-Allow-Origin: * together with credentials.
func New(opts Options) (middleware.Middleware, error) {
	maxAge, err := maxAgeSeconds(opts.MaxAge)
	if err != nil {
		return nil, err
	}
	if opts.Credentials && slices.Contains(opts.Origins, "*") {
		return nil, errors.New("httpx/cors: wildcard origin cannot allow credentials")
	}
	if len(opts.Origins) == 0 {
		return func(next http.Handler) http.Handler { return next }, nil
	}
	c := rscors.New(rscors.Options{
		AllowedOrigins:   slices.Clone(opts.Origins),
		AllowedMethods:   slices.Clone(opts.Methods),
		AllowedHeaders:   slices.Clone(opts.Headers),
		ExposedHeaders:   slices.Clone(opts.Expose),
		AllowCredentials: opts.Credentials,
		MaxAge:           maxAge,
	})
	return c.Handler, nil
}

func maxAgeSeconds(maxAge time.Duration) (int, error) {
	if maxAge < 0 {
		return -1, nil
	}
	seconds := int64(maxAge / time.Second)
	maxInt := int64(^uint(0) >> 1)
	if seconds > maxInt {
		return 0, fmt.Errorf("httpx/cors: max age %s overflows int seconds", maxAge)
	}
	return int(seconds), nil
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.cors", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/cors: load options: %w", err)
	}
	return opts, nil
}

// Module provides a CORS [middleware.Middleware]. Apps attach it to their
// chi router via fx.Invoke, typically near the top of the stack so
// preflight OPTIONS short-circuit before auth + rate-limit.
var Module = fx.Module(
	"golusoris.httpx.cors",
	fx.Provide(loadOptions, New),
)
