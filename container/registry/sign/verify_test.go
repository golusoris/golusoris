// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	sgsign "github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/container/registry/sign"
)

func keyPolicy(pub crypto.PublicKey) sign.Policy {
	return sign.Policy{Key: pub, InsecureIgnoreTlog: true}
}

func verifyOne(t *testing.T, c *registry.Client, ref string, p sign.Policy) sign.VerifiedSignature {
	t.Helper()
	got, err := sign.Verify(testCtx(t), c, ref, p)
	if err != nil || len(got) != 1 {
		t.Fatalf("Verify = %d signatures, %v; want 1", len(got), err)
	}
	return got[0]
}

// wantRejected asserts Verify fails closed and names reason.
func wantRejected(t *testing.T, c *registry.Client, ref string, p sign.Policy, reason string) {
	t.Helper()
	got, err := sign.Verify(testCtx(t), c, ref, p)
	if !errors.Is(err, sign.ErrNoValidSignature) || !strings.Contains(err.Error(), reason) || got != nil {
		t.Fatalf("Verify = %v, %v; want ErrNoValidSignature naming %q", got, err, reason)
	}
}

func subjectOf(t *testing.T, c *registry.Client, ref string) (v1.Descriptor, []byte) {
	t.Helper()
	m, err := c.Manifest(testCtx(t), ref)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}, m.Raw
}

// pushReferrer stores raw as the single layer of a referrer of subject.
func pushReferrer(t *testing.T, c *registry.Client, ref string, subject v1.Descriptor, artifactType string, layer types.MediaType, raw []byte) {
	t.Helper()
	repo, _, _ := strings.Cut(ref, "@")
	_, err := c.PushArtifact(testCtx(t), repo, registry.Artifact{
		ArtifactType: artifactType,
		Blobs:        []registry.Blob{{MediaType: layer, Reader: bytes.NewReader(raw)}},
		Subject:      &subject,
	})
	if err != nil {
		t.Fatalf("push referrer: %v", err)
	}
}

// signContent signs content into a protobuf-JSON bundle without pushing it.
func signContent(t *testing.T, kp sgsign.Keypair, content sgsign.Content) []byte {
	t.Helper()
	b, err := sgsign.Bundle(content, kp, sgsign.BundleOptions{Context: testCtx(t)})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := protojson.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func statementFor(t *testing.T, digest v1.Hash, predicateType string) sgsign.Content {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"_type":         "https://in-toto.io/Statement/v1",
		"subject":       []any{map[string]any{"digest": map[string]string{digest.Algorithm: digest.Hex}}},
		"predicateType": predicateType,
		"predicate":     map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &sgsign.DSSEData{Data: raw, PayloadType: "application/vnd.in-toto+json"}
}

// rewrite changes a signed bundle after the fact.
func rewrite(t *testing.T, raw []byte, edit func(*protobundle.Bundle)) []byte {
	t.Helper()
	b := &protobundle.Bundle{}
	if err := protojson.Unmarshal(raw, b); err != nil {
		t.Fatal(err)
	}
	edit(b)
	out, err := protojson.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerify_KeySignature(t *testing.T) {
	t.Parallel()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		key        crypto.Signer
		index, api bool
	}{
		{name: "ecdsa tag-schema", key: ecKey(t)},
		{name: "ecdsa referrers-api", key: ecKey(t), api: true},
		{name: "ecdsa index", key: ecKey(t), index: true},
		{name: "rsa", key: rsaKey},
		{name: "ed25519", key: edKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newRegistry(t, nil, regsrv.WithReferrersSupport(tc.api))
			ref, _ := seed(t, host, tc.index)
			c := newClient(t)
			sig, err := sign.Image(testCtx(t), c, ref, sign.Signer{Key: tc.key}, sign.Options{})
			if err != nil {
				t.Fatalf("Image: %v", err)
			}
			got := verifyOne(t, c, ref, keyPolicy(tc.key.Public()))
			if got.Descriptor.Digest != sig.Descriptor.Digest || got.Descriptor.ArtifactType != sign.BundleMediaType ||
				got.Descriptor.Annotations[sign.AnnotationBundleContent] != "dsse-envelope" || got.Subject.Digest != sig.Subject.Digest ||
				got.Subject.MediaType != sig.Subject.MediaType ||
				!bytes.Equal(got.Bundle, sig.Bundle) || got.Result.Statement.GetPredicateType() != sign.PredicateType ||
				got.Result.Signature.PublicKeyID == nil {
				t.Fatalf("verified = %+v, signed = %+v", got, sig)
			}
			wantRejected(t, c, ref, keyPolicy(ecKey(t).Public()), "verify bundle")
		})
	}
}

