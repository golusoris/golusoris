// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign

import (
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	intoto "github.com/in-toto/attestation/go/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/signature"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/core/validate"
)

const (
	// bundleMediaTypePrefix matches every Sigstore bundle media type, as
	// cosign's bundle reader does.
	bundleMediaTypePrefix = "application/vnd.dev.sigstore.bundle"
	// maxBundleBytes caps one fetched bundle; cosign bundles are a few KiB.
	maxBundleBytes = 1 << 20
)

// ErrNoValidSignature reports that no referrer of the manifest holds a
// bundle the [Policy] accepts. It wraps the reason each bundle was rejected.
var ErrNoValidSignature = errors.New("sign: no valid signature")

var (
	errNotBundle     = errors.New("sign: not a Sigstore bundle referrer")
	errBundleVersion = errors.New("sign: bundle older than v0.3")
	errPredicateType = errors.New("sign: predicate type is not " + PredicateType)
	errAnnotations   = errors.New("sign: missing or mismatched annotation")
)

// Policy is what [Verify] accepts. Set Key or Identities, not both. The
// zero values of the Insecure* and SignedTimestamps fields match `cosign
// verify` without flags: a verified Rekor entry is required, and keyless
// certificates must carry an SCT.
type Policy struct {
	// Key accepts bundles signed by this public key that carry no
	// certificate (`cosign verify --key`).
	Key crypto.PublicKey `koanf:"-"`
	// Identities accept bundles whose Fulcio certificate chains to a CA in
	// TrustedMaterial and matches any one of them (keyless).
	Identities []Identity `koanf:"identities"`
	// TrustedMaterial holds the Fulcio CAs, Rekor and CT log keys and
	// timestamp authorities, e.g. the public-good root from
	// root.FetchTrustedRoot. Required unless Key is set with
	// InsecureIgnoreTlog and without SignedTimestamps.
	TrustedMaterial root.TrustedMaterial `koanf:"-"`
	// Annotations must all appear, as strings, on the in-toto subject
	// (`cosign verify -a key=value`).
	Annotations map[string]string `koanf:"annotations"`
	// InsecureIgnoreTlog accepts bundles without a verified Rekor entry
	// (`--insecure-ignore-tlog`). Keyless certificates are then checked at
	// the current time unless SignedTimestamps is set.
	InsecureIgnoreTlog bool `koanf:"insecure_ignore_tlog"`
	// InsecureIgnoreSCT accepts Fulcio certificates without an embedded
	// signed certificate timestamp (`--insecure-ignore-sct`).
	InsecureIgnoreSCT bool `koanf:"insecure_ignore_sct"`
	// SignedTimestamps requires an RFC 3161 timestamp from a TSA in
	// TrustedMaterial and checks the certificate at that time
	// (`--use-signed-timestamps`).
	SignedTimestamps bool `koanf:"signed_timestamps"`
}

// Identity is one accepted keyless signer. Each part is an exact value, a
// regular expression, or both (both must match). Expressions are anchored:
// they match the whole value, unlike cosign's substring match.
type Identity struct {
	// Issuer is the OIDC issuer (`--certificate-oidc-issuer`).
	Issuer string `koanf:"issuer"`
	// IssuerRegexp matches the issuer (`--certificate-oidc-issuer-regexp`).
	IssuerRegexp string `koanf:"issuer_regexp"`
	// Subject is the certificate SAN: email, URI or workflow identity
	// (`--certificate-identity`).
	Subject string `koanf:"subject"`
	// SubjectRegexp matches the SAN (`--certificate-identity-regexp`).
	SubjectRegexp string `koanf:"subject_regexp"`
}

// VerifiedSignature is one signature [Verify] accepted.
type VerifiedSignature struct {
	Signature
	// Result is sigstore-go's verification result: the statement, the
	// certificate summary or key hint, and the verified timestamps.
	Result *verify.VerificationResult
}

