package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

// s3TestClient builds a real s3.Client pointed at srv with path-style
// addressing and static creds, so requests flow through the genuine SDK
// marshalers against our XML stub — no live bucket or MinIO required.
func s3TestClient(t *testing.T, srv *httptest.Server) *s3.Client {
	t.Helper()
	return s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
		HTTPClient:   srv.Client(),
	})
}

func newTestBucket(t *testing.T, srv *httptest.Server) *S3Bucket {
	t.Helper()
	client := s3TestClient(t, srv)
	return newS3BucketForTest("my-bucket", client, presignAdapter{s3.NewPresignClient(client)}, time.Minute)
}

func TestS3Bucket_Put(t *testing.T) {
	t.Parallel()
	var gotBody string
	var gotPath, gotMethod, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	obj, err := b.Put(context.Background(), "dir/file.txt", strings.NewReader("hello world"), PutOptions{
		ContentType: "text/plain",
	})
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/my-bucket/dir/file.txt", gotPath)
	require.Equal(t, "text/plain", gotCT)
	require.Equal(t, "hello world", gotBody)
	require.Equal(t, "dir/file.txt", obj.Key)
	require.Equal(t, `"abc123"`, obj.ETag)
	require.Equal(t, "text/plain", obj.ContentType)
}

func TestS3Bucket_Put_DefaultContentType(t *testing.T) {
	t.Parallel()
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	obj, err := b.Put(context.Background(), "k", strings.NewReader("x"), PutOptions{})
	require.NoError(t, err)
	require.Equal(t, "application/octet-stream", gotCT)
	require.Equal(t, "application/octet-stream", obj.ContentType)
}

func TestS3Bucket_Get(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/my-bucket/dir/file.txt", r.URL.Path)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"e1"`)
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.Header().Set("Content-Length", "11")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello world"))
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	rc, obj, err := b.Get(context.Background(), "dir/file.txt")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Close() })

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(data))
	require.Equal(t, "dir/file.txt", obj.Key)
	require.Equal(t, int64(11), obj.Size)
	require.Equal(t, "text/plain", obj.ContentType)
	require.Equal(t, `"e1"`, obj.ETag)
	require.False(t, obj.LastModified.IsZero())
}

func TestS3Bucket_Get_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message>` +
			`<Key>missing</Key></Error>`))
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	_, _, err := b.Get(context.Background(), "missing")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3Bucket_Exists(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodHead, r.Method)
		require.Equal(t, "/my-bucket/present.txt", r.URL.Path)
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	ok, err := b.Exists(context.Background(), "present.txt")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestS3Bucket_Exists_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	ok, err := b.Exists(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestS3Bucket_Delete(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	require.NoError(t, b.Delete(context.Background(), "gone.txt"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/my-bucket/gone.txt", gotPath)
}

func TestS3Bucket_List(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "uploads/", r.URL.Query().Get("prefix"))
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Name>my-bucket</Name><Prefix>uploads/</Prefix><KeyCount>2</KeyCount>` +
			`<MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>` +
			`<Contents><Key>uploads/a.txt</Key><Size>5</Size><ETag>"a"</ETag>` +
			`<LastModified>2015-10-21T07:28:00.000Z</LastModified></Contents>` +
			`<Contents><Key>uploads/b.txt</Key><Size>7</Size><ETag>"b"</ETag>` +
			`<LastModified>2015-10-21T07:28:00.000Z</LastModified></Contents>` +
			`</ListBucketResult>`))
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	objs, err := b.List(context.Background(), ListOptions{Prefix: "uploads/"})
	require.NoError(t, err)
	require.Len(t, objs, 2)
	require.Equal(t, "uploads/a.txt", objs[0].Key)
	require.Equal(t, int64(5), objs[0].Size)
	require.Equal(t, "uploads/b.txt", objs[1].Key)
	require.Equal(t, int64(7), objs[1].Size)
}

func TestS3Bucket_List_RespectsLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Name>my-bucket</Name><IsTruncated>false</IsTruncated>` +
			`<Contents><Key>a</Key><Size>1</Size></Contents>` +
			`<Contents><Key>b</Key><Size>1</Size></Contents>` +
			`<Contents><Key>c</Key><Size>1</Size></Contents>` +
			`</ListBucketResult>`))
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	objs, err := b.List(context.Background(), ListOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, objs, 2)
}

func TestS3Bucket_URL_PresignsGet(t *testing.T) {
	t.Parallel()
	// Presigning is local (no HTTP round-trip); the server is just an endpoint
	// for the signer to embed in the URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := newTestBucket(t, srv)
	got, err := b.URL(context.Background(), "dir/file.txt")
	require.NoError(t, err)
	require.Contains(t, got, "/my-bucket/dir/file.txt")
	require.Contains(t, got, "X-Amz-Signature=")
	require.Contains(t, got, "X-Amz-Expires=60")
}

func TestNewS3Bucket_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts S3Options
	}{
		{name: "missing bucket", opts: S3Options{Region: "us-east-1"}},
		{name: "missing region", opts: S3Options{Bucket: "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewS3Bucket(context.Background(), tc.opts)
			require.Error(t, err)
		})
	}
}

func TestPresignTTL_Default(t *testing.T) {
	t.Parallel()
	require.Equal(t, defaultPresignTTL, presignTTL(0))
	require.Equal(t, defaultPresignTTL, presignTTL(-5*time.Second))
	require.Equal(t, 30*time.Second, presignTTL(30*time.Second))
}
