// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/storage"
)

func testAccountKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func sharedKeyBucket(t *testing.T, serviceURL string, clk clockwork.Clock) *Bucket {
	t.Helper()
	b, err := New(Options{
		ServiceURL: serviceURL, Container: "media", AccountName: "acct", AccountKey: testAccountKey(t),
	}, clk)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestValidate(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewRealClock()
	base := Options{ServiceURL: "https://acct.blob.core.windows.net/", Container: "c"}
	with := func(mut func(*Options)) Options {
		o := base
		mut(&o)
		return o
	}
	for _, tc := range []struct {
		name    string
		opts    Options
		clk     clockwork.Clock
		wantErr bool
	}{
		{name: "entra id", opts: base, clk: clk},
		{name: "shared key", opts: with(func(o *Options) { o.AccountName, o.AccountKey = "a", "k" }), clk: clk},
		{name: "block min", opts: with(func(o *Options) { o.BlockSize = MinBlockSize }), clk: clk},
		{name: "block max", opts: with(func(o *Options) { o.BlockSize = MaxBlockSize }), clk: clk},
		{name: "concurrency max", opts: with(func(o *Options) { o.Concurrency = MaxConcurrency }), clk: clk},
		{name: "presign max", opts: with(func(o *Options) { o.PresignTTL = storage.MaxPresignTTL }), clk: clk},
		{name: "nil clock", opts: base, wantErr: true},
		{name: "missing container", opts: with(func(o *Options) { o.Container = "" }), clk: clk, wantErr: true},
		{name: "relative url", opts: with(func(o *Options) { o.ServiceURL = "acct/blob" }), clk: clk, wantErr: true},
		{name: "key without name", opts: with(func(o *Options) { o.AccountKey = "k" }), clk: clk, wantErr: true},
		{name: "block below min", opts: with(func(o *Options) { o.BlockSize = MinBlockSize - 1 }), clk: clk, wantErr: true},
		{name: "block above max", opts: with(func(o *Options) { o.BlockSize = MaxBlockSize + 1 }), clk: clk, wantErr: true},
		{name: "concurrency above max", opts: with(func(o *Options) { o.Concurrency = MaxConcurrency + 1 }), clk: clk, wantErr: true},
		{name: "negative concurrency", opts: with(func(o *Options) { o.Concurrency = -1 }), clk: clk, wantErr: true},
		{name: "presign above max", opts: with(func(o *Options) { o.PresignTTL = storage.MaxPresignTTL + 1 }), clk: clk, wantErr: true},
	} {
		if err := validate(tc.opts, tc.clk); (err != nil) != tc.wantErr {
			t.Errorf("%s: validate() = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestPutHeaders(t *testing.T) {
	t.Parallel()
	md5 := storage.Checksum{Algorithm: storage.ChecksumMD5, Value: make([]byte, 16)}
	header, err := putHeaders(storage.PresignPutOptions{
		ContentType: "video/mp4", Metadata: map[string]string{"JobID": "7"}, Checksum: md5,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"X-Ms-Blob-Type": "BlockBlob", "X-Ms-Blob-Content-Type": "video/mp4",
		"X-Ms-Meta-Jobid": "7", "Content-MD5": md5.Base64(),
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, tc := range []struct {
		opts storage.PresignPutOptions
		want error
	}{
		{opts: storage.PresignPutOptions{ContentLength: 5}, want: storage.ErrUnsupportedConstraint},
		{opts: storage.PresignPutOptions{Checksum: storage.Checksum{Algorithm: storage.ChecksumSHA256}}, want: storage.ErrUnsupportedChecksum},
		{opts: storage.PresignPutOptions{Checksum: storage.Checksum{Algorithm: storage.ChecksumCRC32C}}, want: storage.ErrUnsupportedChecksum},
		{opts: storage.PresignPutOptions{Checksum: storage.Checksum{Algorithm: "whirlpool"}}, want: storage.ErrUnsupportedChecksum},
	} {
		if _, err = putHeaders(tc.opts); !errors.Is(err, tc.want) {
			t.Errorf("putHeaders(%+v) = %v, want %v", tc.opts, err, tc.want)
		}
	}
	if _, err = putHeaders(storage.PresignPutOptions{Metadata: map[string]string{"job-id": "7"}}); err == nil {
		t.Error("non-identifier metadata name accepted")
	}
}

func TestMetadataNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"job": true, "Job_ID": true, "_x": true, "a1": true,
		"": false, "1a": false, "job-id": false, "job id": false, "jöb": false,
	} {
		if got := validMetadataName(name); got != want {
			t.Errorf("validMetadataName(%q) = %v, want %v", name, got, want)
		}
	}
	in, err := toMetadata(map[string]string{"Owner": "alice"})
	if err != nil || *in["owner"] != "alice" {
		t.Fatalf("toMetadata = %v, %v", in, err)
	}
	if out := fromMetadata(map[string]*string{"Owner": in["owner"], "nil": nil}); out["owner"] != "alice" || out["nil"] != "" {
		t.Fatalf("fromMetadata = %v", out)
	}
	if m, err := toMetadata(nil); m != nil || err != nil {
		t.Fatalf("toMetadata(nil) = %v, %v", m, err)
	}
}

func TestSASExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 7, 12, 0, 0, 900_000_000, time.UTC)
	if got := sasExpiry(now, storage.MinPresignTTL); !got.Equal(time.Date(2026, 10, 7, 12, 0, 2, 0, time.UTC)) {
		t.Fatalf("1s ttl rounds to %s", got)
	}
	exact := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if got := sasExpiry(exact, time.Minute); !got.Equal(exact.Add(time.Minute)) {
		t.Fatalf("whole-second ttl = %s", got)
	}
	if got := sasExpiry(now, storage.MaxPresignTTL); !got.Equal(now.Add(storage.MaxPresignTTL).Truncate(time.Second)) {
		t.Fatalf("max ttl = %s, want floor of now+7d", got)
	}
}

type staticCredential struct{}

func (staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "entra-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type delegationRecorder struct {
	mu    sync.Mutex
	query url.Values
	body  string
	authz string
}

func delegationBucket(t *testing.T, rec *delegationRecorder, status int) *Bucket {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.query, rec.body, rec.authz = r.URL.Query(), string(body), r.Header.Get("Authorization")
		rec.mu.Unlock()
		if status != http.StatusOK {
			w.Header().Set("X-Ms-Error-Code", "AuthorizationPermissionMismatch")
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><UserDelegationKey>` +
			`<SignedOid>oid-1</SignedOid><SignedTid>tid-1</SignedTid>` +
			`<SignedStart>2026-10-07T11:59:00Z</SignedStart><SignedExpiry>2026-10-07T13:00:00Z</SignedExpiry>` +
			`<SignedService>b</SignedService><SignedVersion>2026-12-06</SignedVersion>` +
			`<Value>` + base64.StdEncoding.EncodeToString([]byte("delegation-key")) + `</Value></UserDelegationKey>`))
	}))
	t.Cleanup(srv.Close)
	svc, err := service.NewClient(srv.URL+"/acct/", staticCredential{}, &service.ClientOptions{
		Transport: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Bucket{
		service: svc, container: svc.NewContainerClient("media"), name: "media",
		clock: clockwork.NewRealClock(), protocol: sas.ProtocolHTTPS, presignTTL: time.Hour,
	}
}

func TestPresignPut_UserDelegationSAS(t *testing.T) {
	t.Parallel()
	rec := &delegationRecorder{}
	b := delegationBucket(t, rec, http.StatusOK)
	req, err := b.PresignPut(context.Background(), "in/clip.mp4", 90*time.Second, storage.PresignPutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/acct/media/in/clip.mp4" || q.Get("sp") != "cw" || q.Get("skoid") != "oid-1" || q.Get("sig") == "" {
		t.Fatalf("SAS URL = %s", req.URL)
	}
	if se := q.Get("se"); se != req.Expires.Format(sas.TimeFormat) {
		t.Fatalf("se = %s, Expires = %s", se, req.Expires)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.query.Get("comp") != "userdelegationkey" || rec.authz != "Bearer entra-token" {
		t.Fatalf("delegation request query=%v authz=%q", rec.query, rec.authz)
	}
	if !strings.Contains(rec.body, "<Expiry>"+req.Expires.Format(sas.TimeFormat)+"</Expiry>") {
		t.Fatalf("delegation key expiry not bound to SAS expiry: %s", rec.body)
	}
}

func TestURL_UserDelegationFailure(t *testing.T) {
	t.Parallel()
	b := delegationBucket(t, &delegationRecorder{}, http.StatusForbidden)
	if _, err := b.URL(context.Background(), "k"); err == nil || !strings.Contains(err.Error(), "user delegation key") {
		t.Fatalf("URL = %v, want delegation key error", err)
	}
}

func TestTokenCredential(t *testing.T) {
	t.Parallel()
	token := filepath.Join(t.TempDir(), "token")
	if _, err := tokenCredential(Options{
		FederatedTokenFile: token, TenantID: "00000000-0000-0000-0000-000000000001",
		ClientID: "00000000-0000-0000-0000-000000000002",
	}); err != nil {
		t.Fatalf("workload identity chain: %v", err)
	}
	if _, err := tokenCredential(Options{ClientID: "00000000-0000-0000-0000-000000000002"}); err != nil {
		t.Fatalf("managed identity chain: %v", err)
	}
}

func TestAwaitCopy(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ms-Copy-Status", "success")
	}))
	t.Cleanup(srv.Close)
	clk := clockwork.NewFakeClock()
	b := sharedKeyBucket(t, srv.URL+"/acct/", clk)
	target := b.container.NewBlobClient("dst")
	if err := b.awaitCopy(context.Background(), target, blob.CopyStatusTypeSuccess); err != nil {
		t.Fatalf("success = %v", err)
	}
	if err := b.awaitCopy(context.Background(), target, blob.CopyStatusTypeFailed); err == nil {
		t.Fatal("failed copy accepted")
	}

	done := make(chan error, 1)
	go func() { done <- b.awaitCopy(context.Background(), target, blob.CopyStatusTypePending) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(copyPollInterval)
	if err := <-done; err != nil {
		t.Fatalf("pending then success = %v", err)
	}

	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := b.awaitCopy(cancelled, target, blob.CopyStatusTypePending); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled pending = %v", err)
	}
}

func TestBucket_RejectsBeforeBackendIO(t *testing.T) {
	t.Parallel()
	b := sharedKeyBucket(t, "http://127.0.0.1:1/acct/", clockwork.NewRealClock())
	ctx := context.Background()
	if _, err := b.Put(ctx, "../x", strings.NewReader(""), storage.PutOptions{}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("Put unsafe = %v", err)
	}
	if _, err := b.Put(ctx, "k", nil, storage.PutOptions{}); err == nil {
		t.Fatal("Put nil reader accepted")
	}
	if _, err := b.Put(ctx, "k", strings.NewReader(""), storage.PutOptions{Metadata: map[string]string{"a-b": "v"}}); err == nil {
		t.Fatal("Put invalid metadata name accepted")
	}
	if _, err := b.Copy(ctx, "a", "a"); !errors.Is(err, storage.ErrCopySameKey) {
		t.Fatalf("Copy same = %v", err)
	}
	if _, err := b.List(ctx, storage.ListOptions{Limit: -1}); err == nil {
		t.Fatal("List negative limit accepted")
	}
	if _, err := b.List(ctx, storage.ListOptions{Prefix: "../"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("List unsafe prefix = %v", err)
	}
	if _, err := b.PresignPut(ctx, "k", 0, storage.PresignPutOptions{}); !errors.Is(err, storage.ErrPresignTTL) {
		t.Fatalf("PresignPut ttl 0 = %v", err)
	}
}
