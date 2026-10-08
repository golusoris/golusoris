// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package meilisearch_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	httpclient "github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/search"
	"github.com/golusoris/golusoris/search/meilisearch"
)

func TestSearch_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/indexes/products/search", r.URL.Path)
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "shoe", got["q"])
		require.Equal(t, `brand = "nike"`, got["filter"])

		_, _ = w.Write([]byte(`{
		  "hits":[{"id":"1","name":"nike shoe"}],
		  "estimatedTotalHits":1,
		  "processingTimeMs":3
		}`))
	}))
	t.Cleanup(srv.Close)

	b, err := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	res, err := b.Search(context.Background(), "products", search.Query{
		Q:       "shoe",
		Filters: map[string]any{"brand": "nike"},
		Limit:   5,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "nike shoe", res.Hits[0].Document["name"])
}

func TestIndex_UsesDocumentsEndpoint(t *testing.T) {
	t.Parallel()
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/indexes/c/documents", r.URL.Path)
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	b, _ := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, b.Index(context.Background(), "c", []search.Document{{"id": "1"}}))
	require.True(t, called)
}

func TestDelete_BatchEndpoint(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/indexes/c/documents/delete-batch", r.URL.Path)
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	b, _ := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, b.Delete(context.Background(), "c", []string{"1", "2"}))
	require.Contains(t, string(gotBody), `"1"`)
	require.Contains(t, string(gotBody), `"2"`)
}

func TestNewBackend_RequiresURL(t *testing.T) {
	t.Parallel()
	_, err := meilisearch.NewBackend(meilisearch.Options{})
	require.Error(t, err)
	_, err = meilisearch.NewBackend(meilisearch.Options{URL: "http://example.test", MaxResponseBytes: -1})
	require.Error(t, err)
}

func TestSearch_ResponseSizeBoundary(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"hits":[],"estimatedTotalHits":0,"processingTimeMs":1}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	exact, err := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL, MaxResponseBytes: int64(len(payload))})
	require.NoError(t, err)
	_, err = exact.Search(t.Context(), "products", search.Query{})
	require.NoError(t, err)

	over, err := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL, MaxResponseBytes: int64(len(payload) - 1)})
	require.NoError(t, err)
	_, err = over.Search(t.Context(), "products", search.Query{})
	require.ErrorIs(t, err, httpclient.ErrBodyTooLarge)
}

var _ search.Backend = (*meilisearch.Backend)(nil)

func TestCreateCollection(t *testing.T) {
	t.Parallel()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 1})
	}))
	t.Cleanup(srv.Close)

	b, _ := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, b.CreateCollection(context.Background(), search.Schema{
		Name: "movies",
		Fields: []search.Field{
			{Name: "genre", Facet: true},
			{Name: "release", Sort: true},
		},
	}))
	require.Contains(t, paths[0], "/indexes")
}

func TestDeleteCollection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/indexes/movies", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"taskUid": 2})
	}))
	t.Cleanup(srv.Close)

	b, _ := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, b.DeleteCollection(context.Background(), "movies"))
}

func TestSearch_WithFilter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits":             []any{},
			"totalHits":        0,
			"processingTimeMs": 1,
		})
	}))
	t.Cleanup(srv.Close)

	b, _ := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	results, err := b.Search(context.Background(), "c", search.Query{
		Q:       "action",
		Filters: map[string]any{"genre": "comedy"},
	})
	require.NoError(t, err)
	require.NotNil(t, results)
}

func TestSearch_EscapesAndOrdersStructuredFilters(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t,
			`active = true AND brand = "nike OR active = true" AND note = "O\"Series" AND rank = 3`,
			body["filter"],
		)
		_, _ = w.Write([]byte(`{"hits":[],"estimatedTotalHits":0,"processingTimeMs":1}`))
	}))
	t.Cleanup(srv.Close)

	b, err := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, err)
	_, err = b.Search(t.Context(), "c", search.Query{Filters: map[string]any{
		"rank":   3,
		"note":   `O"Series`,
		"brand":  "nike OR active = true",
		"active": true,
	}})
	require.NoError(t, err)
}

func TestSearch_RejectsInvalidStructuredFilters(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"hits":[],"estimatedTotalHits":0}`))
	}))
	t.Cleanup(srv.Close)

	b, err := meilisearch.NewBackend(meilisearch.Options{URL: srv.URL})
	require.NoError(t, err)
	for _, filters := range []map[string]any{
		{"brand OR active": "nike"},
		{"brand": []string{"nike"}},
	} {
		_, err = b.Search(t.Context(), "c", search.Query{Filters: filters})
		require.Error(t, err)
	}
	require.Zero(t, requests.Load())
}
