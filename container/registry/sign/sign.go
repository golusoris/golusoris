// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sign signs OCI images, indexes and artifacts by digest with
// Sigstore, in process, through sigstore-go: no cosign binary and no exec.
//
// [Image] signs a DSSE envelope over an in-toto statement whose subject is
// the manifest digest and whose predicate type is [PredicateType], wraps it
// in a Sigstore bundle ([BundleMediaType]) and pushes the bundle as an OCI
// 1.1 referrer of that manifest. That is the layout `cosign sign` writes
// with the new bundle format (the cosign v3 default), so `cosign verify`
// accepts the signature.
//
// The signing key is a caller-held [crypto.Signer] (in memory, PKCS#11 or
// KMS-backed), an ephemeral key certified by Fulcio for an OIDC identity
// token (keyless), or a caller key certified by Fulcio. Rekor and an RFC 3161
// timestamp authority are optional.
//
// [Verify] reads that layout back, from [Image] or `cosign sign`, and checks
// each bundle against a [Policy]: a public key or keyless certificate
// identities, the transparency log, timestamp and SCT requirements `cosign
// verify` applies, and the subject annotations.
//
// [ImageLayout] and [VerifyLayout] do the same inside an OCI image-layout
// directory ([registry.Layout]), offline unless the signer or policy names a
// Sigstore service; [registry.Client.CopyFromLayout] and
// [registry.Client.CopyToLayout] carry the signatures between layout and
// registry. [GitHubToken] and [FileToken] supply the OIDC identity token for
// keyless signing in GitHub Actions and Kubernetes.
package sign

import (
	"bytes"
	"context"
	"crypto"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	intoto "github.com/in-toto/attestation/go/v1"
	"github.com/jonboulle/clockwork"
	sgsign "github.com/sigstore/sigstore-go/pkg/sign"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/core/clock"
)

const (
	// BundleMediaType is the Sigstore bundle v0.3 media type: the referrer's
	// artifactType and the media type of its single layer.
	BundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
	// PredicateType is the in-toto predicate type `cosign sign` uses for a
	// plain image signature.
	PredicateType = "https://sigstore.dev/cosign/sign/v1"
	// DefaultTimeout bounds the whole Sigstore exchange (ID token, Fulcio,
	// timestamp authority, Rekor) when Options.Timeout is zero.
	DefaultTimeout = 2 * time.Minute
)

// Referrer manifest annotations, as `cosign sign` writes them.
const (
	AnnotationCreated       = "org.opencontainers.image.created"
	AnnotationBundleContent = "dev.sigstore.bundle.content"
	AnnotationPredicateType = "dev.sigstore.bundle.predicateType"
)

const (
	dssePayloadType = "application/vnd.in-toto+json"
	bundleContent   = "dsse-envelope"
	// serviceRetries is how often sigstore-go retries a 5xx or 429 answer.
	serviceRetries = 1
)

// Sentinel errors; match with [errors.Is].
var (
	// ErrDigestRequired reports a reference without a digest. Signing a tag
	// would sign whatever the tag points at when the registry answers.
	ErrDigestRequired = errors.New("sign: reference must name a digest")
	// ErrInvalidOptions reports an unusable [Signer] or [Options].
	ErrInvalidOptions = errors.New("sign: invalid options")
)

// Signer is the key a signature is made with and what binds its public half
// to an identity.
type Signer struct {
	// Key signs the payload: an in-memory key, a PKCS#11 handle or a
	// KMS-backed [crypto.Signer]. Nil signs with an ephemeral ECDSA P-256 key
	// that is discarded afterwards, which needs IDToken (keyless signing).
	Key crypto.Signer
	// IDToken returns the OIDC identity token Fulcio exchanges for a
	// short-lived code-signing certificate over Key. Nil binds the signature
	// to Key's public key only. Requires Options.FulcioURL.
	IDToken func(ctx context.Context) (string, error)
}

