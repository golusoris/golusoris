// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package typesense_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	httpclient "github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/search"
	"github.com/golusoris/golusoris/search/typesense"
)

func TestSearch_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-key", r.Header.Get("X-Typesense-Api-Key"))
		switch {
		case strings.HasPrefix(r.URL.Path, "/collections/products/documents/search"):
			require.Equal(t, "shoe", r.URL.Query().Get("q"))
			require.Equal(t, "name", r.URL.Query().Get("query_by"))
			require.Equal(t, "brand:=`nike`", r.URL.Query().Get("filter_by"))
			_, _ = w.Write([]byte(`{
				"found": 1, "page": 1, "search_time_ms": 4,
				"hits": [{"document":{"id":"1","name":"nike shoe"},"text_match":100}]
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "test-key"})
	require.NoError(t, err)

	res, err := b.Search(context.Background(), "products", search.Query{
		Q: "shoe", Fields: []string{"name"},
		Filters: map[string]any{"brand": "nike"},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Total)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "nike shoe", res.Hits[0].Document["name"])
}

func TestIndex_UsesImportEndpoint(t *testing.T) {
	t.Parallel()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Contains(t, r.URL.RawQuery, "action=upsert")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"success\":true}\n{\"success\":true}\n"))
	}))
	t.Cleanup(srv.Close)

	b, _ := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, b.Index(context.Background(), "c", []search.Document{
		{"id": "1", "name": "a"},
		{"id": "2", "name": "b"},
	}))
	require.Contains(t, gotBody, `"name":"a"`)
	require.Contains(t, gotBody, `"name":"b"`)
}

func TestIndex_ReportsPerDocumentImportFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"success\":true}\n{\"success\":false,\"error\":\"schema mismatch\"}\n"))
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	err = b.Index(t.Context(), "c", []search.Document{{"id": "1"}, {"id": "2"}})
	require.ErrorContains(t, err, "document 1")
	require.ErrorContains(t, err, "schema mismatch")
}

func TestIndex_RejectsIncompleteImportResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"success\":true}\n"))
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	err = b.Index(t.Context(), "c", []search.Document{{"id": "1"}, {"id": "2"}})
	require.ErrorContains(t, err, "returned 1 results for 2 documents")
}

func TestIndex_RejectsExtraImportResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"success\":true}\n{\"success\":true}\n"))
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	err = b.Index(t.Context(), "c", []search.Document{{"id": "1"}})
	require.ErrorContains(t, err, "returned more than 1 results")
}

func TestIndex_ImportResponseSizeBoundary(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"success":true}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	exact, err := typesense.NewBackend(typesense.Options{
		URL: srv.URL, APIKey: "k", MaxResponseBytes: int64(len(payload)),
	})
	require.NoError(t, err)
	require.NoError(t, exact.Index(t.Context(), "c", []search.Document{{"id": "1"}}))

	over, err := typesense.NewBackend(typesense.Options{
		URL: srv.URL, APIKey: "k", MaxResponseBytes: int64(len(payload) - 1),
	})
	require.NoError(t, err)
	err = over.Index(t.Context(), "c", []search.Document{{"id": "1"}})
	require.ErrorIs(t, err, httpclient.ErrBodyTooLarge)
}

func TestCreateCollection_IdempotentOn409(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"A collection with name ` + "`" + `x` + "`" + ` already exists"}`))
	}))
	t.Cleanup(srv.Close)

	b, _ := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, b.CreateCollection(context.Background(), search.Schema{
		Name:   "x",
		Fields: []search.Field{{Name: "name", Type: search.FieldTypeString}},
	}))
}

func TestNewBackend_RequiresURLAndKey(t *testing.T) {
	t.Parallel()
	_, err := typesense.NewBackend(typesense.Options{APIKey: "k"})
	require.Error(t, err)
	_, err = typesense.NewBackend(typesense.Options{URL: "http://x"})
	require.Error(t, err)
	_, err = typesense.NewBackend(typesense.Options{URL: "http://x", APIKey: "k", MaxResponseBytes: -1})
	require.Error(t, err)
}

func TestSearch_ResponseSizeBoundary(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"found":0,"page":1,"search_time_ms":1,"hits":[]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	exact, err := typesense.NewBackend(typesense.Options{
		URL: srv.URL, APIKey: "k", MaxResponseBytes: int64(len(payload)),
	})
	require.NoError(t, err)
	_, err = exact.Search(t.Context(), "products", search.Query{Fields: []string{"name"}})
	require.NoError(t, err)

	over, err := typesense.NewBackend(typesense.Options{
		URL: srv.URL, APIKey: "k", MaxResponseBytes: int64(len(payload) - 1),
	})
	require.NoError(t, err)
	_, err = over.Search(t.Context(), "products", search.Query{Fields: []string{"name"}})
	if !errors.Is(err, httpclient.ErrBodyTooLarge) {
		t.Fatalf("overflow error = %v, want ErrBodyTooLarge", err)
	}
}

// compile-time: Backend satisfies search.Backend.
var _ search.Backend = (*typesense.Backend)(nil)

// Avoid unused json import when no decode test exercises it.
var _ = json.Marshal

func TestDeleteCollection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/collections/mycol", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	b, _ := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, b.DeleteCollection(context.Background(), "mycol"))
}

func TestDelete_SingleDoc(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	b, _ := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, b.Delete(context.Background(), "col", []string{"doc-1"}))
	require.Equal(t, "/collections/col/documents/doc-1", gotPath)
}

func TestSearch_WithFilters(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits":           []any{},
			"found":          0,
			"search_time_ms": 1,
		})
	}))
	t.Cleanup(srv.Close)

	b, _ := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	results, err := b.Search(context.Background(), "c", search.Query{
		Q:       "hello",
		Filters: map[string]any{"active": true},
	})
	require.NoError(t, err)
	require.NotNil(t, results)
}

func TestSearch_EscapesAndOrdersStructuredFilters(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t,
			"active:=true && brand:=`nike || active:=true` && note:=`O\\`Series` && rank:=3",
			r.URL.Query().Get("filter_by"),
		)
		_, _ = w.Write([]byte(`{"hits":[],"found":0,"page":1,"search_time_ms":1}`))
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	_, err = b.Search(t.Context(), "c", search.Query{
		Fields: []string{"name"},
		Filters: map[string]any{
			"rank":   3,
			"note":   "O`Series",
			"brand":  "nike || active:=true",
			"active": true,
		},
	})
	require.NoError(t, err)
}

func TestSearch_RejectsInvalidStructuredFilters(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"hits":[],"found":0}`))
	}))
	t.Cleanup(srv.Close)

	b, err := typesense.NewBackend(typesense.Options{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	for _, filters := range []map[string]any{
		{"brand || active": "nike"},
		{"brand": []string{"nike"}},
	} {
		_, err = b.Search(t.Context(), "c", search.Query{Fields: []string{"name"}, Filters: filters})
		require.Error(t, err)
	}
	require.Zero(t, requests.Load())
}