// Verify checks the Sigstore bundles stored as referrers of the manifest ref
// names, the layout [Image] and `cosign sign` (new bundle format) write, and
// returns every one p accepts. ref must carry a digest; a tag alone is
// [ErrDigestRequired]. A bundle is accepted when sigstore-go verifies it
// under p for that digest, its predicate type is [PredicateType] and its
// subject carries p.Annotations. No accepted bundle is [ErrNoValidSignature].
// The policy and ref are checked before any network I/O.
func Verify(ctx context.Context, c *registry.Client, ref string, p Policy) ([]VerifiedSignature, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil registry client", ErrInvalidOptions)
	}
	chk, err := newChecker(p)
	if err != nil {
		return nil, err
	}
	d, err := parseDigest(ref)
	if err != nil {
		return nil, err
	}
	subject, err := subjectOf(ctx, c, d)
	if err != nil {
		return nil, err
	}
	refs, err := c.Referrers(ctx, d.String(), "")
	if err != nil {
		return nil, fmt.Errorf("sign: list referrers: %w", err)
	}
	return chk.referrers(ctx, c, d, subject, refs)
}

type checker struct {
	v           *verify.Verifier
	policy      []verify.PolicyOption
	annotations map[string]string
}

func newChecker(p Policy) (*checker, error) {
	if err := validatePolicy(p); err != nil {
		return nil, err
	}
	tm, popts, err := trustFor(p)
	if err != nil {
		return nil, err
	}
	v, err := verify.NewVerifier(tm, verifierOptions(p)...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
	}
	return &checker{v: v, policy: popts, annotations: p.Annotations}, nil
}

func validatePolicy(p Policy) error {
	hasKey, hasTrust := !validate.IsNil(p.Key), !validate.IsNil(p.TrustedMaterial)
	switch {
	case !hasKey && len(p.Identities) == 0:
		return fmt.Errorf("%w: policy needs a key or certificate identities", ErrInvalidOptions)
	case hasKey && len(p.Identities) > 0:
		return fmt.Errorf("%w: policy takes a key or certificate identities, not both", ErrInvalidOptions)
	case !hasTrust && (!hasKey || !p.InsecureIgnoreTlog || p.SignedTimestamps):
		return fmt.Errorf("%w: keyless, transparency log and timestamp checks need trusted material", ErrInvalidOptions)
	}
	return nil
}

// trustFor returns the material and policy options for p: the key as the
// only trusted public key, or the identities against p.TrustedMaterial.
func trustFor(p Policy) (root.TrustedMaterial, []verify.PolicyOption, error) {
	if validate.IsNil(p.Key) {
		ids, err := identities(p.Identities)
		if err != nil {
			return nil, nil, err
		}
		return p.TrustedMaterial, ids, nil
	}
	sv, err := signature.LoadDefaultVerifier(p.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: key: %w", ErrInvalidOptions, err)
	}
	key := root.NewExpiringKey(sv, time.Time{}, time.Time{})
	tm := root.TrustedMaterialCollection{root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) {
		return key, nil
	})}
	if !validate.IsNil(p.TrustedMaterial) {
		tm = append(tm, p.TrustedMaterial)
	}
	return tm, []verify.PolicyOption{verify.WithKey()}, nil
}

func identities(ids []Identity) ([]verify.PolicyOption, error) {
	opts := make([]verify.PolicyOption, 0, len(ids))
	for _, id := range ids {
		ci, err := verify.NewShortCertificateIdentity(id.Issuer, anchor(id.IssuerRegexp), id.Subject, anchor(id.SubjectRegexp))
		if err != nil {
			return nil, fmt.Errorf("%w: identity: %w", ErrInvalidOptions, err)
		}
		opts = append(opts, verify.WithCertificateIdentity(ci))
	}
	return opts, nil
}

// anchor makes re match the whole value; sigstore-go matches substrings.
func anchor(re string) string {
	if re == "" {
		return ""
	}
	return "^(?:" + re + ")$"
}

// verifierOptions maps p the way cosign v3 maps its verify flags
// (CheckOpts.verificationOptions).
func verifierOptions(p Policy) []verify.VerifierOption {
	keyless := validate.IsNil(p.Key)
	var opts []verify.VerifierOption
	if keyless && !p.InsecureIgnoreSCT {
		opts = append(opts, verify.WithSignedCertificateTimestamps(1))
	}
	if !p.InsecureIgnoreTlog {
		opts = append(opts, verify.WithTransparencyLog(1))
	}
	switch {
	case p.SignedTimestamps:
		opts = append(opts, verify.WithSignedTimestamps(1))
	case !keyless:
		opts = append(opts, verify.WithNoObserverTimestamps())
	case p.InsecureIgnoreTlog:
		opts = append(opts, verify.WithCurrentTime())
	default:
		opts = append(opts, verify.WithIntegratedTimestamps(1))
	}
	return opts
}

