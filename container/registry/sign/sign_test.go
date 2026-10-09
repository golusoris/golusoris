// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/jonboulle/clockwork"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/container/registry/sign"
)

var errBoom = errors.New("boom")

func newRegistry(t *testing.T, mw func(http.Handler) http.Handler, opts ...regsrv.Option) string {
	t.Helper()
	h := regsrv.New(opts...)
	if mw != nil {
		h = mw(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse registry URL: %v", err)
	}
	return u.Host
}

// newTransport is private: httptest.Server.Close resets http.DefaultTransport (#701).
func newTransport(t *testing.T) *http.Transport {
	t.Helper()
	rt := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(rt.CloseIdleConnections)
	return rt
}

func newClient(t *testing.T) *registry.Client {
	t.Helper()
	return registry.New(registry.Options{}, authn.NewMultiKeychain(), newTransport(t))
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// seed pushes a random image (or index) to host/repo:v1 and returns its
// digest reference and descriptor digest.
func seed(t *testing.T, host string, index bool) (string, v1.Hash) {
	t.Helper()
	tag, err := name.NewTag(host + "/app:v1")
	if err != nil {
		t.Fatal(err)
	}
	var (
		h    v1.Hash
		werr error
	)
	opt := remote.WithTransport(newTransport(t))
	if index {
		idx, rerr := random.Index(64, 1, 2)
		if rerr != nil {
			t.Fatal(rerr)
		}
		h, _ = idx.Digest()
		werr = remote.WriteIndex(tag, idx, opt)
	} else {
		img, rerr := random.Image(64, 1)
		if rerr != nil {
			t.Fatal(rerr)
		}
		h, _ = img.Digest()
		werr = remote.Write(tag, img, opt)
	}
	if werr != nil {
		t.Fatalf("seed: %v", werr)
	}
	return host + "/app@" + h.String(), h
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func loadBundle(t *testing.T, raw []byte) *bundle.Bundle {
	t.Helper()
	var b bundle.Bundle
	if err := b.UnmarshalJSON(raw); err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	return &b
}

// verifyWithKey runs the sigstore-go checks `cosign verify --key k.pub
// --insecure-ignore-tlog <ref>` runs on a new-format bundle: the public key
// as trusted material, no observer timestamps, the image digest as artifact.
func verifyWithKey(t *testing.T, raw []byte, pub crypto.PublicKey, digest v1.Hash) (*verify.VerificationResult, error) {
	t.Helper()
	sv, err := signature.LoadDefaultVerifier(pub)
	if err != nil {
		t.Fatal(err)
	}
	key := root.NewExpiringKey(sv, time.Time{}, time.Time{})
	tm := root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) { return key, nil })
	v, err := verify.NewVerifier(tm, verify.WithNoObserverTimestamps())
	if err != nil {
		t.Fatal(err)
	}
	return v.Verify(loadBundle(t, raw), verify.NewPolicy(artifactDigest(t, digest), verify.WithKey()))
}

func artifactDigest(t *testing.T, digest v1.Hash) verify.ArtifactPolicyOption {
	t.Helper()
	want, err := hex.DecodeString(digest.Hex)
	if err != nil {
		t.Fatal(err)
	}
	return verify.WithArtifactDigest(digest.Algorithm, want)
}

// failingTransport fails the test on any request: it proves a call did no I/O.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
	return nil, errBoom
}

type failingSigner struct{ crypto.Signer }

func (failingSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) { return nil, errBoom }

func TestImage_KeySignatureVerifies(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		key   crypto.Signer
		index bool
		api   bool
	}{
		{name: "ecdsa-p256 tag-schema", key: ecKey(t)},
		{name: "ecdsa-p256 referrers-api", key: ecKey(t), api: true},
		{name: "ecdsa-p384 index", key: p384, index: true},
		{name: "rsa-2048", key: rsaKey},
		{name: "ed25519", key: edKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newRegistry(t, nil, regsrv.WithReferrersSupport(tc.api))
			ref, digest := seed(t, host, tc.index)
			at := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
			sig, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: tc.key},
				sign.Options{Clock: clockwork.NewFakeClockAt(at)})
			if err != nil {
				t.Fatalf("Image: %v", err)
			}
			if sig.Subject.Digest != digest || sig.Descriptor.ArtifactType != sign.BundleMediaType {
				t.Fatalf("signature = %+v", sig)
			}
			checkReferrer(t, host, ref, sig, at)
			res, err := verifyWithKey(t, sig.Bundle, tc.key.Public(), digest)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if res.Statement.GetPredicateType() != sign.PredicateType {
				t.Fatalf("predicate type = %q", res.Statement.GetPredicateType())
			}
			if _, err = verifyWithKey(t, sig.Bundle, ecKey(t).Public(), digest); err == nil {
				t.Fatal("bundle verified with a foreign key")
			}
		})
	}
}

