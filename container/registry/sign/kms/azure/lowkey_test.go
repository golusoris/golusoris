// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/container/registry/sign/kms/azure"
	"github.com/golusoris/golusoris/container/registry/sign/kms/azure/internal/lowkeydev"
)

// #nosec G101 -- mock bearer token for the local emulator.
const lowkeyToken = "lowkey-test-token"

type lowkeyCredential struct{}

func (lowkeyCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: lowkeyToken, ExpiresOn: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

var (
	signOps   = []*azkeys.KeyOperation{new(azkeys.KeyOperationSign), new(azkeys.KeyOperationVerify)}
	noSignOps = []*azkeys.KeyOperation{new(azkeys.KeyOperationVerify)}
)

func ecParams(crv azkeys.CurveName, ops []*azkeys.KeyOperation) azkeys.CreateKeyParameters {
	return azkeys.CreateKeyParameters{Kty: new(azkeys.KeyTypeEC), Curve: new(crv), KeyOps: ops}
}

func rsaParams(size int32, ops []*azkeys.KeyOperation) azkeys.CreateKeyParameters {
	return azkeys.CreateKeyParameters{Kty: new(azkeys.KeyTypeRSA), KeySize: new(size), KeyOps: ops}
}

func fetchCert(addr string) (*x509.Certificate, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		InsecureSkipVerify: true, // probe dial fetches self-signed emulator leaf.
	})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("lowkey: no peer certificates")
	}
	return certs[0], nil
}

func buildTransport(t *testing.T, containerAddr string) *http.Transport {
	t.Helper()
	leaf, err := fetchCert(containerAddr)
	if err != nil {
		t.Fatalf("fetch lowkey certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: "primary.localhost",
			MinVersion: tls.VersionTLS12,
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, containerAddr)
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return tr
}

func startLowkey(t *testing.T) (string, *http.Transport) {
	t.Helper()
	if testing.Short() {
		t.Skip("lowkey: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Image:        lowkeydev.LowkeyVaultImage,
		ReaperImage:  lowkeydev.RyukImage,
		ExposedPorts: []string{"8443/tcp"},
		Env: map[string]string{
			"LOWKEY_ARGS": "--LOWKEY_AUTH_RESOURCE=localhost --LOWKEY_VAULT_RELAXED_PORTS=true",
		},
		WaitingFor: wait.ForLog("Started LowkeyVaultApp").WithStartupTimeout(3 * time.Minute),
		Started:    true,
	})
	if err != nil {
		t.Fatalf("start lowkey container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("terminate container: %v", termErr)
		}
	})
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "8443/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	tr := buildTransport(t, net.JoinHostPort(host, port.Port()))
	return "https://primary.localhost", tr
}

func createKey(t *testing.T, client *azkeys.Client, name string, params azkeys.CreateKeyParameters) string {
	t.Helper()
	resp, err := client.CreateKey(t.Context(), name, params, nil)
	if err != nil {
		t.Fatalf("create key %s: %v", name, err)
	}
	if resp.Key == nil || resp.Key.KID == nil {
		t.Fatalf("create key %s: nil key or KID", name)
	}
	return resp.Key.KID.Version()
}

func parseECCurve(name azkeys.CurveName) (elliptic.Curve, error) {
	switch name {
	case azkeys.CurveNameP256:
		return elliptic.P256(), nil
	case azkeys.CurveNameP384:
		return elliptic.P384(), nil
	case azkeys.CurveNameP521:
		return elliptic.P521(), nil
	case azkeys.CurveNameP256K:
		return nil, fmt.Errorf("unsupported curve: %s", name)
	default:
		return nil, fmt.Errorf("unknown curve: %s", name)
	}
}

func parseWebKeyEC(k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	if k.Crv == nil {
		return nil, errors.New("nil curve")
	}
	crv, err := parseECCurve(*k.Crv)
	if err != nil {
		return nil, err
	}
	size := (crv.Params().BitSize + 7) / 8
	if len(k.X) == 0 || len(k.Y) == 0 {
		return nil, errors.New("empty ec coordinates")
	}
	if len(k.X) > size || len(k.Y) > size {
		return nil, fmt.Errorf("curve %s: coordinate longer than curve size %d: x=%d y=%d", *k.Crv, size, len(k.X), len(k.Y))
	}
	// Left-pad coordinates to the curve byte size required by uncompressed points.
	pt := make([]byte, 1+2*size)
	pt[0] = 4
	copy(pt[1+size-len(k.X):1+size], k.X)
	copy(pt[1+2*size-len(k.Y):], k.Y)
	pub, err := ecdsa.ParseUncompressedPublicKey(crv, pt)
	if err != nil {
		return nil, fmt.Errorf("parse ec pub: %w", err)
	}
	return pub, nil
}