// referrers verifies each referrer that may hold a bundle; one failing
// bundle does not hide another that passes.
func (k *checker) referrers(ctx context.Context, c *registry.Client, d name.Digest, subject v1.Descriptor, refs []v1.Descriptor) ([]VerifiedSignature, error) {
	var (
		ok      []VerifiedSignature
		reasons []error
	)
	for _, desc := range refs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("sign: verify: %w", err)
		}
		if !maybeBundle(desc) {
			continue
		}
		sig, err := k.referrer(ctx, c, d.Context(), subject, desc.Digest)
		if err != nil {
			reasons = append(reasons, fmt.Errorf("referrer %s: %w", desc.Digest, err))
			continue
		}
		ok = append(ok, sig)
	}
	if len(ok) > 0 {
		return ok, nil
	}
	if len(reasons) == 0 {
		return nil, fmt.Errorf("%w: %s has no Sigstore bundle referrers", ErrNoValidSignature, d.DigestStr())
	}
	return nil, fmt.Errorf("%w: %s: %w", ErrNoValidSignature, d.DigestStr(), errors.Join(reasons...))
}

// maybeBundle skips referrers whose artifactType rules a bundle out. A
// registry that reports config.mediaType there (ggcr's) or nothing gets the
// manifest checked instead.
func maybeBundle(d v1.Descriptor) bool {
	return d.ArtifactType == "" || d.ArtifactType == string(registry.EmptyJSONMediaType) ||
		strings.HasPrefix(d.ArtifactType, bundleMediaTypePrefix)
}

func (k *checker) referrer(ctx context.Context, c *registry.Client, repo name.Repository, subject v1.Descriptor, h v1.Hash) (VerifiedSignature, error) {
	desc, man, err := c.ArtifactManifest(ctx, repo.Digest(h.String()).String())
	if err != nil {
		return VerifiedSignature{}, fmt.Errorf("sign: fetch referrer: %w", err)
	}
	if len(man.Layers) != 1 || !strings.HasPrefix(string(man.Layers[0].MediaType), bundleMediaTypePrefix) {
		return VerifiedSignature{}, errNotBundle
	}
	raw, err := c.FetchBlob(ctx, repo.Name(), man.Layers[0], maxBundleBytes)
	if err != nil {
		return VerifiedSignature{}, fmt.Errorf("sign: fetch bundle: %w", err)
	}
	res, err := k.check(raw, subject.Digest)
	if err != nil {
		return VerifiedSignature{}, err
	}
	desc.Annotations = man.Annotations
	return VerifiedSignature{Descriptor: desc, Subject: subject, Bundle: raw, Result: res}, nil
}

// check verifies one bundle for digest, then applies what sigstore-go leaves
// to the caller: bundle version, predicate type, subject annotations.
func (k *checker) check(raw []byte, digest v1.Hash) (*verify.VerificationResult, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(raw); err != nil {
		return nil, fmt.Errorf("sign: parse bundle: %w", err)
	}
	if !b.MinVersion("v0.3") {
		return nil, errBundleVersion
	}
	want, err := hex.DecodeString(digest.Hex)
	if err != nil {
		return nil, fmt.Errorf("sign: subject digest: %w", err)
	}
	res, err := k.v.Verify(&b, verify.NewPolicy(verify.WithArtifactDigest(digest.Algorithm, want), k.policy...))
	if err != nil {
		return nil, fmt.Errorf("sign: verify bundle: %w", err)
	}
	if res.Statement.GetPredicateType() != PredicateType {
		return nil, errPredicateType
	}
	if !annotated(res.Statement, digest, k.annotations) {
		return nil, errAnnotations
	}
	return res, nil
}

// annotated reports whether a statement subject naming digest carries every
// wanted annotation as a string, the check `cosign verify -a` makes.
func annotated(st *intoto.Statement, digest v1.Hash, want map[string]string) bool {
	for _, s := range st.GetSubject() {
		if s.GetDigest()[digest.Algorithm] == digest.Hex && hasAll(s.GetAnnotations(), want) {
			return true
		}
	}
	return false
}

func hasAll(have *structpb.Struct, want map[string]string) bool {
	for k, v := range want {
		f := have.GetFields()[k]
		if _, isString := f.GetKind().(*structpb.Value_StringValue); !isString || f.GetStringValue() != v {
			return false
		}
	}
	return true
}
