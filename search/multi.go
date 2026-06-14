package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrAllBackendsFailed is returned by [MultiSearcher.Search] when every
// backend errored and the tolerance policy did not salvage any results.
var ErrAllBackendsFailed = errors.New("search: all backends failed")

// MultiSearcher fans a [Query] out to N [Searcher]s concurrently and merges
// their [Results] into one. Hits are deduplicated by document ID, keeping the
// hit with the best (highest) score; the merged hits are then sorted by score
// descending so the strongest matches lead.
//
// Per-backend errors are tolerated: a backend that fails is skipped and its
// error recorded. Search only returns an error when every backend failed
// (wrapping [ErrAllBackendsFailed]); a partial failure still yields the hits
// from the backends that succeeded. Set [Options.FailFast] to instead fail the
// whole query as soon as any backend errors.
type MultiSearcher struct {
	backends []Searcher
	failFast bool
}

// MultiOption configures a [MultiSearcher].
type MultiOption func(*MultiSearcher)

// WithFailFast makes the [MultiSearcher] return the first backend error instead
// of tolerating partial failure. Off by default (error-tolerant).
func WithFailFast() MultiOption {
	return func(m *MultiSearcher) { m.failFast = true }
}

// NewMultiSearcher returns a [MultiSearcher] over backends. nil backends are
// ignored, so a gated-off [Searcher] (see [Gate]) can be passed through
// unconditionally.
func NewMultiSearcher(backends []Searcher, opts ...MultiOption) *MultiSearcher {
	kept := make([]Searcher, 0, len(backends))
	for _, b := range backends {
		if b != nil {
			kept = append(kept, b)
		}
	}
	m := &MultiSearcher{backends: kept}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// backendResult pairs a backend's outcome with its index for deterministic
// error reporting.
type backendResult struct {
	idx     int
	results Results
	err     error
}

// Search implements [Searcher]. It queries every backend concurrently and
// merges the results. See [MultiSearcher] for the dedup, scoring, and error
// semantics.
func (m *MultiSearcher) Search(ctx context.Context, collection string, q Query) (Results, error) {
	if len(m.backends) == 0 {
		return Results{}, nil
	}

	out := m.fanOut(ctx, collection, q)

	merged := newHitMerger()
	var took int
	var errs []error
	var succeeded int
	for _, r := range out {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("backend %d: %w", r.idx, r.err))
			continue
		}
		succeeded++
		merged.add(r.results.Hits)
		if r.results.Took > took {
			took = r.results.Took
		}
	}

	if succeeded == 0 {
		return Results{}, fmt.Errorf("%w: %w", ErrAllBackendsFailed, errors.Join(errs...))
	}

	hits := merged.sorted()
	hits = paginate(hits, q.Offset, q.Limit)
	return Results{Hits: hits, Total: int64(merged.len()), Took: took}, nil
}

// fanOut runs every backend's Search concurrently and returns the per-backend
// outcomes in backend order.
func (m *MultiSearcher) fanOut(ctx context.Context, collection string, q Query) []backendResult {
	fanCtx := ctx
	var cancel context.CancelFunc
	if m.failFast {
		fanCtx, cancel = context.WithCancel(ctx)
		defer cancel()
	}

	out := make([]backendResult, len(m.backends))
	var wg sync.WaitGroup
	wg.Add(len(m.backends))
	for i, b := range m.backends {
		go func() {
			defer wg.Done()
			res, err := b.Search(fanCtx, collection, q)
			out[i] = backendResult{idx: i, results: res, err: err}
			if err != nil && m.failFast && cancel != nil {
				cancel()
			}
		}()
	}
	wg.Wait()

	if m.failFast {
		for _, r := range out {
			if r.err != nil {
				return []backendResult{r}
			}
		}
	}
	return out
}

// hitMerger deduplicates hits by document ID, keeping the highest-scoring hit
// per ID. Hits without an ID are kept as-is (they can't be deduped).
type hitMerger struct {
	byID  map[string]Hit
	order []string // insertion order of IDs for stable output
	noID  []Hit
}

func newHitMerger() *hitMerger {
	return &hitMerger{byID: map[string]Hit{}}
}

func (h *hitMerger) add(hits []Hit) {
	for _, hit := range hits {
		id := hitID(hit)
		if id == "" {
			h.noID = append(h.noID, hit)
			continue
		}
		prev, ok := h.byID[id]
		if !ok {
			h.byID[id] = hit
			h.order = append(h.order, id)
			continue
		}
		if hit.Score > prev.Score {
			h.byID[id] = hit
		}
	}
}

func (h *hitMerger) len() int {
	return len(h.order) + len(h.noID)
}

// sorted returns the merged hits ordered by score descending, with ties broken
// by document ID for determinism. Hits without an ID follow, in insertion order.
func (h *hitMerger) sorted() []Hit {
	merged := make([]Hit, 0, h.len())
	for _, id := range h.order {
		merged = append(merged, h.byID[id])
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		return hitID(merged[i]) < hitID(merged[j])
	})
	merged = append(merged, h.noID...)
	return merged
}

// hitID extracts the document's "id" field as a string, or "" if absent.
func hitID(h Hit) string {
	id, _ := h.Document["id"].(string)
	return id
}

// paginate applies offset and limit to an already-merged hit slice. A zero or
// negative limit means no limit.
func paginate(hits []Hit, offset, limit int) []Hit {
	if offset > 0 {
		if offset >= len(hits) {
			return nil
		}
		hits = hits[offset:]
	}
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// --- enabled gate ---

// disabledBackend is a no-op [Backend] for a configured-off search. Search
// returns empty [Results]; every indexing method is a no-op that succeeds. It
// lets callers keep a non-nil [Backend] in the graph without branching on
// whether search is enabled.
type disabledBackend struct{}

// Search implements [Searcher]; it always returns empty results and no error.
func (disabledBackend) Search(_ context.Context, _ string, _ Query) (Results, error) {
	return Results{}, nil
}

// CreateCollection implements [Indexer] as a no-op.
func (disabledBackend) CreateCollection(_ context.Context, _ Schema) error { return nil }

// DeleteCollection implements [Indexer] as a no-op.
func (disabledBackend) DeleteCollection(_ context.Context, _ string) error { return nil }

// Index implements [Indexer] as a no-op.
func (disabledBackend) Index(_ context.Context, _ string, _ []Document) error { return nil }

// Delete implements [Indexer] as a no-op.
func (disabledBackend) Delete(_ context.Context, _ string, _ []string) error { return nil }

// Disabled returns a no-op [Backend] that yields empty results and silently
// drops writes. Use it where a [Backend] is required but search is turned off
// by configuration.
func Disabled() Backend {
	return disabledBackend{}
}

// Gate returns b when enabled, or a no-op [Disabled] backend when not. It lets
// search-backend selection be gated by an "enabled" flag without the caller
// having to nil-check or branch at every call site.
func Gate(b Backend, enabled bool) Backend {
	if enabled && b != nil {
		return b
	}
	return Disabled()
}
