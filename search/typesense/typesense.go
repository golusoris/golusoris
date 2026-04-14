// Package typesense implements the [search.Backend] interface against
// a Typesense cluster.
//
// Raw HTTP — we don't pull in typesense/typesense-go because it brings
// in a fairly heavy generated client while Typesense's REST API is
// small enough to hand-code.
//
// Usage:
//
//	b, err := typesense.NewBackend(typesense.Options{
//	    URL:    "http://localhost:8108",
//	    APIKey: os.Getenv("TYPESENSE_API_KEY"),
//	})
//	_ = b.Index(ctx, "products", []search.Document{{"id":"1","name":"shoe"}})
//	res, _ := b.Search(ctx, "products", search.Query{Q:"shoe", Limit:10})
package typesense

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golusoris/golusoris/search"
)

// Options configures the Typesense backend.
type Options struct {
	// URL is the Typesense cluster base URL (e.g. "http://localhost:8108"
	// or "https://xxx.a1.typesense.net"). Required.
	URL string `koanf:"url"`
	// APIKey is a Typesense API key. Required.
	APIKey string `koanf:"api_key"`
	// HTTPClient is optional; defaults to a 10s-timeout client.
	HTTPClient *http.Client
}

// Backend implements [search.Backend].
type Backend struct {
	base string
	key  string
	hc   *http.Client
}

