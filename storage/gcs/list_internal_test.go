// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	gstorage "cloud.google.com/go/storage"
	"github.com/jonboulle/clockwork"
	"google.golang.org/api/option"

	"github.com/golusoris/golusoris/storage"
)

// stubListBucket serves every object listing with names and records the
// last query string.
func stubListBucket(t *testing.T, names []string, query *atomic.Value) *Bucket {
	t.Helper()
	items := make([]map[string]string, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]string{"name": name, "size": "1"})
	}
	body, err := json.Marshal(map[string]any{"kind": "storage#objects", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	client, err := gstorage.NewClient(context.Background(),
		option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return newBucket(client, &urlSigner{}, Options{Bucket: "media"}, clockwork.NewRealClock())
}

func TestList_SendsStartOffsetAndSkipsEqualObject(t *testing.T) {
	t.Parallel()
	var query atomic.Value
	b := stubListBucket(t, []string{"a/2", "a/3", "a/4"}, &query)
	objects, err := b.List(context.Background(), storage.ListOptions{Prefix: "a/", StartAfter: "a/2", Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objects) != 2 || objects[0].Key != "a/3" || objects[1].Key != "a/4" {
		t.Fatalf("List = %+v; want a/3, a/4", objects)
	}
	sent, ok := query.Load().(url.Values)
	if !ok || sent.Get("startOffset") != "a/2" || sent.Get("maxResults") != "3" {
		t.Fatalf("query = %v; want startOffset=a/2 and maxResults=3", sent)
	}
}

func TestList_RejectsObjectsBeforeStartAfter(t *testing.T) {
	t.Parallel()
	var query atomic.Value
	b := stubListBucket(t, []string{"a/1", "a/3"}, &query)
	if _, err := b.List(context.Background(), storage.ListOptions{StartAfter: "a/2"}); !errors.Is(err, storage.ErrListOrder) {
		t.Fatalf("List error = %v; want ErrListOrder", err)
	}
}

func TestList_StartAfterRejectedOrResolvedWithoutIO(t *testing.T) {
	t.Parallel()
	b := offlineBucket(t, &urlSigner{})
	ctx := context.Background()
	if _, err := b.List(ctx, storage.ListOptions{StartAfter: "a//b"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("List unsafe StartAfter = %v; want ErrUnsafeKey", err)
	}
	objects, err := b.List(ctx, storage.ListOptions{Prefix: "a/", StartAfter: "b"})
	if err != nil || len(objects) != 0 {
		t.Fatalf("List past prefix range = %+v, %v; want empty without I/O", objects, err)
	}
}
