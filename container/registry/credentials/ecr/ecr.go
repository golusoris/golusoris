// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ecr resolves Amazon ECR registry credentials from the AWS default
// credential chain — IRSA web identity, EKS Pod Identity, instance roles,
// environment — through ecr:GetAuthorizationToken. No static keys are
// configured here; the region comes from the registry host name.
package ecr

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/container/registry/credentials"
)

// loadTimeout bounds reading the shared AWS config at construction.
const loadTimeout = 15 * time.Second

// ErrToken reports an unusable GetAuthorizationToken response.
var ErrToken = errors.New("ecr: invalid authorization token")

// hostPattern matches private ECR endpoints, including FIPS, China and
// dual-stack hosts; group 3 is the region.
var hostPattern = regexp.MustCompile(`^(\d{12})\.dkr[.-]ecr(-fips)?\.([a-z0-9][a-z0-9-]*)\.(amazonaws\.com(\.cn)?|on\.aws|on\.amazonwebservices\.com\.cn)$`)

// Provider implements [credentials.Provider] for ECR hosts.
type Provider struct {
	cfg    aws.Config
	optFns []func(*ecr.Options)
}

// New builds a provider from an AWS config; optFns customise the ECR
// client (endpoint overrides, retries).
func New(cfg aws.Config, optFns ...func(*ecr.Options)) *Provider {
	return &Provider{cfg: cfg, optFns: optFns}
}

// Region returns the region of an ECR host, or false for other hosts.
func Region(host string) (string, bool) {
	m := hostPattern.FindStringSubmatch(host)
	if m == nil {
		return "", false
	}
	return m[3], true
}

// Credential implements [credentials.Provider].
func (p *Provider) Credential(ctx context.Context, host string) (credentials.Credential, error) {
	region, ok := Region(host)
	if !ok {
		return credentials.Credential{}, credentials.ErrNoCredential
	}
	opts := make([]func(*ecr.Options), 0, len(p.optFns)+1)
	opts = append(opts, func(o *ecr.Options) { o.Region = region })
	opts = append(opts, p.optFns...)
	out, err := ecr.NewFromConfig(p.cfg, opts...).GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("ecr: get authorization token for %s: %w", host, err)
	}
	if len(out.AuthorizationData) == 0 {
		return credentials.Credential{}, fmt.Errorf("%w: empty authorization data", ErrToken)
	}
	data := out.AuthorizationData[0]
	user, pass, err := decodeToken(aws.ToString(data.AuthorizationToken))
	if err != nil {
		return credentials.Credential{}, err
	}
	return credentials.Credential{Username: user, Password: pass, ExpiresAt: aws.ToTime(data.ExpiresAt)}, nil
}

// decodeToken splits base64("user:password").
func decodeToken(token string) (string, string, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrToken, err)
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok || user == "" || pass == "" {
		return "", "", fmt.Errorf("%w: not user:password", ErrToken)
	}
	return user, pass, nil
}

// NewDefault loads the AWS default config (environment, shared files, IRSA
// web identity, Pod Identity, IMDS) and builds a provider. optFns tune
// loading, e.g. [config.WithHTTPClient].
func NewDefault(ctx context.Context, optFns ...func(*config.LoadOptions) error) (*Provider, error) {
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	cfg, err := config.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("ecr: load AWS config: %w", err)
	}
	return New(cfg), nil
}

func newProvider() (*Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	return NewDefault(ctx)
}

// Module adds the ECR provider to the container/registry/credentials chain. Credentials
// resolve through the AWS default chain (AWS_ROLE_ARN +
// AWS_WEB_IDENTITY_TOKEN_FILE for IRSA, the Pod Identity agent, IMDS).
//
//	fx.New(config.Module, clock.Module, credentials.Module, ecr.Module, artifact.Module)
var Module = fx.Module(
	"golusoris.container.registry.credentials.ecr",
	credentials.ProvideFn(newProvider),
)