// TestVerify_ReferrersAPIQuirk pins why Verify lists referrers unfiltered:
// ggcr's registry reports config.mediaType as artifactType, so a listing
// filtered by BundleMediaType misses the bundle Verify still finds.
func TestVerify_ReferrersAPIQuirk(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil, regsrv.WithReferrersSupport(true))
	ref, _ := seed(t, host, false)
	c := newClient(t)
	key := ecKey(t)
	if _, err := sign.Image(testCtx(t), c, ref, sign.Signer{Key: key}, sign.Options{}); err != nil {
		t.Fatalf("Image: %v", err)
	}
	filtered, err := c.Referrers(testCtx(t), ref, sign.BundleMediaType)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("filtered referrers = %v, %v; the quirk this test pins is gone", filtered, err)
	}
	verifyOne(t, c, ref, keyPolicy(key.Public()))
}

// TestVerify_KeyIsTheOnlyTrustedKey keeps a key that TrustedMaterial also
// carries from standing in for Policy.Key.
func TestVerify_KeyIsTheOnlyTrustedKey(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	c := newClient(t)
	other := ecKey(t)
	if _, err := sign.Image(testCtx(t), c, ref, sign.Signer{Key: other}, sign.Options{}); err != nil {
		t.Fatalf("Image: %v", err)
	}
	sv, err := signature.LoadDefaultVerifier(other.Public())
	if err != nil {
		t.Fatal(err)
	}
	otherKey := root.NewExpiringKey(sv, time.Time{}, time.Time{})
	tm := root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) { return otherKey, nil })
	wantRejected(t, c, ref, sign.Policy{Key: ecKey(t).Public(), TrustedMaterial: tm, InsecureIgnoreTlog: true}, "verify bundle")
	verifyOne(t, c, ref, sign.Policy{Key: other.Public(), TrustedMaterial: tm, InsecureIgnoreTlog: true})
}

// keylessImage signs a fresh image with an ephemeral key a test Fulcio
// certifies for testEmail / testIssuer.
func keylessImage(t *testing.T, opts sign.Options) (*registry.Client, string, testCA) {
	t.Helper()
	ca := newCA(t, "test fulcio")
	token := idToken(t)
	opts.FulcioURL, opts.Transport = fulcioServer(t, ca, token).URL, newTransport(t)
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	c := newClient(t)
	s := sign.Signer{IDToken: func(context.Context) (string, error) { return token, nil }}
	if _, err := sign.Image(testCtx(t), c, ref, s, opts); err != nil {
		t.Fatalf("Image: %v", err)
	}
	return c, ref, ca
}