// checkReferrer reads the signature back the way cosign's GetBundles does:
// all referrers of the digest (no artifactType filter; ggcr's test registry
// reports config.mediaType there), one bundle layer, subject = the signed
// manifest.
func checkReferrer(t *testing.T, host, ref string, sig sign.Signature, at time.Time) {
	t.Helper()
	c := newClient(t)
	ctx := testCtx(t)
	refs, err := c.Referrers(ctx, ref, "")
	if err != nil || len(refs) != 1 || refs[0].Digest != sig.Descriptor.Digest {
		t.Fatalf("referrers = %+v, %v", refs, err)
	}
	_, man, err := c.ArtifactManifest(ctx, host+"/app@"+sig.Descriptor.Digest.String())
	if err != nil {
		t.Fatalf("referrer manifest: %v", err)
	}
	if len(man.Layers) != 1 || man.Layers[0].MediaType != sign.BundleMediaType || man.Subject == nil ||
		man.Subject.Digest != sig.Subject.Digest || man.Subject.MediaType != sig.Subject.MediaType {
		t.Fatalf("referrer manifest = %+v", man)
	}
	want := map[string]string{
		sign.AnnotationCreated:       at.Format(time.RFC3339),
		sign.AnnotationBundleContent: "dsse-envelope",
		sign.AnnotationPredicateType: sign.PredicateType,
	}
	for k, v := range want {
		if man.Annotations[k] != v {
			t.Fatalf("annotation %s = %q, want %q", k, man.Annotations[k], v)
		}
	}
	blob, err := c.FetchBlob(ctx, host+"/app", man.Layers[0], 1<<20)
	if err != nil || string(blob) != string(sig.Bundle) {
		t.Fatalf("bundle layer = %q, %v", blob, err)
	}
}

// TestImage_StatementMatchesCosign checks the signed payload field by field
// against what `cosign sign` signs, including -a annotations.
func TestImage_StatementMatchesCosign(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, digest := seed(t, host, false)
	key := ecKey(t)
	sig, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: key},
		sign.Options{Annotations: map[string]string{"build": "rc4"}})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	env := loadBundle(t, sig.Bundle).GetDsseEnvelope()
	if env.GetPayloadType() != "application/vnd.in-toto+json" || len(env.GetSignatures()) != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	var st struct {
		Type    string `json:"_type"` //nolint:tagliatelle // in-toto wire name
		Subject []struct {
			Digest      map[string]string
			Annotations map[string]string
		}
		PredicateType string
		Predicate     map[string]any
	}
	if err = json.Unmarshal(env.GetPayload(), &st); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	if st.Type != "https://in-toto.io/Statement/v1" || st.PredicateType != sign.PredicateType ||
		len(st.Subject) != 1 || st.Subject[0].Digest["sha256"] != digest.Hex ||
		st.Subject[0].Annotations["build"] != "rc4" || st.Predicate == nil {
		t.Fatalf("statement = %+v", st)
	}
	if _, err = verifyWithKey(t, sig.Bundle, key.Public(), v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("bundle verified against another digest")
	}
}