// Options selects the Sigstore services and bounds of one signature.
type Options struct {
	// FulcioURL is the Fulcio base URL, e.g. "https://fulcio.sigstore.dev".
	// Required with Signer.IDToken.
	FulcioURL string `koanf:"fulcio_url"`
	// RekorURL records the signature in this transparency log, e.g.
	// "https://rekor.sigstore.dev". Empty records nothing; verifiers then
	// need `cosign verify --insecure-ignore-tlog`.
	RekorURL string `koanf:"rekor_url"`
	// RekorVersion is the Rekor API major version: 0 or 1 for Rekor v1, 2
	// for Rekor v2. Keyless signing with Rekor v2 needs TSAURL.
	RekorVersion uint32 `koanf:"rekor_version"`
	// TSAURL is the full RFC 3161 timestamp endpoint, e.g.
	// "https://timestamp.sigstore.dev/api/v1/timestamp". Empty requests no
	// signed timestamp.
	TSAURL string `koanf:"tsa_url"`
	// Annotations become the in-toto subject annotations, as
	// `cosign sign -a key=value` sets them; `cosign verify -a` checks them.
	Annotations map[string]string `koanf:"annotations"`
	// Timeout bounds the Sigstore exchange and each request in it. Zero uses
	// [DefaultTimeout]. Registry calls are bounded by the [registry.Client].
	Timeout time.Duration `koanf:"timeout"`
	// Transport carries Fulcio and timestamp requests. Nil uses a private
	// clone of [http.DefaultTransport]. sigstore-go's Rekor clients use their
	// own transport.
	Transport http.RoundTripper `koanf:"-"`
	// Clock stamps [AnnotationCreated]. Nil uses the wall clock.
	Clock clock.Clock `koanf:"-"`
}

// Signature is one pushed signature.
type Signature struct {
	// Descriptor is the referrer manifest holding the bundle.
	Descriptor v1.Descriptor
	// Subject is the signed manifest.
	Subject v1.Descriptor
	// Bundle is the Sigstore bundle JSON stored as the referrer's layer.
	Bundle []byte
}

// Image signs the manifest ref names and pushes the signature as a referrer
// of it. ref must carry a digest ("registry/repo@sha256:..." or
// "registry/repo:tag@sha256:..."); a tag alone is [ErrDigestRequired]. The
// manifest may be an image, an index or an OCI artifact. Options and ref are
// checked before any network I/O.
func Image(ctx context.Context, c *registry.Client, ref string, s Signer, opts Options) (Signature, error) {
	if c == nil {
		return Signature{}, fmt.Errorf("%w: nil registry client", ErrInvalidOptions)
	}
	if err := validateSigner(s, opts); err != nil {
		return Signature{}, err
	}
	d, h, err := parseDigest(ref)
	if err != nil {
		return Signature{}, err
	}
	return signIn(ctx, remoteStore{c: c, repo: d.Context()}, h, s, opts)
}

// ImageLayout signs the manifest digest names in the OCI image layout l and
// stores the signature in l: the referrer manifest [Image] pushes, listed in
// l's index.json, so [VerifyLayout] reads it offline and
// [registry.Client.CopyFromLayout] carries it to a registry where `cosign
// verify` and [Verify] accept it. No network I/O unless s or opts name
// Fulcio, Rekor or a timestamp authority. A zero digest is
// [ErrDigestRequired]; options are checked before any I/O.
func ImageLayout(ctx context.Context, l *registry.Layout, digest v1.Hash, s Signer, opts Options) (Signature, error) {
	if l == nil {
		return Signature{}, fmt.Errorf("%w: nil layout", ErrInvalidOptions)
	}
	if err := validateSigner(s, opts); err != nil {
		return Signature{}, err
	}
	if digest == (v1.Hash{}) {
		return Signature{}, ErrDigestRequired
	}
	return signIn(ctx, layoutStore{l: l}, digest, s, opts)
}

