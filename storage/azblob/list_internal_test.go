// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/storage"
)

// listBlobsXML renders a List Blobs response holding names and, when set,
// a continuation marker.
func listBlobsXML(names []string, marker string) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults ContainerName="media"><Blobs>`)
	for _, name := range names {
		fmt.Fprintf(&body, "<Blob><Name>%s</Name><Properties><Content-Length>1</Content-Length></Properties></Blob>", name)
	}
	fmt.Fprintf(&body, "</Blobs><NextMarker>%s</NextMarker></EnumerationResults>", marker)
	return body.String()
}

func TestList_SendsStartFromAndSkipsEqualBlob(t *testing.T) {
	t.Parallel()
	var query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.Query())
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(listBlobsXML([]string{"a/2", "a/3", "a/4"}, "")))
	}))
	t.Cleanup(srv.Close)

	b := sharedKeyBucket(t, srv.URL+"/acct/", clockwork.NewRealClock())
	objects, err := b.List(context.Background(), storage.ListOptions{Prefix: "a/", StartAfter: "a/2", Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objects) != 2 || objects[0].Key != "a/3" || objects[1].Key != "a/4" {
		t.Fatalf("List = %+v; want a/3, a/4", objects)
	}
	sent, ok := query.Load().(url.Values)
	if !ok || sent.Get("startFrom") != "a/2" || sent.Get("maxresults") != "3" {
		t.Fatalf("query = %v; want startFrom=a/2 and maxresults=3", sent)
	}
}

func TestListedObjects_RejectsBlobsBeforeStartAfter(t *testing.T) {
	t.Parallel()
	items := []*container.BlobItem{{Name: new("a/1")}, {Name: new("a/3")}}
	_, err := listedObjects(items, storage.ListOptions{StartAfter: "a/2", Limit: 10})
	if !errors.Is(err, storage.ErrListOrder) {
		t.Fatalf("listedObjects error = %v; want ErrListOrder", err)
	}
}

func TestList_ContinuesPastEmptyPagesWithinBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		emptyPages int32
		wantErr    bool
	}{
		{name: "two empty pages", emptyPages: 2},
		{name: "never filled", emptyPages: maxEmptyListPages, wantErr: true},
	} {
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/xml")
			if requests.Add(1) <= tc.emptyPages {
				_, _ = w.Write([]byte(listBlobsXML(nil, "more")))
				return
			}
			_, _ = w.Write([]byte(listBlobsXML([]string{"k"}, "more")))
		}))
		b := sharedKeyBucket(t, srv.URL+"/acct/", clockwork.NewRealClock())
		objects, err := b.List(context.Background(), storage.ListOptions{})
		srv.Close()
		if tc.wantErr != (err != nil) || (!tc.wantErr && len(objects) != 1) {
			t.Errorf("%s: List = %+v, %v after %d requests", tc.name, objects, err, requests.Load())
		}
		if want := min(tc.emptyPages+1, maxEmptyListPages); requests.Load() != want {
			t.Errorf("%s: %d requests; want %d", tc.name, requests.Load(), want)
		}
	}
}

func TestList_StartAfterRejectedOrResolvedWithoutIO(t *testing.T) {
	t.Parallel()
	b := sharedKeyBucket(t, "http://127.0.0.1:1/acct/", clockwork.NewRealClock())
	ctx := context.Background()
	if _, err := b.List(ctx, storage.ListOptions{StartAfter: "a//b"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("List unsafe StartAfter = %v; want ErrUnsafeKey", err)
	}
	objects, err := b.List(ctx, storage.ListOptions{Prefix: "a/", StartAfter: "b"})
	if err != nil || len(objects) != 0 {
		t.Fatalf("List past prefix range = %+v, %v; want empty without I/O", objects, err)
	}
}