func parseWebKeyPublic(k *azkeys.JSONWebKey) (crypto.PublicKey, error) {
	if k == nil || k.Kty == nil {
		return nil, errors.New("nil web key or kty")
	}
	switch *k.Kty {
	case azkeys.KeyTypeEC, azkeys.KeyTypeECHSM:
		return parseWebKeyEC(k)
	case azkeys.KeyTypeRSA, azkeys.KeyTypeRSAHSM:
		return &rsa.PublicKey{N: new(big.Int).SetBytes(k.N), E: int(new(big.Int).SetBytes(k.E).Int64())}, nil
	case azkeys.KeyTypeOct, azkeys.KeyTypeOctHSM:
		return nil, fmt.Errorf("unsupported symmetric key type: %s", *k.Kty)
	default:
		return nil, fmt.Errorf("unknown kty: %s", *k.Kty)
	}
}

func webKeyPublic(t *testing.T, k *azkeys.JSONWebKey) crypto.PublicKey {
	t.Helper()
	pub, err := parseWebKeyPublic(k)
	if err != nil {
		t.Fatalf("webKeyPublic: %v", err)
	}
	return pub
}

func TestWebKeyPublic_ShortXPadding(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"P-256", "P-384"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			priv := keyWithShortX(t, kind)
			point, err := priv.PublicKey.Bytes()
			if err != nil {
				t.Fatalf("public key bytes: %v", err)
			}
			size := (len(point) - 1) / 2
			jwk := &azkeys.JSONWebKey{
				Kty: new(azkeys.KeyTypeEC),
				Crv: new(azkeys.CurveName(kind)),
				X:   point[2 : 1+size],
				Y:   point[1+size:],
			}
			assertPublicEqual(t, &priv.PublicKey, webKeyPublic(t, jwk))

			oversized := &azkeys.JSONWebKey{
				Kty: new(azkeys.KeyTypeEC),
				Crv: new(azkeys.CurveName(kind)),
				X:   make([]byte, size+1),
				Y:   point[1+size:],
			}
			if _, err := parseWebKeyPublic(oversized); err == nil {
				t.Fatal("parseWebKeyPublic succeeded for oversized X coordinate, want error")
			}
		})
	}
}

func assertPublicEqual(t *testing.T, expected, got crypto.PublicKey) {
	t.Helper()
	switch exp := expected.(type) {
	case *ecdsa.PublicKey:
		g, ok := got.(*ecdsa.PublicKey)
		if !ok || !exp.Equal(g) {
			t.Fatalf("public key mismatch: got %v, want %v", got, expected)
		}
	case *rsa.PublicKey:
		g, ok := got.(*rsa.PublicKey)
		if !ok || !exp.Equal(g) {
			t.Fatalf("public key mismatch: got %v, want %v", got, expected)
		}
	default:
		t.Fatalf("unsupported key type %T", expected)
	}
}

//nolint:paralleltest // subtests are sequential to test stateful container keys and version pinning.
func TestLowkey_Sign(t *testing.T) {
	vault, tr := startLowkey(t)
	azClient, err := azkeys.NewClient(vault, lowkeyCredential{}, &azkeys.ClientOptions{
		Transport: &http.Client{Transport: tr, Timeout: 10 * time.Second},
	})
	if err != nil {
		t.Fatalf("azkeys client: %v", err)
	}
	t.Run("P-256", func(t *testing.T) {
		testSignEC(t, vault, tr, azClient, "ec-p256", azkeys.CurveNameP256, crypto.SHA256)
	})
	t.Run("P-384", func(t *testing.T) {
		testSignEC(t, vault, tr, azClient, "ec-p384", azkeys.CurveNameP384, crypto.SHA384)
	})
	t.Run("RSA-2048", func(t *testing.T) {
		testSignRSA(t, vault, tr, azClient, "rsa-2048")
	})
	t.Run("negative", func(t *testing.T) {
		testNegative(t, vault, tr, azClient)
	})
	t.Run("boundary", func(t *testing.T) {
		testBoundary(t, vault, tr, azClient)
	})
}

