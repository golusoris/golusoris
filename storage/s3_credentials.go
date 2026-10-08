// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

func validateS3Credentials(opts S3Options) error {
	if (opts.AccessKey == "") != (opts.SecretKey == "") {
		return errors.New("storage/s3: access_key and secret_key must be set together")
	}
	if opts.WebIdentityTokenFile != "" && opts.RoleARN == "" {
		return errors.New("storage/s3: web_identity_token_file requires role_arn")
	}
	if opts.WebIdentityTokenFile != "" && opts.AccessKey != "" {
		return errors.New("storage/s3: web_identity_token_file and static keys are mutually exclusive")
	}
	return nil
}

// loadS3Config resolves base credentials (static keys or the default chain),
// then layers an explicit role assumption on top when RoleARN is set.
func loadS3Config(ctx context.Context, opts S3Options) (aws.Config, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(opts.Region)}
	if opts.AccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, ""),
		))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("storage/s3: load aws config: %w", err)
	}
	if opts.RoleARN != "" {
		cfg.Credentials = aws.NewCredentialsCache(s3RoleProvider(cfg, opts))
	}
	return cfg, nil
}

// s3RoleProvider re-reads WebIdentityTokenFile on every refresh, so rotated
// projected service-account tokens are picked up without a restart.
func s3RoleProvider(cfg aws.Config, opts S3Options) aws.CredentialsProvider {
	client := sts.NewFromConfig(cfg, func(o *sts.Options) {
		if opts.STSEndpoint != "" {
			o.BaseEndpoint = aws.String(opts.STSEndpoint)
		}
	})
	if opts.WebIdentityTokenFile != "" {
		return stscreds.NewWebIdentityRoleProvider(
			client, opts.RoleARN, stscreds.IdentityTokenFile(opts.WebIdentityTokenFile),
			func(o *stscreds.WebIdentityRoleOptions) { o.RoleSessionName = opts.RoleSessionName },
		)
	}
	return stscreds.NewAssumeRoleProvider(client, opts.RoleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = opts.RoleSessionName
	})
}