// signIn signs manifest h of st and stores the bundle there as its referrer.
func signIn(ctx context.Context, st store, h v1.Hash, s Signer, opts Options) (Signature, error) {
	subject, err := subjectOf(ctx, st, h)
	if err != nil {
		return Signature{}, err
	}
	payload, err := statement(subject.Digest, opts.Annotations)
	if err != nil {
		return Signature{}, err
	}
	raw, err := signBundle(ctx, payload, s, opts)
	if err != nil {
		return Signature{}, err
	}
	desc, err := push(ctx, st, subject, raw, opts.Clock)
	if err != nil {
		return Signature{}, err
	}
	return Signature{Descriptor: desc, Subject: subject, Bundle: raw}, nil
}

func parseDigest(ref string) (name.Digest, v1.Hash, error) {
	r, err := registry.ParseReference(ref)
	if err != nil {
		return name.Digest{}, v1.Hash{}, fmt.Errorf("sign: %w", err)
	}
	d, ok := r.(name.Digest)
	if !ok {
		return name.Digest{}, v1.Hash{}, fmt.Errorf("%w: %q", ErrDigestRequired, ref)
	}
	h, err := v1.NewHash(d.DigestStr())
	if err != nil {
		return name.Digest{}, v1.Hash{}, fmt.Errorf("sign: %w", err)
	}
	return d, h, nil
}

func validateSigner(s Signer, o Options) error {
	if s.Key == nil && s.IDToken == nil {
		return fmt.Errorf("%w: signer needs a key, an ID token source, or both", ErrInvalidOptions)
	}
	if (s.IDToken == nil) != (o.FulcioURL == "") {
		return fmt.Errorf("%w: an ID token source and a Fulcio URL go together", ErrInvalidOptions)
	}
	if err := validateRekor(s, o); err != nil {
		return err
	}
	for _, u := range []string{o.FulcioURL, o.RekorURL, o.TSAURL} {
		if err := checkURL(u); err != nil {
			return err
		}
	}
	return nil
}

func validateRekor(s Signer, o Options) error {
	if o.RekorVersion > 2 {
		return fmt.Errorf("%w: rekor version %d", ErrInvalidOptions, o.RekorVersion)
	}
	if o.RekorVersion == 2 && s.IDToken != nil && o.TSAURL == "" {
		return fmt.Errorf("%w: keyless signing with Rekor v2 needs a timestamp authority", ErrInvalidOptions)
	}
	if o.RekorURL == "" && o.RekorVersion != 0 {
		return fmt.Errorf("%w: rekor version without a Rekor URL", ErrInvalidOptions)
	}
	return nil
}

func checkURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: service URL: %w", ErrInvalidOptions, err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: service URL %q is not absolute http(s)", ErrInvalidOptions, raw)
	}
	return nil
}

// subjectOf reads manifest h; registry and layout reads reject a body that
// does not hash to h.
func subjectOf(ctx context.Context, st store, h v1.Hash) (v1.Descriptor, error) {
	m, err := st.manifest(ctx, h)
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("sign: subject: %w", err)
	}
	if m.Digest != h {
		return v1.Descriptor{}, fmt.Errorf("%w: subject %s served as %s", registry.ErrDigestMismatch, h, m.Digest)
	}
	return v1.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}, nil
}

// statement builds the in-toto statement `cosign sign` signs: the manifest
// digest as sole subject, [PredicateType], an empty predicate.
func statement(digest v1.Hash, annotations map[string]string) ([]byte, error) {
	fields := make(map[string]any, len(annotations))
	for k, v := range annotations {
		fields[k] = v
	}
	ann, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: annotations: %w", ErrInvalidOptions, err)
	}
	st := &intoto.Statement{
		Type: intoto.StatementTypeUri,
		Subject: []*intoto.ResourceDescriptor{{
			Digest:      map[string]string{digest.Algorithm: digest.Hex},
			Annotations: ann,
		}},
		PredicateType: PredicateType,
		Predicate:     &structpb.Struct{},
	}
	if err = st.Validate(); err != nil {
		return nil, fmt.Errorf("sign: statement: %w", err)
	}
	raw, err := protojson.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("sign: encode statement: %w", err)
	}
	return raw, nil
}