// TestImage_DefaultOptions is the boundary of an empty Options: wall clock,
// default timeout, no annotations, no services.
func TestImage_DefaultOptions(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, digest := seed(t, host, false)
	key := ecKey(t)
	sig, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: key}, sign.Options{})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	b := loadBundle(t, sig.Bundle)
	if b.GetMediaType() != sign.BundleMediaType || b.GetVerificationMaterial().GetPublicKey() == nil ||
		len(b.GetVerificationMaterial().GetTlogEntries()) != 0 {
		t.Fatalf("bundle = %s", sig.Bundle)
	}
	if sig.Descriptor.Annotations[sign.AnnotationCreated] == "" {
		t.Fatalf("created annotation missing: %v", sig.Descriptor.Annotations)
	}
	if _, err = time.Parse(time.RFC3339, sig.Descriptor.Annotations[sign.AnnotationCreated]); err != nil {
		t.Fatalf("created annotation: %v", err)
	}
	if _, err = verifyWithKey(t, sig.Bundle, key.Public(), digest); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestImage_RejectsBeforeIO(t *testing.T) {
	t.Parallel()
	key := ecKey(t)
	token := func(context.Context) (string, error) { return "t", nil }
	const digestRef = "registry.example/app@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	cases := []struct {
		name string
		ref  string
		s    sign.Signer
		o    sign.Options
		want error
	}{
		{name: "tag only", ref: "registry.example/app:v1", s: sign.Signer{Key: key}, want: sign.ErrDigestRequired},
		{name: "repository only", ref: "registry.example/app", s: sign.Signer{Key: key}, want: sign.ErrDigestRequired},
		{name: "no key no token", ref: digestRef, want: sign.ErrInvalidOptions},
		{name: "token without fulcio", ref: digestRef, s: sign.Signer{IDToken: token}, want: sign.ErrInvalidOptions},
		{name: "fulcio without token", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{FulcioURL: "https://f.example"}, want: sign.ErrInvalidOptions},
		{name: "rekor version 3", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{RekorURL: "https://r.example", RekorVersion: 3}, want: sign.ErrInvalidOptions},
		{name: "rekor version without url", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{RekorVersion: 1}, want: sign.ErrInvalidOptions},
		{name: "keyless rekor v2 without tsa", ref: digestRef, s: sign.Signer{IDToken: token}, o: sign.Options{FulcioURL: "https://f.example", RekorURL: "https://r.example", RekorVersion: 2}, want: sign.ErrInvalidOptions},
		{name: "relative url", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{RekorURL: "/rekor"}, want: sign.ErrInvalidOptions},
		{name: "non-http url", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{TSAURL: "ftp://tsa.example"}, want: sign.ErrInvalidOptions},
		{name: "unparsable url", ref: digestRef, s: sign.Signer{Key: key}, o: sign.Options{TSAURL: "http://[::1"}, want: sign.ErrInvalidOptions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := registry.New(registry.Options{}, authn.NewMultiKeychain(), failingTransport{t})
			if _, err := sign.Image(testCtx(t), c, tc.ref, tc.s, tc.o); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := sign.Image(t.Context(), nil, digestRef, sign.Signer{Key: key}, sign.Options{}); !errors.Is(err, sign.ErrInvalidOptions) {
		t.Fatalf("nil client err = %v", err)
	}
	if _, err := sign.Image(t.Context(), newClient(t), "Not A Ref@@", sign.Signer{Key: key}, sign.Options{}); err == nil {
		t.Fatal("malformed reference accepted")
	}
}

func TestImage_SignerErrorPropagates(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	c := newClient(t)
	_, err := sign.Image(testCtx(t), c, ref, sign.Signer{Key: failingSigner{ecKey(t)}}, sign.Options{})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want signer error", err)
	}
	if refs, rerr := c.Referrers(testCtx(t), ref, ""); rerr != nil || len(refs) != 0 {
		t.Fatalf("referrers after failed sign = %v, %v", refs, rerr)
	}
}

// TestImage_UnsupportedKey is the boundary of a key sigstore has no
// algorithm for.
func TestImage_UnsupportedKey(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	p224, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: p224}, sign.Options{}); !errors.Is(err, sign.ErrInvalidOptions) {
		t.Fatalf("err = %v, want ErrInvalidOptions", err)
	}
}

func TestImage_PushFailureWrapped(t *testing.T) {
	t.Parallel()
	denyDigestPut := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/sha256:") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	host := newRegistry(t, denyDigestPut)
	ref, _ := seed(t, host, false)
	_, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: ecKey(t)}, sign.Options{})
	var terr *transport.Error
	if !errors.As(err, &terr) || terr.StatusCode != http.StatusForbidden || !strings.Contains(err.Error(), "sign: push signature") {
		t.Fatalf("err = %v, want wrapped 403", err)
	}
}

func TestImage_SubjectMismatchOrMissing(t *testing.T) {
	t.Parallel()
	var other atomic.Pointer[string]
	swap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if o := other.Load(); o != nil && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/manifests/sha256:") {
				r.URL.Path = r.URL.Path[:strings.LastIndex(r.URL.Path, "/")+1] + *o
			}
			next.ServeHTTP(w, r)
		})
	}
	host := newRegistry(t, swap)
	ref, _ := seed(t, host, false)
	missing := host + "/app@sha256:" + strings.Repeat("a", 64)
	key := ecKey(t)
	if _, err := sign.Image(testCtx(t), newClient(t), missing, sign.Signer{Key: key}, sign.Options{}); err == nil {
		t.Fatal("signed a digest the registry does not hold")
	}
	img, err := random.Image(32, 1)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := img.Digest()
	tag, _ := name.NewTag(host + "/app:other")
	if err = remote.Write(tag, img, remote.WithTransport(newTransport(t))); err != nil {
		t.Fatal(err)
	}
	swapTo := h.String()
	other.Store(&swapTo)
	if _, err = sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: key}, sign.Options{}); err == nil {
		t.Fatal("signed a manifest served under another digest")
	}
}
