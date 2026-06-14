package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/search"
)

// errSearcher always fails, to exercise the error-tolerant policy.
type errSearcher struct{ err error }

func (e errSearcher) Search(_ context.Context, _ string, _ search.Query) (search.Results, error) {
	return search.Results{}, e.err
}

// scoredSearcher returns fixed hits, to exercise score-based dedup/merge.
type scoredSearcher struct{ hits []search.Hit }

func (s scoredSearcher) Search(_ context.Context, _ string, _ search.Query) (search.Results, error) {
	return search.Results{Hits: s.hits, Total: int64(len(s.hits)), Took: 7}, nil
}

func hit(id string, score float64) search.Hit {
	return search.Hit{Document: search.Document{"id": id, "name": id}, Score: score}
}

func newMemberWith(t *testing.T, collection string, docs []search.Document) search.Searcher {
	t.Helper()
	s := search.NewMemorySearcher()
	if err := s.Index(context.Background(), collection, docs); err != nil {
		t.Fatalf("index: %v", err)
	}
	return s
}

func TestMultiSearcher_MergesAndDedupes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	a := newMemberWith(t, "products", []search.Document{
		{"id": "1", "name": "Blue Sneaker"},
		{"id": "2", "name": "Blue Boot"},
	})
	b := newMemberWith(t, "products", []search.Document{
		{"id": "2", "name": "Blue Boot"}, // duplicate of a's id=2
		{"id": "3", "name": "Blue Cap"},
	})

	m := search.NewMultiSearcher([]search.Searcher{a, b})
	res, err := m.Search(ctx, "products", search.Query{Q: "blue"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := len(res.Hits); got != 3 {
		t.Fatalf("expected 3 deduped hits, got %d", got)
	}
	if res.Total != 3 {
		t.Fatalf("expected total=3, got %d", res.Total)
	}
	seen := map[string]int{}
	for _, h := range res.Hits {
		seen[h.Document["id"].(string)]++
	}
	for _, id := range []string{"1", "2", "3"} {
		if seen[id] != 1 {
			t.Errorf("id %q appeared %d times, want 1", id, seen[id])
		}
	}
}

func TestMultiSearcher_KeepsBestScoreAndSorts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	low := scoredSearcher{hits: []search.Hit{hit("dup", 10), hit("a", 5)}}
	high := scoredSearcher{hits: []search.Hit{hit("dup", 99), hit("b", 50)}}

	m := search.NewMultiSearcher([]search.Searcher{low, high})
	res, err := m.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(res.Hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(res.Hits))
	}
	// Best-score-wins for the duplicate.
	var dupScore float64
	for _, h := range res.Hits {
		if h.Document["id"] == "dup" {
			dupScore = h.Score
		}
	}
	if dupScore != 99 {
		t.Errorf("expected dup score 99 (best), got %v", dupScore)
	}
	// Sorted by score descending: dup(99), b(50), a(5).
	wantOrder := []string{"dup", "b", "a"}
	for i, want := range wantOrder {
		if got := res.Hits[i].Document["id"]; got != want {
			t.Errorf("hit[%d] = %v, want %v", i, got, want)
		}
	}
	// Took is the max across backends.
	if res.Took != 7 {
		t.Errorf("expected Took=7, got %d", res.Took)
	}
}

func TestMultiSearcher_ToleratesPartialFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	good := newMemberWith(t, "c", []search.Document{{"id": "1", "name": "ok"}})
	bad := errSearcher{err: errors.New("boom")}

	m := search.NewMultiSearcher([]search.Searcher{bad, good})
	res, err := m.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("expected partial success (no error), got %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit from the surviving backend, got %d", len(res.Hits))
	}
}

func TestMultiSearcher_AllFailErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sentinel := errors.New("down")
	m := search.NewMultiSearcher([]search.Searcher{
		errSearcher{err: sentinel},
		errSearcher{err: errors.New("also down")},
	})
	_, err := m.Search(ctx, "c", search.Query{Q: "*"})
	if err == nil {
		t.Fatal("expected error when all backends fail")
	}
	if !errors.Is(err, search.ErrAllBackendsFailed) {
		t.Errorf("expected ErrAllBackendsFailed, got %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped backend error, got %v", err)
	}
}

