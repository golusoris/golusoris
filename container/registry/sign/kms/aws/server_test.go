// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"

	"github.com/golusoris/golusoris/container/registry/sign/kms/aws"
	"github.com/golusoris/golusoris/container/registry/sign/kms/aws/internal/localstackdev"
)

var serverKeySpecs = []types.KeySpec{
	types.KeySpecEccNistP256, types.KeySpecEccNistP384, types.KeySpecEccNistP521, types.KeySpecRsa2048,
}

// localCreds is the account LocalStack accepts for any request.
func localCreds() *awssdk.Config {
	return &awssdk.Config{
		Region: localstackdev.Region,
		Credentials: awssdk.CredentialsProviderFunc(func(context.Context) (awssdk.Credentials, error) {
			return awssdk.Credentials{AccessKeyID: "test", SecretAccessKey: "test", Source: "test"}, nil
		}),
	}
}

func admin(srv localstackdev.Server) *kms.Client {
	return kms.NewFromConfig(*localCreds(), func(o *kms.Options) { o.BaseEndpoint = awssdk.String(srv.Endpoint) })
}

func createKey(t *testing.T, c *kms.Client, spec types.KeySpec, usage types.KeyUsageType) string {
	t.Helper()
	out, err := c.CreateKey(t.Context(), &kms.CreateKeyInput{KeySpec: spec, KeyUsage: usage})
	if err != nil {
		t.Fatalf("create %s key: %v", spec, err)
	}
	return awssdk.ToString(out.KeyMetadata.Arn)
}

func serverConfig(srv localstackdev.Server, key string) aws.Config {
	return aws.Config{Key: key, Endpoint: srv.Endpoint, AWS: localCreds()}
}

// TestServer signs images with keys of a real KMS API (LocalStack): every
// key spec, alias pinning, PSS and rejected keys.
func TestServer(t *testing.T) {
	t.Parallel()
	srv := localstackdev.Start(t)
	c := admin(srv)
	for _, spec := range serverKeySpecs {
		t.Run(string(spec), func(t *testing.T) {
			t.Parallel()
			imageRoundTrip(t, newSigner(t, serverConfig(srv, createKey(t, c, spec, types.KeyUsageTypeSignVerify))))
		})
	}
	t.Run("pss", func(t *testing.T) {
		t.Parallel()
		testServerPSS(t, srv, c)
	})
	t.Run("alias pinning", func(t *testing.T) {
		t.Parallel()
		testServerAlias(t, srv, c)
	})
	t.Run("rejected keys", func(t *testing.T) {
		t.Parallel()
		testServerRejects(t, srv, c)
	})
}

// TestServer_IRSA loads credentials the way IRSA injects them, a role ARN
// and a projected token file, exchanged at LocalStack's STS. Not parallel:
// the default chain reads process environment.
func TestServer_IRSA(t *testing.T) {
	srv := localstackdev.Start(t)
	key := createKey(t, admin(srv), types.KeySpecEccNistP256, types.KeyUsageTypeSignVerify)
	isolateChain(t)
	t.Setenv("AWS_REGION", srv.Region)
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::000000000000:role/signer")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", writeFile(t, saToken))
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.Endpoint)
	imageRoundTrip(t, newSigner(t, aws.Config{Key: key, Endpoint: srv.Endpoint}))
}

func testServerPSS(t *testing.T, srv localstackdev.Server, c *kms.Client) {
	t.Helper()
	s := newSigner(t, serverConfig(srv, createKey(t, c, types.KeySpecRsa2048, types.KeyUsageTypeSignVerify)))
	for _, opts := range []*rsa.PSSOptions{
		{Hash: crypto.SHA256, SaltLength: rsa.PSSSaltLengthEqualsHash},
		{Hash: crypto.SHA512, SaltLength: rsa.PSSSaltLengthAuto},
	} {
		d := digestOf(opts.Hash, "payload")
		sig, err := s.Sign(rand.Reader, d, opts)
		if err != nil {
			t.Fatalf("PSS %v: %v", opts.Hash, err)
		}
		if !verifies(s.Public(), d, sig, opts) {
			t.Fatalf("PSS %v: signature does not verify", opts.Hash)
		}
	}
}

func testServerAlias(t *testing.T, srv localstackdev.Server, c *kms.Client) {
	t.Helper()
	a := createKey(t, c, types.KeySpecEccNistP256, types.KeyUsageTypeSignVerify)
	b := createKey(t, c, types.KeySpecEccNistP256, types.KeyUsageTypeSignVerify)
	// LocalStack resolves aliases whose target is a key ID, not a key ARN.
	a, b = a[strings.LastIndex(a, "/")+1:], b[strings.LastIndex(b, "/")+1:]
	if _, err := c.CreateAlias(t.Context(), &kms.CreateAliasInput{AliasName: awssdk.String("alias/signing"), TargetKeyId: &a}); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	pinned := newSigner(t, serverConfig(srv, "alias/signing"))
	if _, err := c.UpdateAlias(t.Context(), &kms.UpdateAliasInput{AliasName: awssdk.String("alias/signing"), TargetKeyId: &b}); err != nil {
		t.Fatalf("update alias: %v", err)
	}
	imageRoundTrip(t, pinned)
	if moved := newSigner(t, serverConfig(srv, "alias/signing")); moved.Public().(*ecdsa.PublicKey).Equal(pinned.Public()) {
		t.Fatal("moved alias: a new signer has the pinned public key")
	}
}

func testServerRejects(t *testing.T, srv localstackdev.Server, c *kms.Client) {
	t.Helper()
	enc := createKey(t, c, types.KeySpecRsa2048, types.KeyUsageTypeEncryptDecrypt)
	if _, err := aws.New(t.Context(), serverConfig(srv, enc)); !errors.Is(err, aws.ErrUnsupportedKey) {
		t.Fatalf("encryption key: New error = %v, want ErrUnsupportedKey", err)
	}
	k1 := createKey(t, c, types.KeySpecEccSecgP256k1, types.KeyUsageTypeSignVerify)
	if _, err := aws.New(t.Context(), serverConfig(srv, k1)); !errors.Is(err, aws.ErrUnsupportedKey) {
		t.Fatalf("secp256k1 key: New error = %v, want ErrUnsupportedKey", err)
	}
	if _, err := aws.New(t.Context(), serverConfig(srv, "alias/missing")); err == nil || !strings.Contains(err.Error(), "NotFoundException") {
		t.Fatalf("missing alias: New error = %v", err)
	}
	key := createKey(t, c, types.KeySpecEccNistP256, types.KeyUsageTypeSignVerify)
	s := newSigner(t, serverConfig(srv, key))
	if _, err := c.DisableKey(t.Context(), &kms.DisableKeyInput{KeyId: &key}); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	if _, err := s.Sign(rand.Reader, digestOf(crypto.SHA256, "m"), crypto.SHA256); err == nil || !strings.Contains(err.Error(), "Disabled") {
		t.Fatalf("disabled key: Sign error = %v", err)
	}
}
