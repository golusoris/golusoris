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
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/compute/metadata"
	gstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
)

const (
	emulatorAccessID = "golusoris-emulator@localhost"
	ephemeralKeyBits = 2048
)

// blobSigner signs a V4 string-to-sign as a service account.
type blobSigner interface {
	SignBlob(ctx context.Context, email string, payload []byte) ([]byte, error)
}

// urlSigner signs URLs as one service account: locally with key when the
// credential carries one, otherwise through blob (IAM signBlob).
type urlSigner struct {
	key    []byte
	blob   blobSigner
	lookup func(context.Context) (string, error)

	mu    sync.Mutex
	email string
}

// apply fills the signing identity into opts; SignBytes captures ctx so the
// remote signBlob call honours the caller's deadline.
func (s *urlSigner) apply(ctx context.Context, opts *gstorage.SignedURLOptions) error {
	email, err := s.accessID(ctx)
	if err != nil {
		return err
	}
	opts.GoogleAccessID = email
	if len(s.key) > 0 {
		opts.PrivateKey = s.key
		return nil
	}
	opts.SignBytes = func(payload []byte) ([]byte, error) { return s.blob.SignBlob(ctx, email, payload) }
	return nil
}

func (s *urlSigner) accessID(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.email != "" {
		return s.email, nil
	}
	if s.lookup == nil {
		return "", errors.New("storage/gcs: no signer email; set signer_email")
	}
	email, err := s.lookup(ctx)
	if err != nil {
		return "", fmt.Errorf("storage/gcs: resolve signer email (set signer_email to skip): %w", err)
	}
	if email == "" {
		return "", errors.New("storage/gcs: metadata server returned no service account email")
	}
	s.email = email
	return email, nil
}

// credentialFile is the subset of an ADC JSON credential that names a signer.
type credentialFile struct {
	Type             string `json:"type"`
	ClientEmail      string `json:"client_email"`
	PrivateKey       string `json:"private_key"`
	ImpersonationURL string `json:"service_account_impersonation_url"`
}

// newCredentialSigner prefers the credential's own key; keyless credentials
// (Workload Identity, federation, user ADC) sign through IAM signBlob.
func newCredentialSigner(ctx context.Context, creds *auth.Credentials, email string) (*urlSigner, error) {
	var file credentialFile
	if raw := creds.JSON(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &file); err != nil {
			return nil, fmt.Errorf("storage/gcs: parse credentials: %w", err)
		}
	}
	if file.Type == "service_account" && file.PrivateKey != "" && (email == "" || email == file.ClientEmail) {
		return &urlSigner{key: []byte(file.PrivateKey), email: file.ClientEmail}, nil
	}
	svc, err := iamcredentials.NewService(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: new iam credentials client: %w", err)
	}
	if email == "" {
		email = impersonatedEmail(file.ImpersonationURL)
	}
	signer := &urlSigner{blob: iamSigner{svc: svc}, email: email}
	if email == "" {
		signer.lookup = metadataEmail
	}
	return signer, nil
}

func metadataEmail(ctx context.Context) (string, error) {
	email, err := metadata.EmailWithContext(ctx, "default")
	if err != nil {
		return "", fmt.Errorf("metadata server: %w", err)
	}
	return email, nil
}

// impersonatedEmail extracts SA from ".../serviceAccounts/SA:generateAccessToken".
func impersonatedEmail(impersonationURL string) string {
	_, rest, ok := strings.Cut(impersonationURL, "/serviceAccounts/")
	if !ok {
		return ""
	}
	email, _, _ := strings.Cut(rest, ":")
	return email
}

// newEphemeralSigner signs emulator URLs; emulators never verify signatures.
func newEphemeralSigner() (*urlSigner, error) {
	key, err := rsa.GenerateKey(rand.Reader, ephemeralKeyBits)
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: generate emulator signing key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: encode emulator signing key: %w", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return &urlSigner{key: pemKey, email: emulatorAccessID}, nil
}

type iamSigner struct{ svc *iamcredentials.Service }

func (s iamSigner) SignBlob(ctx context.Context, email string, payload []byte) ([]byte, error) {
	resp, err := s.svc.Projects.ServiceAccounts.SignBlob(
		"projects/-/serviceAccounts/"+email,
		&iamcredentials.SignBlobRequest{Payload: base64.StdEncoding.EncodeToString(payload)},
	).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: sign blob as %s: %w", email, err)
	}
	sig, err := base64.StdEncoding.DecodeString(resp.SignedBlob)
	if err != nil {
		return nil, fmt.Errorf("storage/gcs: decode signed blob: %w", err)
	}
	return sig, nil
}
