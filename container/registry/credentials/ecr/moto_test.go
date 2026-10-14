// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ecr

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/internal/testimages"
)

const motoDefaultAccount = "123456789012"

func startMoto(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testimages.Moto,
			ReaperImage:  testimages.Ryuk,
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor:   wait.ForHTTP("/moto-api/").WithPort("5000/tcp").WithStartupTimeout(1 * time.Minute),
		},
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("ecr: start moto container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if termErr := container.Terminate(stopCtx); termErr != nil {
			t.Logf("ecr: terminate moto container: %v", termErr)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("ecr: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5000/tcp")
	if err != nil {
		t.Fatalf("ecr: mapped port: %v", err)
	}
	endpoint := "http://" + net.JoinHostPort(host, port.Port())
	verifyMoto(t, ctx, endpoint)
	return endpoint
}

func verifyMoto(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/moto-api/", nil)
	if err != nil {
		t.Fatalf("ecr: verify moto request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("ecr: verify moto get: %v", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("ecr: close verify body: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ecr: moto healthcheck status = %d, want 200", resp.StatusCode)
	}
}

func TestMoto_Credential(t *testing.T) {
	t.Parallel()
	endpoint := startMoto(t)
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
		}),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
	p := New(cfg, func(o *ecr.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	testMotoCredentialPositive(t, p)
	testMotoCredentialNegative(t, p, cfg, endpoint)
}

func testMotoCredentialPositive(t *testing.T, p *Provider) {
	t.Helper()
	got, err := p.Credential(t.Context(), testHost)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Username != "AWS" {
		t.Errorf("Username = %q, want AWS", got.Username)
	}
	wantPassword := motoDefaultAccount + "-auth-token"
	if got.Password != wantPassword {
		t.Errorf("Password = %q, want %s", got.Password, wantPassword)
	}
	wantExpiry := time.Unix(1420070400, 0)
	if got.ExpiresAt.IsZero() || !got.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, wantExpiry)
	}

	// Credential discards the proxyEndpoint that carries the region, so only success for a second region is observable.
	secondHost := motoDefaultAccount + ".dkr.ecr.us-west-2.amazonaws.com"
	if _, err = p.Credential(t.Context(), secondHost); err != nil {
		t.Fatalf("second region Credential: %v", err)
	}
}

func testMotoCredentialNegative(t *testing.T, p *Provider, cfg aws.Config, endpoint string) {
	t.Helper()
	if _, err := p.Credential(t.Context(), "ghcr.io"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("non-ECR host err = %v, want ErrNoCredential", err)
	}

	pNotFound := New(cfg, func(o *ecr.Options) {
		o.BaseEndpoint = aws.String(endpoint + "/nope")
		o.RetryMaxAttempts = 1
	})
	_, err := pNotFound.Credential(t.Context(), testHost)
	if err == nil || !strings.Contains(err.Error(), "ecr: get authorization token for") {
		t.Fatalf("404 path err = %v, want to contain 'ecr: get authorization token for'", err)
	}
}

func motoIRSAEnv(t *testing.T, endpoint string) {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(token, []byte("projected-sa-token"), 0o600); err != nil {
		t.Fatalf("ecr: write token file: %v", err)
	}
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("ecr: write empty config: %v", err)
	}
	setEnv(t, map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123456789012:role/probe",
		"AWS_WEB_IDENTITY_TOKEN_FILE": token,
		"AWS_REGION":                  "us-east-1",
		"AWS_ENDPOINT_URL":            endpoint,
		"AWS_ENDPOINT_URL_STS":        endpoint,
		"AWS_ENDPOINT_URL_ECR":        endpoint,
		"AWS_CONFIG_FILE":             empty,
		"AWS_SHARED_CREDENTIALS_FILE": empty,
		"AWS_EC2_METADATA_DISABLED":   "true",
		"AWS_PROFILE":                 "",
		"AWS_ACCESS_KEY_ID":           "",
		"AWS_SECRET_ACCESS_KEY":       "",
		"AWS_SESSION_TOKEN":           "",
	})
}

//nolint:paralleltest // t.Setenv configures shared environment for the AWS default chain.
func TestMoto_NewDefaultIRSA(t *testing.T) {
	endpoint := startMoto(t)
	motoIRSAEnv(t, endpoint)

	client := &http.Client{Timeout: 5 * time.Second}
	p, err := NewDefault(t.Context(), config.WithHTTPClient(client))
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	got, err := p.Credential(t.Context(), testHost)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Username != "AWS" {
		t.Errorf("Username = %q, want AWS", got.Username)
	}
	wantPassword := motoDefaultAccount + "-auth-token"
	if got.Password != wantPassword {
		t.Errorf("Password = %q, want %s", got.Password, wantPassword)
	}
}
