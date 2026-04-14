// Package meilisearch implements the [search.Backend] interface against
// a Meilisearch cluster.
//
// Raw HTTP — we don't pull in meilisearch/meilisearch-go because the
// cluster's REST API is small and the SDK adds surface we don't need.
//
// Usage:
//
//	b, err := meilisearch.NewBackend(meilisearch.Options{
//	    URL:    "http://localhost:7700",
//	    APIKey: os.Getenv("MEILI_MASTER_KEY"),
//	})
package meilisearch

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

// Options configures the Meilisearch backend.
type Options struct {
	// URL is the Meilisearch base URL. Required.
	URL string `koanf:"url"`
	// APIKey is sent as Bearer token. Required for protected instances.
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

// NewBackend returns a Meilisearch backend.
func NewBackend(opts Options) (*Backend, error) {
	if opts.URL == "" {
		return nil, errors.New("search/meilisearch: url is required")
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Backend{base: strings.TrimRight(opts.URL, "/"), key: opts.APIKey, hc: hc}, nil
}

// CreateCollection implements [search.Indexer]. Meilisearch infers its
// schema from documents, but the schema's Sort/Facet hints are mapped
// to `sortableAttributes` + `filterableAttributes` via a settings
// update.
func (b *Backend) CreateCollection(ctx context.Context, s search.Schema) error {
	// Create index (idempotent: Meili returns the existing index on conflict).
	body := map[string]any{"uid": s.Name, "primaryKey": "id"}
	if err := b.do(ctx, http.MethodPost, "/indexes", body, nil); err != nil {
		// Accept "index already exists" as idempotent.
		if !strings.Contains(err.Error(), "already_exists") {
			return err
		}
	}

	var filterable, sortable []string
	for _, f := range s.Fields {
		if f.Facet {
			filterable = append(filterable, f.Name)
		}
		if f.Sort {
			sortable = append(sortable, f.Name)
		}
	}

	if len(filterable) > 0 {
		if err := b.do(ctx, http.MethodPut, "/indexes/"+url.PathEscape(s.Name)+"/settings/filterable-attributes", filterable, nil); err != nil {
			return err
		}
	}
	if len(sortable) > 0 {
		if err := b.do(ctx, http.MethodPut, "/indexes/"+url.PathEscape(s.Name)+"/settings/sortable-attributes", sortable, nil); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCollection implements [search.Indexer].
func (b *Backend) DeleteCollection(ctx context.Context, name string) error {
	return b.do(ctx, http.MethodDelete, "/indexes/"+url.PathEscape(name), nil, nil)
}

// Index implements [search.Indexer]. Uses Meili's documents POST,
// which upserts by primary key.
func (b *Backend) Index(ctx context.Context, collection string, docs []search.Document) error {
	if len(docs) == 0 {
		return nil
	}
	path := "/indexes/" + url.PathEscape(collection) + "/documents"
	return b.do(ctx, http.MethodPost, path, docs, nil)
}

// Delete implements [search.Indexer].
func (b *Backend) Delete(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	path := "/indexes/" + url.PathEscape(collection) + "/documents/delete-batch"
	return b.do(ctx, http.MethodPost, path, ids, nil)
}

// Search implements [search.Searcher].
func (b *Backend) Search(ctx context.Context, collection string, q search.Query) (search.Results, error) {
	body := map[string]any{"q": q.Q}
	if q.RawFilter != "" {
		body["filter"] = q.RawFilter
	} else if len(q.Filters) > 0 {
		body["filter"] = filtersToMeili(q.Filters)
	}
	if q.SortBy != "" {
		body["sort"] = strings.Split(q.SortBy, ",")
	}
	if q.Limit > 0 {
		body["limit"] = q.Limit
	}
	if q.Offset > 0 {
		body["offset"] = q.Offset
	}
	if len(q.Fields) > 0 {
		body["attributesToSearchOn"] = q.Fields
	}

	var out meiliSearchResponse
	path := "/indexes/" + url.PathEscape(collection) + "/search"
	if err := b.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return search.Results{}, err
	}
	hits := make([]search.Hit, 0, len(out.Hits))
	for _, h := range out.Hits {
		hits = append(hits, search.Hit{Document: search.Document(h)})
	}
	return search.Results{
		Hits:  hits,
		Total: out.EstimatedTotalHits,
		Took:  out.ProcessingTimeMs,
	}, nil
}

func (b *Backend) do(ctx context.Context, method, path string, body, dst any) error {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("search/meilisearch: marshal: %w", err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, r)
	if err != nil {
		return fmt.Errorf("search/meilisearch: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.key != "" {
		req.Header.Set("Authorization", "Bearer "+b.key)
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return fmt.Errorf("search/meilisearch: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("search/meilisearch: status %d: %s", resp.StatusCode, raw)
	}
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("search/meilisearch: decode: %w", err)
		}
	}
	return nil
}

func filtersToMeili(f map[string]any) string {
	parts := make([]string, 0, len(f))
	for k, v := range f {
		switch val := v.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s = %q", k, val))
		case bool:
			parts = append(parts, k+" = "+strconv.FormatBool(val))
		case int:
			parts = append(parts, k+" = "+strconv.Itoa(val))
		case int64:
			parts = append(parts, k+" = "+strconv.FormatInt(val, 10))
		case float64:
			parts = append(parts, k+" = "+strconv.FormatFloat(val, 'f', -1, 64))
		default:
			parts = append(parts, fmt.Sprintf("%s = %v", k, v))
		}
	}
	return strings.Join(parts, " AND ")
}

type meiliSearchResponse struct {
	Hits               []map[string]any `json:"hits"`
	EstimatedTotalHits int64            `json:"estimatedTotalHits"`
	ProcessingTimeMs   int              `json:"processingTimeMs"`
}
