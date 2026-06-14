package search

import (
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Backend selector constants for [Options.Backend].
const (
	backendMemory      = "memory"
	backendTypesense   = "typesense"
	backendMeilisearch = "meilisearch"
	backendPgFTS       = "pgfts"
)

// Options selects and tunes the search backend.
//
// Only the in-process "memory" backend is wired by this module, because the
// typesense/meilisearch/pgfts backends live in their own subpackages with
// separate import graphs (importing them here would create an import cycle:
// the subpackages already import search). Apps that want one of those add it
// explicitly, e.g.:
//
//	fx.New(
//	    golusoris.Core,
//	    search.Module, // provides search.Backend (memory)
//	    fx.Decorate(func(*pgxpool.Pool) search.Backend {
//	        return pgftsBackendAdapter // wraps pgfts.New(pool, …)
//	    }),
//	)
//
// or by supplying the concrete *typesense.Backend / *meilisearch.Backend and
// decorating search.Backend with it.
//
// Config key prefix: search.*
//
//	search.backend = "memory"   # memory|typesense|meilisearch|pgfts
//	search.url     = "..."      # typesense/meilisearch base URL (sub-module)
//	search.api_key = "..."      # typesense/meilisearch API key (sub-module)
type Options struct {
	// Backend selects the implementation: "memory" (default). The
	// "typesense", "meilisearch", and "pgfts" values are recognised so apps
	// can read the same key, but those backends are wired by the app (see
	// the doc comment above), not by this module.
	Backend string `koanf:"backend"`
	// URL is the base URL for the typesense/meilisearch backends. Consumed
	// by those subpackages' own constructors, surfaced here for a single
	// config namespace.
	URL string `koanf:"url"`
	// APIKey authenticates against the typesense/meilisearch backends.
	APIKey string `koanf:"api_key"`
}

func defaultOptions() Options {
	return Options{Backend: backendMemory}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("search", &opts); err != nil {
		return Options{}, fmt.Errorf("search: load options: %w", err)
	}
	if opts.Backend == "" {
		opts.Backend = backendMemory
	}
	return opts, nil
}

// newBackend constructs the configured [Backend]. Only "memory" is wired by
// this module; the external backends must be supplied by the app (see the
// [Options] doc comment), so selecting them here is an explicit error that
// tells the operator where to wire them.
func newBackend(opts Options, logger *slog.Logger) (Backend, error) {
	switch opts.Backend {
	case backendMemory:
		logger.Debug("search: started", slog.String("backend", backendMemory))
		return NewMemorySearcher(), nil
	case backendTypesense, backendMeilisearch, backendPgFTS:
		return nil, fmt.Errorf(
			"search: backend %q is not wired by search.Module; supply it from its subpackage and decorate search.Backend",
			opts.Backend,
		)
	default:
		return nil, fmt.Errorf("search: unknown backend %q", opts.Backend)
	}
}

// Module provides search.Backend to the fx graph (memory backend by default).
var Module = fx.Module("golusoris.search",
	fx.Provide(loadOptions),
	fx.Provide(newBackend),
)