func TestMultiSearcher_FailFast(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	good := newMemberWith(t, "c", []search.Document{{"id": "1", "name": "ok"}})
	bad := errSearcher{err: errors.New("boom")}

	m := search.NewMultiSearcher([]search.Searcher{good, bad}, search.WithFailFast())
	if _, err := m.Search(ctx, "c", search.Query{Q: "*"}); err == nil {
		t.Fatal("fail-fast: expected error when a backend fails")
	}
}

func TestMultiSearcher_Pagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	a := scoredSearcher{hits: []search.Hit{hit("a", 100), hit("b", 90), hit("c", 80)}}
	b := scoredSearcher{hits: []search.Hit{hit("d", 70), hit("e", 60)}}

	m := search.NewMultiSearcher([]search.Searcher{a, b})
	res, err := m.Search(ctx, "c", search.Query{Q: "*", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 5 {
		t.Fatalf("expected total=5 before pagination, got %d", res.Total)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 paginated hits, got %d", len(res.Hits))
	}
	// Sorted by score desc: a,b,c,d,e — offset 1, limit 2 => b,c.
	if res.Hits[0].Document["id"] != "b" || res.Hits[1].Document["id"] != "c" {
		t.Errorf("unexpected page: got %v,%v", res.Hits[0].Document["id"], res.Hits[1].Document["id"])
	}
}

func TestMultiSearcher_NilBackendsIgnored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	good := newMemberWith(t, "c", []search.Document{{"id": "1", "name": "ok"}})
	m := search.NewMultiSearcher([]search.Searcher{nil, good, nil})
	res, err := m.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(res.Hits))
	}
}

func TestMultiSearcher_EmptyIsNoError(t *testing.T) {
	t.Parallel()
	m := search.NewMultiSearcher(nil)
	res, err := m.Search(context.Background(), "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("expected no error for empty MultiSearcher, got %v", err)
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Fatalf("expected empty results, got %+v", res)
	}
}

func TestMultiSearcher_HitsWithoutIDKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	noID := scoredSearcher{hits: []search.Hit{
		{Document: search.Document{"name": "anon-a"}, Score: 1},
		{Document: search.Document{"name": "anon-b"}, Score: 2},
	}}
	m := search.NewMultiSearcher([]search.Searcher{noID})
	res, err := m.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Hits without an id can't be deduped; both are kept.
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 id-less hits kept, got %d", len(res.Hits))
	}
}

func TestGate_Disabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := newMemberWith(t, "c", []search.Document{{"id": "1", "name": "ok"}})
	gated := search.Gate(backend.(search.Backend), false)

	res, err := gated.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("disabled gate should return empty results, got %d", len(res.Hits))
	}
	// Writes are silently dropped, not errored.
	if err := gated.Index(ctx, "c", []search.Document{{"id": "2"}}); err != nil {
		t.Fatalf("disabled Index should be a no-op, got %v", err)
	}
	if err := gated.CreateCollection(ctx, search.Schema{Name: "c"}); err != nil {
		t.Fatalf("disabled CreateCollection should be a no-op, got %v", err)
	}
	if err := gated.Delete(ctx, "c", []string{"1"}); err != nil {
		t.Fatalf("disabled Delete should be a no-op, got %v", err)
	}
	if err := gated.DeleteCollection(ctx, "c"); err != nil {
		t.Fatalf("disabled DeleteCollection should be a no-op, got %v", err)
	}
}

func TestGate_Enabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	backend := search.NewMemorySearcher()
	if err := backend.Index(ctx, "c", []search.Document{{"id": "1", "name": "ok"}}); err != nil {
		t.Fatalf("index: %v", err)
	}
	gated := search.Gate(backend, true)
	if gated != search.Backend(backend) {
		t.Fatal("enabled gate should pass the backend through unchanged")
	}
	res, err := gated.Search(ctx, "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("enabled gate should reach the real backend, got %d hits", len(res.Hits))
	}
}

func TestGate_NilBackendDisabled(t *testing.T) {
	t.Parallel()
	gated := search.Gate(nil, true)
	res, err := gated.Search(context.Background(), "c", search.Query{Q: "*"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("nil backend should gate to empty results, got %d", len(res.Hits))
	}
}

// Disabled must satisfy the full Backend interface (Indexer + Searcher).
var _ search.Backend = search.Disabled()
