// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const s3ListResultHead = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>my-bucket</Name>`

// s3ListResult renders a ListObjectsV2 response holding keys.
func s3ListResult(keys []string, truncated bool, token string) string {
	var body strings.Builder
	body.WriteString(s3ListResultHead)
	fmt.Fprintf(&body, "<IsTruncated>%t</IsTruncated>", truncated)
	if token != "" {
		fmt.Fprintf(&body, "<NextContinuationToken>%s</NextContinuationToken>", token)
	}
	for _, key := range keys {
		fmt.Fprintf(&body, "<Contents><Key>%s</Key><Size>1</Size></Contents>", key)
	}
	body.WriteString("</ListBucketResult>")
	return body.String()
}

// s3KeyspaceHandler serves sorted keys honouring prefix, start-after, and
// max-keys like S3 general purpose buckets.
func s3KeyspaceHandler(t *testing.T, keys []string, requests *atomic.Int32) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		query := r.URL.Query()
		maxKeys, err := strconv.Atoi(query.Get("max-keys"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		page := make([]string, 0, maxKeys)
		for _, key := range keys {
			if strings.HasPrefix(key, query.Get("prefix")) && key > query.Get("start-after") {
				page = append(page, key)
			}
		}
		truncated := len(page) > maxKeys
		page = page[:min(maxKeys, len(page))]
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(s3ListResult(page, truncated, "")))
	}
}

func TestS3Bucket_WalkFollowsStartAfter(t *testing.T) {
	t.Parallel()
	keys := make([]string, 0, 2*MaxListLimit+2)
	for i := range 2*MaxListLimit + 1 {
		keys = append(keys, fmt.Sprintf("walk/%04d", i))
	}
	var requests atomic.Int32
	srv := httptest.NewServer(s3KeyspaceHandler(t, append(keys, "zz/other"), &requests))
	t.Cleanup(srv.Close)

	var got []string
	err := Walk(context.Background(), newTestBucket(t, srv), ListOptions{Prefix: "walk/"}, func(obj Object) error {
		got = append(got, obj.Key)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, keys, got)
	require.Equal(t, int32(4), requests.Load(), "three pages plus the empty one")
}

func TestS3Bucket_ListSendsStartAfter(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(s3KeyspaceHandler(t, []string{"a/1", "a/2", "a/3", "b/1"}, &requests))
	t.Cleanup(srv.Close)
	b := newTestBucket(t, srv)
	ctx := context.Background()

	objects, err := b.List(ctx, ListOptions{Prefix: "a/", StartAfter: "a/1"})
	require.NoError(t, err)
	require.Len(t, objects, 2)
	require.Equal(t, "a/2", objects[0].Key)

	objects, err = b.List(ctx, ListOptions{Prefix: "a/", StartAfter: "a/3"})
	require.NoError(t, err)
	require.Empty(t, objects)
	require.Equal(t, int32(2), requests.Load())

	objects, err = b.List(ctx, ListOptions{Prefix: "a/", StartAfter: "b"})
	require.NoError(t, err)
	require.Empty(t, objects)
	_, err = b.List(ctx, ListOptions{StartAfter: "a/../b"})
	require.ErrorIs(t, err, ErrUnsafeKey)
	require.Equal(t, int32(2), requests.Load(), "out-of-range and invalid StartAfter skip the request")
}

func TestS3Bucket_ListRejectsKeysNotAfterStartAfter(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"a", "b"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(s3ListResult([]string{key, "c"}, false, "")))
		}))
		_, err := newTestBucket(t, srv).List(context.Background(), ListOptions{StartAfter: "b"})
		srv.Close()
		require.ErrorIs(t, err, ErrListOrder, "server returned %q for start-after b", key)
	}
}

func TestS3Bucket_ListContinuesPastEmptyTruncatedPages(t *testing.T) {
	t.Parallel()
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.URL.Query().Get("continuation-token"))
		switch len(tokens) {
		case 1, 2:
			_, _ = w.Write([]byte(s3ListResult(nil, true, fmt.Sprintf("t%d", len(tokens)))))
		default:
			_, _ = w.Write([]byte(s3ListResult([]string{"k"}, true, "t3")))
		}
	}))
	t.Cleanup(srv.Close)

	objects, err := newTestBucket(t, srv).List(context.Background(), ListOptions{})
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, []string{"", "t1", "t2"}, tokens)
}

func TestS3Bucket_ListBoundsEmptyTruncatedPages(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(s3ListResult(nil, true, "again")))
	}))
	t.Cleanup(srv.Close)

	_, err := newTestBucket(t, srv).List(context.Background(), ListOptions{})
	require.ErrorContains(t, err, "consecutive empty truncated pages")
	require.Equal(t, int32(s3MaxEmptyListPages), requests.Load())
}