// signBundle signs payload and returns the bundle as protobuf JSON, the
// encoding cosign stores.
func signBundle(ctx context.Context, payload []byte, s Signer, o Options) ([]byte, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport, release := ownTransport(o.Transport)
	defer release()
	kp, err := keypairFor(s.Key)
	if err != nil {
		return nil, err
	}
	bo, err := bundleOptions(ctx, s, o, timeout, transport)
	if err != nil {
		return nil, err
	}
	content := &sgsign.DSSEData{Data: payload, PayloadType: dssePayloadType}
	b, err := sgsign.Bundle(content, kp, bo)
	if err != nil {
		return nil, fmt.Errorf("sign: sign bundle: %w", err)
	}
	raw, err := protojson.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("sign: encode bundle: %w", err)
	}
	return raw, nil
}

func bundleOptions(ctx context.Context, s Signer, o Options, timeout time.Duration, rt http.RoundTripper) (sgsign.BundleOptions, error) {
	bo := sgsign.BundleOptions{Context: ctx}
	if s.IDToken != nil {
		tok, err := s.IDToken(ctx)
		if err != nil {
			return sgsign.BundleOptions{}, fmt.Errorf("sign: id token: %w", err)
		}
		if tok == "" {
			return sgsign.BundleOptions{}, fmt.Errorf("%w: empty ID token", ErrInvalidOptions)
		}
		bo.CertificateProvider = sgsign.NewFulcio(&sgsign.FulcioOptions{
			BaseURL: o.FulcioURL, Timeout: timeout, Retries: serviceRetries, Transport: rt,
		})
		bo.CertificateProviderOptions = &sgsign.CertificateProviderOptions{IDToken: tok}
	}
	if o.TSAURL != "" {
		bo.TimestampAuthorities = []*sgsign.TimestampAuthority{sgsign.NewTimestampAuthority(&sgsign.TimestampAuthorityOptions{
			URL: o.TSAURL, Timeout: timeout, Retries: serviceRetries, Transport: rt,
		})}
	}
	if o.RekorURL != "" {
		bo.TransparencyLogs = []sgsign.Transparency{sgsign.NewRekor(&sgsign.RekorOptions{
			BaseURL: o.RekorURL, Timeout: timeout, Retries: serviceRetries, Version: max(o.RekorVersion, 1),
		})}
	}
	return bo, nil
}

// ownTransport returns rt, or a private clone of http.DefaultTransport and the
// function that closes its idle connections once signing is done.
func ownTransport(rt http.RoundTripper) (http.RoundTripper, func()) {
	if rt != nil {
		return rt, func() {}
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport, func() {}
	}
	own := base.Clone()
	return own, own.CloseIdleConnections
}

// push stores the bundle the way `cosign sign` does: artifactType and single
// layer [BundleMediaType], empty config, subject = the signed manifest.
func push(ctx context.Context, st store, subject v1.Descriptor, bundle []byte, clk clock.Clock) (v1.Descriptor, error) {
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	desc, err := st.push(ctx, registry.Artifact{
		ArtifactType: BundleMediaType,
		Blobs:        []registry.Blob{{MediaType: BundleMediaType, Reader: bytes.NewReader(bundle)}},
		Annotations: map[string]string{
			AnnotationCreated:       clk.Now().UTC().Format(time.RFC3339),
			AnnotationBundleContent: bundleContent,
			AnnotationPredicateType: PredicateType,
		},
		Subject: &subject,
	})
	if err != nil {
		return v1.Descriptor{}, fmt.Errorf("sign: push signature: %w", err)
	}
	return desc, nil
}