func TestVerify_KeylessIdentity(t *testing.T) {
	t.Parallel()
	c, ref, ca := keylessImage(t, sign.Options{})
	exact := sign.Identity{Issuer: testIssuer, Subject: testEmail}
	cases := []struct {
		name string
		ids  []sign.Identity
		ok   bool
	}{
		{name: "exact", ids: []sign.Identity{exact}, ok: true},
		{name: "subject regexp", ids: []sign.Identity{{Issuer: testIssuer, SubjectRegexp: `[a-z]+@example\.com`}}, ok: true},
		{name: "issuer regexp", ids: []sign.Identity{{IssuerRegexp: `https://issuer\.example\.(com|org)`, Subject: testEmail}}, ok: true},
		{name: "second of two", ids: []sign.Identity{{Issuer: testIssuer, Subject: "mallory@example.com"}, exact}, ok: true},
		{name: "other subject", ids: []sign.Identity{{Issuer: testIssuer, Subject: "mallory@example.com"}}},
		{name: "other issuer", ids: []sign.Identity{{Issuer: "https://evil.example.com", Subject: testEmail}}},
		{name: "subject regexp is anchored", ids: []sign.Identity{{Issuer: testIssuer, SubjectRegexp: `example\.com`}}},
		{name: "issuer regexp is anchored", ids: []sign.Identity{{IssuerRegexp: `issuer`, Subject: testEmail}}},
		{name: "subject regexp prefix is anchored", ids: []sign.Identity{{Issuer: testIssuer, SubjectRegexp: `ci@example`}}},
		{name: "alternation is anchored as a whole", ids: []sign.Identity{{Issuer: testIssuer, SubjectRegexp: `mallory|example\.com`}}},
		{name: "exact and regexp both apply", ids: []sign.Identity{{Issuer: testIssuer, Subject: testEmail, SubjectRegexp: `mallory@.*`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := sign.Policy{Identities: tc.ids, TrustedMaterial: trustRoot(t, &ca, nil, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true}
			if !tc.ok {
				wantRejected(t, c, ref, p, "certificate identity")
				return
			}
			got := verifyOne(t, c, ref, p)
			if got.Result.Signature.Certificate == nil || got.Result.Signature.Certificate.SubjectAlternativeName != testEmail ||
				got.Result.VerifiedTimestamps[0].Type != "CurrentTime" {
				t.Fatalf("result = %+v", got.Result)
			}
		})
	}
}

// TestVerify_KeylessRequirements checks what a keyless bundle without SCT,
// log entry or timestamp fails, and that keys and certificates do not mix.
func TestVerify_KeylessRequirements(t *testing.T) {
	t.Parallel()
	c, ref, ca := keylessImage(t, sign.Options{})
	ids := []sign.Identity{{Issuer: testIssuer, Subject: testEmail}}
	tsa := newTSA(t)
	other := newCA(t, "other fulcio")
	cases := []struct {
		name, reason string
		p            sign.Policy
	}{
		{
			name: "sct required by default", reason: "signed certificate timestamp",
			p: sign.Policy{Identities: ids, TrustedMaterial: trustRoot(t, &ca, nil, nil), InsecureIgnoreTlog: true},
		},
		{
			name: "tlog required by default", reason: "log entries",
			p: sign.Policy{Identities: ids, TrustedMaterial: trustRoot(t, &ca, nil, newLog(t)), InsecureIgnoreSCT: true},
		},
		{
			name: "signed timestamp required", reason: "timestamp",
			p: sign.Policy{Identities: ids, TrustedMaterial: trustRoot(t, &ca, &tsa, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true, SignedTimestamps: true},
		},
		{
			name: "certificate under a key policy", reason: "expected key signature",
			p: keyPolicy(ecKey(t).Public()),
		},
		{
			name: "foreign ca", reason: "leaf certificate",
			p: sign.Policy{Identities: ids, TrustedMaterial: trustRoot(t, &other, nil, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantRejected(t, c, ref, tc.p, tc.reason)
		})
	}
	t.Run("key bundle under an identity policy", func(t *testing.T) {
		t.Parallel()
		host := newRegistry(t, nil)
		keyRef, _ := seed(t, host, false)
		keyC := newClient(t)
		if _, err := sign.Image(testCtx(t), keyC, keyRef, sign.Signer{Key: ecKey(t)}, sign.Options{}); err != nil {
			t.Fatalf("Image: %v", err)
		}
		p := sign.Policy{Identities: ids, TrustedMaterial: trustRoot(t, &ca, nil, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true}
		wantRejected(t, keyC, keyRef, p, "verify bundle")
	})
}

func TestVerify_SignedTimestamps(t *testing.T) {
	t.Parallel()
	tsa := newTSA(t)
	c, ref, ca := keylessImage(t, sign.Options{TSAURL: tsa.server(t).URL + "/api/v1/timestamp"})
	p := sign.Policy{
		Identities:      []sign.Identity{{Issuer: testIssuer, Subject: testEmail}},
		TrustedMaterial: trustRoot(t, &ca, &tsa, nil), InsecureIgnoreTlog: true, InsecureIgnoreSCT: true, SignedTimestamps: true,
	}
	got := verifyOne(t, c, ref, p)
	if ts := got.Result.VerifiedTimestamps; len(ts) != 1 || ts[0].Type != "TimestampAuthority" {
		t.Fatalf("verified timestamps = %+v", ts)
	}
	other := newTSA(t)
	p.TrustedMaterial = trustRoot(t, &ca, &other, nil)
	wantRejected(t, c, ref, p, "timestamp")

	host := newRegistry(t, nil)
	keyRef, _ := seed(t, host, false)
	key := ecKey(t)
	if _, err := sign.Image(testCtx(t), c, keyRef, sign.Signer{Key: key}, sign.Options{}); err != nil {
		t.Fatalf("Image: %v", err)
	}
	wantRejected(t, c, keyRef, sign.Policy{Key: key.Public(), TrustedMaterial: trustRoot(t, nil, &tsa, nil), InsecureIgnoreTlog: true, SignedTimestamps: true}, "timestamp")
}

// TestVerify_TransparencyLog runs the default policy, which requires a
// verified Rekor entry, against a log the trusted root holds and one it
// does not.
func TestVerify_TransparencyLog(t *testing.T) {
	t.Parallel()
	log := newLog(t)
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	unlogged, _ := seed(t, host, false)
	c := newClient(t)
	key := ecKey(t)
	for _, r := range []struct {
		ref  string
		opts sign.Options
	}{{ref, sign.Options{RekorURL: log.server(t).URL}}, {unlogged, sign.Options{}}} {
		if _, err := sign.Image(testCtx(t), c, r.ref, sign.Signer{Key: key}, r.opts); err != nil {
			t.Fatalf("Image: %v", err)
		}
	}
	p := sign.Policy{Key: key.Public(), TrustedMaterial: trustRoot(t, nil, nil, log)}
	if got := verifyOne(t, c, ref, p); len(got.Bundle) == 0 {
		t.Fatal("empty bundle")
	}
	wantRejected(t, c, unlogged, p, "log entries")
	wantRejected(t, c, ref, sign.Policy{Key: key.Public(), TrustedMaterial: trustRoot(t, nil, nil, newLog(t))}, "log entries")

	kc, kref, ca := keylessImage(t, sign.Options{RekorURL: log.server(t).URL})
	kp := sign.Policy{
		Identities:      []sign.Identity{{Issuer: testIssuer, Subject: testEmail}},
		TrustedMaterial: trustRoot(t, &ca, nil, log), InsecureIgnoreSCT: true,
	}
	if ts := verifyOne(t, kc, kref, kp).Result.VerifiedTimestamps; len(ts) != 1 || ts[0].Type != "Tlog" {
		t.Fatalf("verified timestamps = %+v", ts)
	}
}

func TestVerify_Annotations(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	c := newClient(t)
	key := ecKey(t)
	opts := sign.Options{Annotations: map[string]string{"build": "rc4", "team": "core"}}
	if _, err := sign.Image(testCtx(t), c, ref, sign.Signer{Key: key}, opts); err != nil {
		t.Fatalf("Image: %v", err)
	}
	cases := []struct {
		name string
		want map[string]string
		ok   bool
	}{
		{name: "none", ok: true},
		{name: "subset", want: map[string]string{"build": "rc4"}, ok: true},
		{name: "all", want: map[string]string{"build": "rc4", "team": "core"}, ok: true},
		{name: "other value", want: map[string]string{"build": "rc5"}},
		{name: "absent key", want: map[string]string{"stage": "prod"}},
		{name: "absent key, empty value", want: map[string]string{"stage": ""}},
		{name: "one of two wrong", want: map[string]string{"build": "rc4", "team": "edge"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := keyPolicy(key.Public())
			p.Annotations = tc.want
			if tc.ok {
				verifyOne(t, c, ref, p)
				return
			}
			wantRejected(t, c, ref, p, "annotation")
		})
	}
}

// TestVerify_RejectsForeignBundles pushes one crafted referrer per case, each
// signed by the policy key, and expects the check that catches it.
func TestVerify_RejectsForeignBundles(t *testing.T) {
	t.Parallel()
	kp, err := sgsign.NewEphemeralKeypair(nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, reason string
		bundle       func(t *testing.T, subject v1.Descriptor, other v1.Hash, manifest []byte) []byte
		layer        types.MediaType
	}{
		{
			name: "signature of another image", reason: "verify bundle",
			bundle: func(t *testing.T, _ v1.Descriptor, other v1.Hash, _ []byte) []byte {
				t.Helper()
				return signContent(t, kp, statementFor(t, other, sign.PredicateType))
			},
		},
		{
			name: "payload changed after signing", reason: "verify bundle",
			bundle: func(t *testing.T, s v1.Descriptor, _ v1.Hash, _ []byte) []byte {
				t.Helper()
				raw := signContent(t, kp, statementFor(t, s.Digest, sign.PredicateType))
				return rewrite(t, raw, func(b *protobundle.Bundle) {
					b.GetDsseEnvelope().Payload = append(b.GetDsseEnvelope().GetPayload(), ' ')
				})
			},
		},
		{
			name: "other predicate type", reason: "predicate type",
			bundle: func(t *testing.T, s v1.Descriptor, _ v1.Hash, _ []byte) []byte {
				t.Helper()
				return signContent(t, kp, statementFor(t, s.Digest, "https://slsa.dev/provenance/v1"))
			},
		},
		{
			name: "message signature over the manifest", reason: "predicate type",
			bundle: func(t *testing.T, _ v1.Descriptor, _ v1.Hash, manifest []byte) []byte {
				t.Helper()
				return signContent(t, kp, &sgsign.PlainData{Data: manifest})
			},
		},
		{
			name: "bundle v0.2", reason: "older than v0.3",
			bundle: func(t *testing.T, s v1.Descriptor, _ v1.Hash, _ []byte) []byte {
				t.Helper()
				raw := signContent(t, kp, statementFor(t, s.Digest, sign.PredicateType))
				return rewrite(t, raw, func(b *protobundle.Bundle) {
					b.MediaType = "application/vnd.dev.sigstore.bundle+json;version=0.2"
				})
			},
		},
		{
			name: "not a bundle", reason: "parse bundle",
			bundle: func(*testing.T, v1.Descriptor, v1.Hash, []byte) []byte { return []byte("{}") },
		},
		{
			name: "non-bundle layer", reason: "not a Sigstore bundle", layer: "application/spdx+json",
			bundle: func(*testing.T, v1.Descriptor, v1.Hash, []byte) []byte { return []byte(`{"spdxVersion":"SPDX-2.3"}`) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newRegistry(t, nil, regsrv.WithReferrersSupport(true))
			ref, _ := seed(t, host, false)
			_, other := seed(t, host, false)
			c := newClient(t)
			subject, manifest := subjectOf(t, c, ref)
			layer := tc.layer
			if layer == "" {
				layer = sign.BundleMediaType
			}
			pushReferrer(t, c, ref, subject, string(layer), layer, tc.bundle(t, subject, other, manifest))
			wantRejected(t, c, ref, keyPolicy(kp.GetPublicKey()), tc.reason)
		})
	}
}

// TestVerify_NoBundles is the boundary of nothing to verify: no referrers,
// or only a referrer whose artifactType rules a bundle out.
func TestVerify_NoBundles(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	c := newClient(t)
	p := keyPolicy(ecKey(t).Public())
	wantRejected(t, c, ref, p, "no Sigstore bundle referrers")
	subject, _ := subjectOf(t, c, ref)
	pushReferrer(t, c, ref, subject, "application/spdx+json", "application/spdx+json", []byte(`{"spdxVersion":"SPDX-2.3"}`))
	wantRejected(t, c, ref, p, "no Sigstore bundle referrers")
}

// TestVerify_OneValidAmongMany is the boundary of several referrers: the
// rejected ones do not hide a valid one, and every valid one is returned.
func TestVerify_OneValidAmongMany(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil, regsrv.WithReferrersSupport(true))
	ref, digest := seed(t, host, false)
	c := newClient(t)
	subject, _ := subjectOf(t, c, ref)
	kp, err := sgsign.NewEphemeralKeypair(nil)
	if err != nil {
		t.Fatal(err)
	}
	pushReferrer(t, c, ref, subject, sign.BundleMediaType, sign.BundleMediaType, []byte("{}"))
	pushReferrer(t, c, ref, subject, sign.BundleMediaType, sign.BundleMediaType,
		signContent(t, kp, statementFor(t, digest, "https://slsa.dev/provenance/v1")))
	if _, err = sign.Image(testCtx(t), c, ref, sign.Signer{Key: ecKey(t)}, sign.Options{}); err != nil {
		t.Fatalf("Image: %v", err)
	}
	valid := signContent(t, kp, statementFor(t, digest, sign.PredicateType))
	pushReferrer(t, c, ref, subject, sign.BundleMediaType, sign.BundleMediaType, valid)
	p := sign.Policy{Key: kp.GetPublicKey(), InsecureIgnoreTlog: true}
	if got := verifyOne(t, c, ref, p); !bytes.Equal(got.Bundle, valid) {
		t.Fatalf("verified bundle = %s", got.Bundle)
	}
	pushReferrer(t, c, ref, subject, sign.BundleMediaType, sign.BundleMediaType,
		signContent(t, kp, statementFor(t, digest, sign.PredicateType)))
	got, err := sign.Verify(testCtx(t), c, ref, p)
	if err != nil || len(got) != 2 || got[0].Descriptor.Digest == got[1].Descriptor.Digest {
		t.Fatalf("Verify = %+v, %v; want both valid signatures", got, err)
	}
}

func TestVerify_RejectsBeforeIO(t *testing.T) {
	t.Parallel()
	pub := ecKey(t).Public()
	tm := trustRoot(t, nil, nil, nil)
	id := sign.Identity{Issuer: testIssuer, Subject: testEmail}
	const digestRef = "registry.example/app@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	cases := []struct {
		name string
		ref  string
		p    sign.Policy
		want error
	}{
		{name: "tag only", ref: "registry.example/app:v1", p: keyPolicy(pub), want: sign.ErrDigestRequired},
		{name: "repository only", ref: "registry.example/app", p: keyPolicy(pub), want: sign.ErrDigestRequired},
		{name: "empty policy", ref: digestRef, want: sign.ErrInvalidOptions},
		{name: "typed nil key", ref: digestRef, p: keyPolicy((*ecdsa.PublicKey)(nil)), want: sign.ErrInvalidOptions},
		{name: "key and identities", ref: digestRef, p: sign.Policy{Key: pub, Identities: []sign.Identity{id}, TrustedMaterial: tm}, want: sign.ErrInvalidOptions},
		{name: "keyless without trust", ref: digestRef, p: sign.Policy{Identities: []sign.Identity{id}, InsecureIgnoreTlog: true}, want: sign.ErrInvalidOptions},
		{name: "keyless typed nil trust", ref: digestRef, p: sign.Policy{Identities: []sign.Identity{id}, TrustedMaterial: (*root.TrustedRoot)(nil)}, want: sign.ErrInvalidOptions},
		{name: "key tlog without trust", ref: digestRef, p: sign.Policy{Key: pub}, want: sign.ErrInvalidOptions},
		{name: "key timestamps without trust", ref: digestRef, p: sign.Policy{Key: pub, InsecureIgnoreTlog: true, SignedTimestamps: true}, want: sign.ErrInvalidOptions},
		{name: "unsupported key", ref: digestRef, p: keyPolicy("not a key"), want: sign.ErrInvalidOptions},
		{name: "identity without subject", ref: digestRef, p: sign.Policy{Identities: []sign.Identity{{Issuer: testIssuer}}, TrustedMaterial: tm}, want: sign.ErrInvalidOptions},
		{name: "identity without issuer", ref: digestRef, p: sign.Policy{Identities: []sign.Identity{{Subject: testEmail}}, TrustedMaterial: tm}, want: sign.ErrInvalidOptions},
		{name: "bad regexp", ref: digestRef, p: sign.Policy{Identities: []sign.Identity{{Issuer: testIssuer, SubjectRegexp: "("}}, TrustedMaterial: tm}, want: sign.ErrInvalidOptions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := registry.New(registry.Options{}, authn.NewMultiKeychain(), failingTransport{t})
			if _, err := sign.Verify(testCtx(t), c, tc.ref, tc.p); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := sign.Verify(t.Context(), nil, digestRef, keyPolicy(pub)); !errors.Is(err, sign.ErrInvalidOptions) {
		t.Fatalf("nil client err = %v", err)
	}
}

func TestVerify_SubjectMissing(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	_, err := sign.Verify(testCtx(t), newClient(t), host+"/app@sha256:"+strings.Repeat("a", 64), keyPolicy(ecKey(t).Public()))
	if err == nil || errors.Is(err, sign.ErrNoValidSignature) {
		t.Fatalf("err = %v, want a subject error", err)
	}
}

// cancelAfterReferrers cancels once the referrers listing has been read, so
// the next registry call would fail on the context.
type cancelAfterReferrers struct {
	rt     http.RoundTripper
	cancel context.CancelFunc
}

func (c cancelAfterReferrers) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(r)
	if err == nil && strings.Contains(r.URL.Path, "/referrers/") {
		resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: c.cancel}
	}
	return resp, err
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}

// TestVerify_StopsOnCanceledContext reports the cancellation itself, not a
// missing signature, when the context ends between referrers.
func TestVerify_StopsOnCanceledContext(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil, regsrv.WithReferrersSupport(true))
	ref, _ := seed(t, host, false)
	key := ecKey(t)
	if _, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: key}, sign.Options{}); err != nil {
		t.Fatalf("Image: %v", err)
	}
	ctx, cancel := context.WithCancel(testCtx(t))
	defer cancel()
	c := registry.New(registry.Options{}, authn.NewMultiKeychain(), cancelAfterReferrers{rt: newTransport(t), cancel: cancel})
	_, err := sign.Verify(ctx, c, ref, keyPolicy(key.Public()))
	if !errors.Is(err, context.Canceled) || errors.Is(err, sign.ErrNoValidSignature) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

// TestVerify_AnnotationsOnThisSubject reads annotations only from the
// subject naming the verified digest, not from a second subject.
func TestVerify_AnnotationsOnThisSubject(t *testing.T) {
	t.Parallel()
	kp, err := sgsign.NewEphemeralKeypair(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, onThis := range []bool{false, true} {
		host := newRegistry(t, nil)
		ref, digest := seed(t, host, false)
		_, other := seed(t, host, false)
		c := newClient(t)
		subject, _ := subjectOf(t, c, ref)
		this := map[string]any{"digest": map[string]string{"sha256": digest.Hex}}
		that := map[string]any{"digest": map[string]string{"sha256": other.Hex}, "annotations": map[string]string{"build": "rc4"}}
		if onThis {
			this["annotations"], that["annotations"] = that["annotations"], nil
		}
		raw, err := json.Marshal(map[string]any{
			"_type": "https://in-toto.io/Statement/v1", "subject": []any{that, this},
			"predicateType": sign.PredicateType, "predicate": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		pushReferrer(t, c, ref, subject, sign.BundleMediaType, sign.BundleMediaType,
			signContent(t, kp, &sgsign.DSSEData{Data: raw, PayloadType: "application/vnd.in-toto+json"}))
		p := sign.Policy{Key: kp.GetPublicKey(), InsecureIgnoreTlog: true, Annotations: map[string]string{"build": "rc4"}}
		if onThis {
			verifyOne(t, c, ref, p)
			continue
		}
		wantRejected(t, c, ref, p, "annotation")
	}
}
