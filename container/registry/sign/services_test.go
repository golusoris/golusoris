// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sign_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/digitorus/timestamp"
	"github.com/go-openapi/runtime"
	"github.com/sigstore/rekor/pkg/generated/models"
	"github.com/sigstore/rekor/pkg/types"
	"github.com/sigstore/rekor/pkg/util"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/signature"

	"github.com/golusoris/golusoris/container/registry/sign"
)

const (
	testEmail  = "ci@example.com"
	testIssuer = "https://issuer.example.com"
)

var (
	oidFulcioIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	oidExtKeyUsage    = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidTimeStamping   = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
)

// idToken is an unsigned JWT: Fulcio verifies tokens, sigstore-go only reads
// the subject for the proof of possession.
func idToken(t *testing.T) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{"sub": testEmail, "iss": testIssuer})
	if err != nil {
		t.Fatal(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newCA(t *testing.T, cn string) testCA {
	t.Helper()
	key := ecKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	return testCA{cert: issue(t, tmpl, tmpl, key.Public(), key), key: key}
}

func (ca testCA) leaf(t *testing.T, tmpl *x509.Certificate, pub crypto.PublicKey) *x509.Certificate {
	t.Helper()
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	tmpl.NotBefore = time.Now().Add(-time.Minute)
	tmpl.NotAfter = time.Now().Add(10 * time.Minute)
	return issue(t, tmpl, ca.cert, pub, ca.key)
}

func issue(t *testing.T, tmpl, parent *x509.Certificate, pub crypto.PublicKey, key crypto.Signer) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func certPEM(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

// fulcioServer issues a code-signing certificate for the requested key after
// checking the bearer token and the proof of possession over the subject.
func fulcioServer(t *testing.T, ca testCA, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/signingCert" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			PublicKeyRequest struct {
				PublicKey         struct{ Algorithm, Content string }
				ProofOfPossession string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pub, err := popKey(req.PublicKeyRequest.PublicKey.Content, req.PublicKeyRequest.ProofOfPossession)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		issuer, _ := asn1.MarshalWithParams(testIssuer, "utf8")
		leaf := ca.leaf(t, &x509.Certificate{
			KeyUsage:        x509.KeyUsageDigitalSignature,
			ExtKeyUsage:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			EmailAddresses:  []string{testEmail},
			ExtraExtensions: []pkix.Extension{{Id: oidFulcioIssuerV2, Value: issuer}},
		}, pub)
		_ = json.NewEncoder(w).Encode(map[string]any{"signedCertificateEmbeddedSct": map[string]any{
			"chain": map[string]any{"certificates": []string{certPEM(leaf), certPEM(ca.cert)}},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func popKey(keyPEM, pop string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("no PEM key")
	}
	pk, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := pk.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	sig, err := base64.StdEncoding.DecodeString(pop)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(testEmail))
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return nil, errors.New("bad proof of possession")
	}
	return pub, nil
}

type testTSA struct {
	root testCA
	leaf *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTSA(t *testing.T) testTSA {
	t.Helper()
	ca := newCA(t, "test tsa root")
	key := ecKey(t)
	eku, err := asn1.Marshal([]asn1.ObjectIdentifier{oidTimeStamping})
	if err != nil {
		t.Fatal(err)
	}
	leaf := ca.leaf(t, &x509.Certificate{
		Subject:         pkix.Name{CommonName: "test tsa"},
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{Id: oidExtKeyUsage, Critical: true, Value: eku}},
	}, key.Public())
	return testTSA{root: ca, leaf: leaf, key: key}
}

// server answers RFC 3161 requests with a token signed by the TSA leaf.
func (tsa testTSA) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts := timestamp.Timestamp{
			HashAlgorithm: req.HashAlgorithm, HashedMessage: req.HashedMessage, Nonce: req.Nonce,
			Time: time.Now(), Policy: asn1.ObjectIdentifier{1, 2, 3, 4}, SerialNumber: big.NewInt(1),
			AddTSACertificate: true,
		}
		resp, err := ts.CreateResponseWithOpts(tsa.leaf, tsa.key, crypto.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// trustRoot is a trusted root holding whichever of the test Fulcio CA, TSA
// and Rekor log are non-nil.
func trustRoot(t *testing.T, fulcio *testCA, tsa *testTSA, log *testLog) root.TrustedMaterial {
	t.Helper()
	var (
		cas  []root.CertificateAuthority
		tsas []root.TimestampingAuthority
		logs map[string]*root.TransparencyLog
	)
	if fulcio != nil {
		cas = []root.CertificateAuthority{&root.FulcioCertificateAuthority{Root: fulcio.cert}}
	}
	if tsa != nil {
		tsas = []root.TimestampingAuthority{&root.SigstoreTimestampingAuthority{Root: tsa.root.cert, Leaf: tsa.leaf}}
	}
	if log != nil {
		logs = map[string]*root.TransparencyLog{hex.EncodeToString(log.id): {
			BaseURL: "https://rekor.test", ID: log.id, ValidityPeriodStart: time.Now().Add(-time.Hour),
			HashFunc: crypto.SHA256, PublicKey: log.key.Public(), SignatureHashFunc: crypto.SHA256,
		}}
	}
	tr, err := root.NewTrustedRoot(root.TrustedRootMediaType01, cas, nil, tsas, logs)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestImage_Keyless signs with an ephemeral key certified by a fake Fulcio,
// with and without a signed timestamp, and verifies the bundle against the
// identity the way `cosign verify --certificate-identity
// --certificate-oidc-issuer` does.
func TestImage_Keyless(t *testing.T) {
	t.Parallel()
	for _, withTSA := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-time", true: "signed-timestamp"}[withTSA], func(t *testing.T) {
			t.Parallel()
			host := newRegistry(t, nil)
			ref, digest := seed(t, host, false)
			ca := newCA(t, "test fulcio")
			token := idToken(t)
			opts := sign.Options{FulcioURL: fulcioServer(t, ca, token).URL, Transport: newTransport(t)}
			vopts := []verify.VerifierOption{verify.WithCurrentTime()}
			var tsa *testTSA
			if withTSA {
				tt := newTSA(t)
				tsa = &tt
				opts.TSAURL = tsa.server(t).URL + "/api/v1/timestamp"
				vopts = []verify.VerifierOption{verify.WithSignedTimestamps(1)}
			}
			s := sign.Signer{IDToken: func(context.Context) (string, error) { return token, nil }}
			sig, err := sign.Image(testCtx(t), newClient(t), ref, s, opts)
			if err != nil {
				t.Fatalf("Image: %v", err)
			}
			v, err := verify.NewVerifier(trustRoot(t, &ca, tsa, nil), vopts...)
			if err != nil {
				t.Fatal(err)
			}
			id, err := verify.NewShortCertificateIdentity(testIssuer, "", testEmail, "")
			if err != nil {
				t.Fatal(err)
			}
			b := loadBundle(t, sig.Bundle)
			if _, err = v.Verify(b, verify.NewPolicy(artifactDigest(t, digest), verify.WithCertificateIdentity(id))); err != nil {
				t.Fatalf("verify: %v", err)
			}
			other, _ := verify.NewShortCertificateIdentity(testIssuer, "", "mallory@example.com", "")
			if _, err = v.Verify(b, verify.NewPolicy(artifactDigest(t, digest), verify.WithCertificateIdentity(other))); err == nil {
				t.Fatal("bundle verified for another identity")
			}
		})
	}
}

// TestImage_KeyWithFulcio certifies a caller-held key instead of an
// ephemeral one.
func TestImage_KeyWithFulcio(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, digest := seed(t, host, false)
	ca := newCA(t, "test fulcio")
	token := idToken(t)
	key := ecKey(t)
	s := sign.Signer{Key: key, IDToken: func(context.Context) (string, error) { return token, nil }}
	sig, err := sign.Image(testCtx(t), newClient(t), ref, s, sign.Options{FulcioURL: fulcioServer(t, ca, token).URL})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	b := loadBundle(t, sig.Bundle)
	cert, err := x509.ParseCertificate(b.GetVerificationMaterial().GetCertificate().GetRawBytes())
	if err != nil || !key.PublicKey.Equal(cert.PublicKey) {
		t.Fatalf("certificate does not hold the caller key: %v", err)
	}
	v, err := verify.NewVerifier(trustRoot(t, &ca, nil, nil), verify.WithCurrentTime())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Verify(b, verify.NewPolicy(artifactDigest(t, digest), verify.WithoutIdentitiesUnsafe())); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// testLog is a one-entry Rekor v1 log: it canonicalizes the proposed entry
// as Rekor does and answers with a signed entry timestamp and an inclusion
// proof under a signed checkpoint, all verifiable with its key.
type testLog struct {
	key *ecdsa.PrivateKey
	id  []byte
}

func newLog(t *testing.T) *testLog {
	t.Helper()
	key := ecKey(t)
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return &testLog{key: key, id: sum[:]}
}

func (l *testLog) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/log/entries" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		entry, err := l.logEntry(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		uuid := strings.Repeat("ab", 32)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", uuid)
		w.Header().Set("Location", "/api/v1/log/entries/"+uuid)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{uuid: entry})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// logEntry logs the canonical entry at global index 42 as the only leaf of
// tree 1.
func (l *testLog) logEntry(r *http.Request) (map[string]any, error) {
	pe, err := models.UnmarshalProposedEntry(r.Body, runtime.JSONConsumer())
	if err != nil {
		return nil, err
	}
	impl, err := types.UnmarshalEntry(pe)
	if err != nil {
		return nil, err
	}
	body, err := types.CanonicalizeEntry(r.Context(), impl)
	if err != nil {
		return nil, err
	}
	const logIndex = 42
	logID, now, b64 := hex.EncodeToString(l.id), time.Now().Unix(), base64.StdEncoding.EncodeToString(body)
	set, err := l.signSET(tlog.RekorPayload{Body: b64, IntegratedTime: now, LogIndex: logIndex, LogID: logID})
	if err != nil {
		return nil, err
	}
	leaf := sha256.Sum256(append([]byte{0}, body...))
	sv, err := signature.LoadECDSASignerVerifier(l.key, crypto.SHA256)
	if err != nil {
		return nil, err
	}
	checkpoint, err := util.CreateAndSignCheckpoint(r.Context(), "rekor.test", 1, 1, leaf[:], sv)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"body": b64, "integratedTime": now, "logID": logID, "logIndex": logIndex,
		"verification": map[string]any{
			"signedEntryTimestamp": base64.StdEncoding.EncodeToString(set),
			"inclusionProof": map[string]any{
				"logIndex": 0, "rootHash": hex.EncodeToString(leaf[:]), "treeSize": 1, "hashes": []string{}, "checkpoint": string(checkpoint),
			},
		},
	}, nil
}

// signSET signs the canonical JSON payload Rekor's signed entry timestamp covers.
func (l *testLog) signSET(p tlog.RekorPayload) ([]byte, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	canon, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canon)
	return ecdsa.SignASN1(rand.Reader, l.key, sum[:])
}