// NewBackend returns a Typesense backend.
func NewBackend(opts Options) (*Backend, error) {
	if opts.URL == "" {
		return nil, errors.New("search/typesense: url is required")
	}
	if opts.APIKey == "" {
		return nil, errors.New("search/typesense: api_key is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Backend{base: strings.TrimRight(opts.URL, "/"), key: opts.APIKey, hc: hc}, nil
}

// CreateCollection implements [search.Indexer].
func (b *Backend) CreateCollection(ctx context.Context, s search.Schema) error {
	fields := make([]map[string]any, 0, len(s.Fields))
	for _, f := range s.Fields {
		entry := map[string]any{
			"name":  f.Name,
			"type":  string(f.Type),
			"facet": f.Facet,
		}
		if f.Sort {
			entry["sort"] = true
		}
		fields = append(fields, entry)
	}
	body := map[string]any{
		"name":   s.Name,
		"fields": fields,
	}
	if s.DefaultSortField != "" {
		body["default_sorting_field"] = s.DefaultSortField
	}
	if err := b.post(ctx, "/collections", body, nil); err != nil {
		// Typesense returns 409 if the collection exists; treat as idempotent.
		if strings.Contains(err.Error(), "409") {
			return nil
		}
		return err
	}
	return nil
}

// DeleteCollection implements [search.Indexer].
func (b *Backend) DeleteCollection(ctx context.Context, name string) error {
	return b.do(ctx, http.MethodDelete, "/collections/"+url.PathEscape(name), nil, nil)
}

// Index implements [search.Indexer]. Uses Typesense's JSONL import
// endpoint with action=upsert so repeated indexing is idempotent.
func (b *Backend) Index(ctx context.Context, collection string, docs []search.Document) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return fmt.Errorf("search/typesense: encode doc: %w", err)
		}
	}
	path := "/collections/" + url.PathEscape(collection) + "/documents/import?action=upsert"
	req, err := b.newRequest(ctx, http.MethodPost, path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := b.hc.Do(req)
	if err != nil {
		return fmt.Errorf("search/typesense: import: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("search/typesense: import status %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// Delete implements [search.Indexer].
func (b *Backend) Delete(ctx context.Context, collection string, ids []string) error {
	for _, id := range ids {
		path := "/collections/" + url.PathEscape(collection) + "/documents/" + url.PathEscape(id)
		if err := b.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Search implements [search.Searcher].
func (b *Backend) Search(ctx context.Context, collection string, q search.Query) (search.Results, error) {
	v := url.Values{}
	if q.Q == "" {
		v.Set("q", "*")
	} else {
		v.Set("q", q.Q)
	}
	if len(q.Fields) > 0 {
		v.Set("query_by", strings.Join(q.Fields, ","))
	} else {
		// Typesense requires query_by — default to '*' doesn't work;
		// leave to caller via Fields, but provide a fallback.
		v.Set("query_by", "")
	}
	if q.RawFilter != "" {
		v.Set("filter_by", q.RawFilter)
	} else if len(q.Filters) > 0 {
		v.Set("filter_by", filtersToTypesense(q.Filters))
	}
	if q.SortBy != "" {
		v.Set("sort_by", q.SortBy)
	}
	if q.Limit > 0 {
		v.Set("per_page", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		// Typesense uses page (1-indexed) not offset.
		v.Set("page", strconv.Itoa(q.Offset/nonZero(q.Limit, 10)+1))
	}

	path := "/collections/" + url.PathEscape(collection) + "/documents/search?" + v.Encode()
	var out typesenseSearchResponse
	if err := b.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return search.Results{}, err
	}
	hits := make([]search.Hit, 0, len(out.Hits))
	for _, h := range out.Hits {
		hl := map[string]string{}
		for _, hg := range h.Highlights {
			if hg.Snippet != "" {
				hl[hg.Field] = hg.Snippet
			}
		}
		hits = append(hits, search.Hit{
			Document:  h.Document,
			Score:     h.TextMatch,
			Highlight: hl,
		})
	}
	return search.Results{
		Hits:  hits,
		Total: int64(out.Found),
		Page:  out.Page,
		Took:  out.SearchTimeMs,
	}, nil
}

func (b *Backend) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("search/typesense: new request: %w", err)
	}
	req.Header.Set("X-Typesense-Api-Key", b.key)
	return req, nil
}

func (b *Backend) post(ctx context.Context, path string, body, dst any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("search/typesense: marshal: %w", err)
	}
	req, err := b.newRequest(ctx, http.MethodPost, path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return b.exec(req, dst)
}

func (b *Backend) do(ctx context.Context, method, path string, body, dst any) error {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("search/typesense: marshal: %w", err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := b.newRequest(ctx, method, path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return b.exec(req, dst)
}

func (b *Backend) exec(req *http.Request, dst any) error {
	resp, err := b.hc.Do(req) //nolint:gosec // G704 SSRF: URL built from caller-supplied collection; caller owns input trust
	if err != nil {
		return fmt.Errorf("search/typesense: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("search/typesense: status %d: %s", resp.StatusCode, raw)
	}
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("search/typesense: decode: %w", err)
		}
	}
	return nil
}

func filtersToTypesense(f map[string]any) string {
	parts := make([]string, 0, len(f))
	for k, v := range f {
		switch val := v.(type) {
		case string:
			parts = append(parts, k+":="+val)
		case bool:
			parts = append(parts, k+":="+strconv.FormatBool(val))
		case int:
			parts = append(parts, k+":="+strconv.Itoa(val))
		case int64:
			parts = append(parts, k+":="+strconv.FormatInt(val, 10))
		case float64:
			parts = append(parts, k+":="+strconv.FormatFloat(val, 'f', -1, 64))
		default:
			parts = append(parts, fmt.Sprintf("%s:=%v", k, v))
		}
	}
	return strings.Join(parts, " && ")
}

func nonZero(a, fallback int) int {
	if a == 0 {
		return fallback
	}
	return a
}

type typesenseSearchResponse struct {
	Found        int `json:"found"`
	Page         int `json:"page"`
	SearchTimeMs int `json:"search_time_ms"`
	Hits         []struct {
		Document   search.Document `json:"document"`
		TextMatch  float64         `json:"text_match"`
		Highlights []struct {
			Field   string `json:"field"`
			Snippet string `json:"snippet"`
		} `json:"highlights"`
	} `json:"hits"`
}
