// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	gstorage "cloud.google.com/go/storage"
	"github.com/jonboulle/clockwork"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"

	"github.com/golusoris/golusoris/storage"
)

func offlineBucket(t *testing.T, signer *urlSigner) *Bucket {
	t.Helper()
	client, err := gstorage.NewClient(context.Background(),
		option.WithEndpoint("http://127.0.0.1:1/storage/v1/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return newBucket(client, signer, Options{Bucket: "media", Endpoint: "http://127.0.0.1:1/storage/v1/"},
		clockwork.NewRealClock())
}

func TestValidate(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewRealClock()
	ok := Options{Bucket: "b"}
	for _, tc := range []struct {
		name    string
		opts    Options
		clk     clockwork.Clock
		wantErr bool
	}{
		{name: "minimal", opts: ok, clk: clk},
		{name: "chunk min", opts: Options{Bucket: "b", ChunkSize: MinChunkSize}, clk: clk},
		{name: "chunk max", opts: Options{Bucket: "b", ChunkSize: MaxChunkSize}, clk: clk},
		{name: "presign max", opts: Options{Bucket: "b", PresignTTL: storage.MaxPresignTTL}, clk: clk},
		{name: "emulator", opts: Options{Bucket: "b", Endpoint: "http://localhost:4443/storage/v1/"}, clk: clk},
		{name: "missing bucket", opts: Options{}, clk: clk, wantErr: true},
		{name: "nil clock", opts: ok, wantErr: true},
		{name: "chunk below min", opts: Options{Bucket: "b", ChunkSize: MinChunkSize - 1}, clk: clk, wantErr: true},
		{name: "chunk above max", opts: Options{Bucket: "b", ChunkSize: MaxChunkSize + 1}, clk: clk, wantErr: true},
		{name: "presign above max", opts: Options{Bucket: "b", PresignTTL: storage.MaxPresignTTL + 1}, clk: clk, wantErr: true},
		{name: "negative presign", opts: Options{Bucket: "b", PresignTTL: -time.Second}, clk: clk, wantErr: true},
		{name: "relative endpoint", opts: Options{Bucket: "b", Endpoint: "storage/v1"}, clk: clk, wantErr: true},
	} {
		if err := validate(tc.opts, tc.clk); (err != nil) != tc.wantErr {
			t.Errorf("%s: validate() = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestPutConstraints(t *testing.T) {
	t.Parallel()
	md5 := storage.Checksum{Algorithm: storage.ChecksumMD5, Value: make([]byte, 16)}
	signOpts, header, err := putConstraints(storage.PresignPutOptions{
		ContentType: "video/mp4", ContentLength: 42, Metadata: map[string]string{"Job": "7"}, Checksum: md5,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := http.Header{
		"Content-Type":                {"video/mp4"},
		"X-Goog-Content-Length-Range": {"42,42"},
		"X-Goog-Meta-Job":             {"7"},
		"Content-Md5":                 {md5.Base64()},
	}
	if !equalHeader(header, wantHeader) {
		t.Fatalf("header = %v, want %v", header, wantHeader)
	}
	if signOpts.ContentType != "video/mp4" || signOpts.MD5 != md5.Base64() || len(signOpts.Headers) != 2 {
		t.Fatalf("sign options = %+v", signOpts)
	}

	crc := storage.Checksum{Algorithm: storage.ChecksumCRC32C, Value: []byte{1, 2, 3, 4}}
	_, header, err = putConstraints(storage.PresignPutOptions{Checksum: crc})
	if err != nil || header.Get("X-Goog-Hash") != "crc32c="+crc.Base64() {
		t.Fatalf("crc32c header = %v, %v", header, err)
	}
	_, _, err = putConstraints(storage.PresignPutOptions{
		Checksum: storage.Checksum{Algorithm: storage.ChecksumSHA256, Value: make([]byte, 32)},
	})
	if !errors.Is(err, storage.ErrUnsupportedChecksum) {
		t.Fatalf("sha256 = %v, want ErrUnsupportedChecksum", err)
	}
}

func equalHeader(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range b {
		if a.Get(k) != b.Get(k) {
			return false
		}
	}
	return true
}

type signBlobRecorder struct {
	mu      sync.Mutex
	path    string
	payload []byte
}

func newIAMServer(t *testing.T, rec *signBlobRecorder, signature []byte) *iamcredentials.Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req iamcredentials.SignBlobRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload, _ := base64.StdEncoding.DecodeString(req.Payload)
		rec.mu.Lock()
		rec.path, rec.payload = r.URL.Path, payload
		rec.mu.Unlock()
		_ = json.NewEncoder(w).Encode(iamcredentials.SignBlobResponse{
			SignedBlob: base64.StdEncoding.EncodeToString(signature),
		})
	}))
	t.Cleanup(srv.Close)
	svc, err := iamcredentials.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPresignPut_SignsThroughIAMSignBlob(t *testing.T) {
	t.Parallel()
	rec := &signBlobRecorder{}
	sig := []byte("remote-signature")
	svc := newIAMServer(t, rec, sig)
	b := offlineBucket(t, &urlSigner{blob: iamSigner{svc: svc}, email: "uploader@proj.iam.gserviceaccount.com"})

	req, err := b.PresignPut(context.Background(), "in/clip.mp4", 90*time.Second, storage.PresignPutOptions{
		ContentType: "video/mp4", ContentLength: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if got := q.Get("X-Goog-Signature"); got != hex.EncodeToString(sig) {
		t.Fatalf("X-Goog-Signature = %q", got)
	}
	if !strings.HasPrefix(q.Get("X-Goog-Credential"), "uploader@proj.iam.gserviceaccount.com/") {
		t.Fatalf("X-Goog-Credential = %q", q.Get("X-Goog-Credential"))
	}
	if q.Get("X-Goog-Expires") != "90" || u.Path != "/media/in/clip.mp4" || u.Scheme != "http" {
		t.Fatalf("signed URL = %s", req.URL)
	}
	if got := q.Get("X-Goog-SignedHeaders"); got != "content-type;host;x-goog-content-length-range" {
		t.Fatalf("X-Goog-SignedHeaders = %q", got)
	}
	signedAt, _ := time.Parse(goog4DateLayout, q.Get("X-Goog-Date"))
	if !req.Expires.Equal(signedAt.Add(90 * time.Second)) {
		t.Fatalf("Expires = %s, signed at %s", req.Expires, signedAt)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.HasSuffix(rec.path, "/projects/-/serviceAccounts/uploader@proj.iam.gserviceaccount.com:signBlob") {
		t.Fatalf("signBlob path = %q", rec.path)
	}
	if !strings.HasPrefix(string(rec.payload), "GOOG4-RSA-SHA256\n") {
		t.Fatalf("signBlob payload = %q", rec.payload)
	}
}

func TestPresignPut_TTLBoundariesMapToWholeSeconds(t *testing.T) {
	t.Parallel()
	signer, err := newEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	b := offlineBucket(t, signer)
	for ttl, want := range map[time.Duration]string{
		storage.MinPresignTTL: "1",
		storage.MaxPresignTTL: "604800",
	} {
		req, presignErr := b.PresignPut(context.Background(), "k", ttl, storage.PresignPutOptions{})
		if presignErr != nil {
			t.Fatalf("ttl %s: %v", ttl, presignErr)
		}
		u, _ := url.Parse(req.URL)
		if got := u.Query().Get("X-Goog-Expires"); got != want {
			t.Fatalf("ttl %s: X-Goog-Expires = %s, want %s", ttl, got, want)
		}
	}
	if _, err = b.PresignPut(context.Background(), "k", storage.MaxPresignTTL+time.Second,
		storage.PresignPutOptions{}); !errors.Is(err, storage.ErrPresignTTL) {
		t.Fatalf("ttl above max = %v", err)
	}
}

func TestPresignPut_SignBlobFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"code":403,"message":"denied"}}`, http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	svc, err := iamcredentials.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	b := offlineBucket(t, &urlSigner{blob: iamSigner{svc: svc}, email: "sa@p.iam.gserviceaccount.com"})
	if _, err = b.URL(context.Background(), "k"); err == nil || !strings.Contains(err.Error(), "sign blob") {
		t.Fatalf("URL with denied signBlob = %v", err)
	}
}

func TestURLSigner_AccessIDLookup(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fail := true
	s := &urlSigner{lookup: func(context.Context) (string, error) {
		calls.Add(1)
		if fail {
			return "", errors.New("metadata down")
		}
		return "node@proj.iam.gserviceaccount.com", nil
	}}
	if _, err := s.accessID(context.Background()); err == nil {
		t.Fatal("lookup error swallowed")
	}
	fail = false
	for range 2 {
		email, err := s.accessID(context.Background())
		if err != nil || email != "node@proj.iam.gserviceaccount.com" {
			t.Fatalf("accessID = %q, %v", email, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("lookup calls = %d, want 2 (failure not cached, success cached)", calls.Load())
	}
	empty := &urlSigner{lookup: func(context.Context) (string, error) { return "", nil }}
	if _, err := empty.accessID(context.Background()); err == nil {
		t.Fatal("empty metadata email accepted")
	}
	if _, err := (&urlSigner{}).accessID(context.Background()); err == nil {
		t.Fatal("signer without email or lookup accepted")
	}
}

func credsWithJSON(t *testing.T, raw []byte) *auth.Credentials {
	t.Helper()
	return auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: staticToken{}, JSON: raw,
	})
}

type staticToken struct{}

func (staticToken) Token(context.Context) (*auth.Token, error) {
	return &auth.Token{Value: "t", Type: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}

func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestNewCredentialSigner(t *testing.T) {
	t.Parallel()
	saJSON, _ := json.Marshal(credentialFile{
		Type: "service_account", ClientEmail: "key@p.iam.gserviceaccount.com", PrivateKey: testKeyPEM(t),
	})
	fedJSON, _ := json.Marshal(credentialFile{
		Type:             "external_account",
		ImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/fed@p.iam.gserviceaccount.com:generateAccessToken",
	})
	for _, tc := range []struct {
		name      string
		raw       []byte
		email     string
		wantEmail string
		wantKey   bool
		wantIAM   bool
	}{
		{name: "service account key signs locally", raw: saJSON, wantEmail: "key@p.iam.gserviceaccount.com", wantKey: true},
		{
			name: "other signer email uses IAM", raw: saJSON, email: "other@p.iam.gserviceaccount.com",
			wantEmail: "other@p.iam.gserviceaccount.com", wantIAM: true,
		},
		{name: "federation impersonates", raw: fedJSON, wantEmail: "fed@p.iam.gserviceaccount.com", wantIAM: true},
		{name: "metadata credentials look up email", raw: nil, wantIAM: true},
	} {
		s, err := newCredentialSigner(context.Background(), credsWithJSON(t, tc.raw), tc.email)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if s.email != tc.wantEmail || (len(s.key) > 0) != tc.wantKey || (s.blob != nil) != tc.wantIAM {
			t.Fatalf("%s: signer email=%q key=%v iam=%v", tc.name, s.email, len(s.key) > 0, s.blob != nil)
		}
		if tc.wantEmail == "" && s.lookup == nil {
			t.Fatalf("%s: no metadata lookup", tc.name)
		}
	}
	if _, err := newCredentialSigner(context.Background(), credsWithJSON(t, []byte("{")), ""); err == nil {
		t.Fatal("malformed credentials JSON accepted")
	}
}

func TestImpersonatedEmail(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/a@p.iam.gserviceaccount.com:generateAccessToken": "a@p.iam.gserviceaccount.com",
		"":                                "",
		"https://example.test/no-account": "",
	} {
		if got := impersonatedEmail(in); got != want {
			t.Errorf("impersonatedEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignedExpiryRejectsMalformedURLs(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"://bad",
		"https://storage.googleapis.com/b/k?X-Goog-Expires=60",
		"https://storage.googleapis.com/b/k?X-Goog-Date=20260101T000000Z&X-Goog-Expires=soon",
	} {
		if _, err := signedExpiry(raw); err == nil {
			t.Errorf("signedExpiry(%q) accepted", raw)
		}
	}
}

func TestMapErrorNotFound(t *testing.T) {
	t.Parallel()
	if err := mapError("stat", "k", gstorage.ErrObjectNotExist); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("mapError = %v", err)
	}
	other := errors.New("boom")
	if err := mapError("stat", "k", other); !errors.Is(err, other) || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("mapError = %v", err)
	}
}

func TestBucket_RejectsBeforeBackendIO(t *testing.T) {
	t.Parallel()
	b := offlineBucket(t, &urlSigner{})
	ctx := context.Background()
	if _, err := b.Put(ctx, "../x", strings.NewReader(""), storage.PutOptions{}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("Put unsafe = %v", err)
	}
	if _, err := b.Put(ctx, "k", nil, storage.PutOptions{}); err == nil {
		t.Fatal("Put nil reader accepted")
	}
	if _, err := b.Copy(ctx, "a", "a"); !errors.Is(err, storage.ErrCopySameKey) {
		t.Fatalf("Copy same = %v", err)
	}
	if _, err := b.List(ctx, storage.ListOptions{Limit: storage.MaxListLimit + 1}); err == nil {
		t.Fatal("List over limit accepted")
	}
	if _, err := b.List(ctx, storage.ListOptions{Prefix: "../"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("List unsafe prefix = %v", err)
	}
	if _, err := b.URL(ctx, "/abs"); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("URL unsafe = %v", err)
	}
	if _, err := b.URL(ctx, "k"); err == nil {
		t.Fatal("URL without signer identity accepted")
	}
}