// TestImage_RekorEntryRecorded proves the signature goes to the configured
// log as a dsse entry and the returned entry lands in the bundle.
func TestImage_RekorEntryRecorded(t *testing.T) {
	t.Parallel()
	host := newRegistry(t, nil)
	ref, _ := seed(t, host, false)
	sig, err := sign.Image(testCtx(t), newClient(t), ref, sign.Signer{Key: ecKey(t)}, sign.Options{RekorURL: newLog(t).server(t).URL})
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	entries := loadBundle(t, sig.Bundle).GetVerificationMaterial().GetTlogEntries()
	if len(entries) != 1 || entries[0].GetKindVersion().GetKind() != "dsse" || entries[0].GetLogIndex() != 42 {
		t.Fatalf("tlog entries = %v", entries)
	}
}

func TestImage_ServiceErrorsPropagate(t *testing.T) {
	t.Parallel()
	ca := newCA(t, "test fulcio")
	token := idToken(t)
	fulcioURL := fulcioServer(t, ca, token).URL
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadRequest)
	}))
	t.Cleanup(refuse.Close)
	tokenFn := func(tok string, err error) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return tok, err }
	}
	cases := []struct {
		name string
		s    sign.Signer
		o    sign.Options
		is   error
		msg  string
	}{
		{name: "id token error", s: sign.Signer{IDToken: tokenFn("", errBoom)}, o: sign.Options{FulcioURL: fulcioURL}, is: errBoom},
		{name: "empty id token", s: sign.Signer{IDToken: tokenFn("", nil)}, o: sign.Options{FulcioURL: fulcioURL}, is: sign.ErrInvalidOptions},
		{name: "fulcio rejects token", s: sign.Signer{IDToken: tokenFn(idToken(t)+"x", nil)}, o: sign.Options{FulcioURL: fulcioURL}, msg: "Fulcio returned 401"},
		{name: "tsa rejects", s: sign.Signer{Key: ecKey(t)}, o: sign.Options{TSAURL: refuse.URL}, msg: "timestamp authority returned 400"},
		{name: "rekor rejects", s: sign.Signer{Key: ecKey(t)}, o: sign.Options{RekorURL: refuse.URL}, msg: "sign: sign bundle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host := newRegistry(t, nil)
			ref, _ := seed(t, host, false)
			c := newClient(t)
			_, err := sign.Image(testCtx(t), c, ref, tc.s, tc.o)
			if err == nil || (tc.is != nil && !errors.Is(err, tc.is)) || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want %v / %q", err, tc.is, tc.msg)
			}
			if refs, rerr := c.Referrers(testCtx(t), ref, ""); rerr != nil || len(refs) != 0 {
				t.Fatalf("referrers after failed sign = %v, %v", refs, rerr)
			}
		})
	}
}