func testSignEC(t *testing.T, vault string, tr *http.Transport, azClient *azkeys.Client, name string, crv azkeys.CurveName, h crypto.Hash) {
	t.Helper()
	v1 := createKey(t, azClient, name, ecParams(crv, signOps))
	cfg := azure.Config{Vault: vault, Key: name, Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	s, err := azure.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := azClient.GetKey(t.Context(), name, v1, nil)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	assertPublicEqual(t, webKeyPublic(t, resp.Key), s.Public())
	d := digestOf(h, "ec payload")
	sig, err := s.Sign(rand.Reader, d, h)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ecdsa.VerifyASN1(s.Public().(*ecdsa.PublicKey), d, sig) {
		t.Fatal("ECDSA signature does not verify")
	}
	v2 := createKey(t, azClient, name, ecParams(crv, signOps))
	if v2 == v1 {
		t.Fatal("expected distinct second version")
	}
	sig2, err := s.Sign(rand.Reader, d, h)
	if err != nil {
		t.Fatalf("Sign after rotation: %v", err)
	}
	if !ecdsa.VerifyASN1(s.Public().(*ecdsa.PublicKey), d, sig2) {
		t.Fatal("pinned version signature does not verify against v1 public key")
	}
}

func testSignRSA(t *testing.T, vault string, tr *http.Transport, azClient *azkeys.Client, name string) {
	t.Helper()
	v1 := createKey(t, azClient, name, rsaParams(2048, signOps))
	cfg := azure.Config{Vault: vault, Key: name, Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	s, err := azure.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := azClient.GetKey(t.Context(), name, v1, nil)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	assertPublicEqual(t, webKeyPublic(t, resp.Key), s.Public())
	d := digestOf(crypto.SHA256, "rsa payload")
	sig, err := s.Sign(rand.Reader, d, crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign PKCS1v15: %v", err)
	}
	if vErr := rsa.VerifyPKCS1v15(s.Public().(*rsa.PublicKey), crypto.SHA256, d, sig); vErr != nil {
		t.Fatalf("VerifyPKCS1v15: %v", vErr)
	}
	pssOpts := &rsa.PSSOptions{Hash: crypto.SHA256, SaltLength: rsa.PSSSaltLengthEqualsHash}
	sigPSS, err := s.Sign(rand.Reader, d, pssOpts)
	if err != nil {
		t.Fatalf("Sign PSS: %v", err)
	}
	if pssErr := rsa.VerifyPSS(s.Public().(*rsa.PublicKey), crypto.SHA256, d, sigPSS, pssOpts); pssErr != nil {
		t.Fatalf("VerifyPSS: %v", pssErr)
	}
	v2 := createKey(t, azClient, name, rsaParams(2048, signOps))
	if v2 == v1 {
		t.Fatal("expected distinct second version")
	}
	sig2, err := s.Sign(rand.Reader, d, crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign PKCS1v15 after rotation: %v", err)
	}
	if pinnedErr := rsa.VerifyPKCS1v15(s.Public().(*rsa.PublicKey), crypto.SHA256, d, sig2); pinnedErr != nil {
		t.Fatalf("VerifyPKCS1v15 pinned: %v", pinnedErr)
	}
}

func testNegative(t *testing.T, vault string, tr *http.Transport, azClient *azkeys.Client) {
	t.Helper()
	noSign := "no-sign-key"
	_ = createKey(t, azClient, noSign, ecParams(azkeys.CurveNameP256, noSignOps))
	cfg := azure.Config{Vault: vault, Key: noSign, Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	if _, err := azure.New(t.Context(), cfg); !errors.Is(err, azure.ErrUnsupportedKey) {
		t.Fatalf("no-sign key: got %v, want ErrUnsupportedKey", err)
	}
	missingCfg := azure.Config{Vault: vault, Key: "missing-key-name", Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	if _, err := azure.New(t.Context(), missingCfg); err == nil || !strings.Contains(err.Error(), "kms/azure: get key") {
		t.Fatalf("missing key: got %v, want error containing 'kms/azure: get key'", err)
	}
	s, err := azure.New(t.Context(), azure.Config{Vault: vault, Key: "ec-p256", Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("New ec-p256: %v", err)
	}
	if _, err := s.Sign(rand.Reader, []byte("short"), crypto.SHA256); err == nil {
		t.Fatal("short digest: expected error, got nil")
	}
}

func testBoundary(t *testing.T, vault string, tr *http.Transport, azClient *azkeys.Client) {
	t.Helper()
	v1 := createKey(t, azClient, "boundary-key", ecParams(azkeys.CurveNameP256, signOps))
	cfg := azure.Config{Vault: vault, Key: "boundary-key", Version: v1, Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	s, err := azure.New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New with explicit version: %v", err)
	}
	d := digestOf(crypto.SHA256, "boundary payload")
	sig, err := s.Sign(rand.Reader, d, crypto.SHA256)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ecdsa.VerifyASN1(s.Public().(*ecdsa.PublicKey), d, sig) {
		t.Fatal("signature does not verify against v1")
	}
	badCfg := azure.Config{Vault: vault, Key: "boundary-key", Version: strings.Repeat("0", 32), Credential: lowkeyCredential{}, Transport: tr, Timeout: 10 * time.Second}
	if _, err := azure.New(t.Context(), badCfg); err == nil {
		t.Fatal("nonexistent version: expected error, got nil")
	}
}
