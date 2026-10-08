// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command golusoris-multiregion is a reference Pulumi (Go) program for an
// active/passive two-region golusoris deployment: Aurora Global Database
// (writer in the primary region, read replica in the secondary), a per-region
// app+ALB stack reused across both regions, and Route53 DNS failover.
//
// WARNING: the Aurora Global Database is expensive and slow to provision
// (~20-40 min, cross-region replication cost). See README.md.
//
// Run:
//
//	pulumi stack init prod
//	pulumi config set --secret golusoris-multiregion:dbPassword <strong-password>
//	pulumi up
package main

import (
	"errors"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/golusoris/golusoris/deploy/internal/imageref"
)

// stackConfig is the resolved, typed view of the multiregion stack config.
type stackConfig struct {
	PrimaryRegion           string
	SecondaryRegion         string
	Domain                  string
	HostedZoneID            string
	PrimaryCertificateARN   string
	SecondaryCertificateARN string
	DBInstanceClass         string
	DBEngineVersion         string
	AppImage                string
	AppPort                 int
	DBPassword              pulumi.StringInput
}

func main() {
	pulumi.Run(run)
}

// run wires per-region providers → global Aurora → per-region app stacks → DNS failover.
func run(ctx *pulumi.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	primaryProvider, secondaryProvider, err := newProviders(ctx, cfg)
	if err != nil {
		return err
	}

	primaryNet, secondaryNet, err := newRegionNetworks(ctx, primaryProvider, secondaryProvider, cfg)
	if err != nil {
		return err
	}

	database, err := newGlobalDB(ctx, primaryProvider, secondaryProvider, primaryNet, secondaryNet, dbConfig{
		InstanceClass: cfg.DBInstanceClass,
		EngineVersion: cfg.DBEngineVersion,
		Password:      cfg.DBPassword,
	})
	if err != nil {
		return err
	}

	primary, err := newRegionStack(ctx, "golusoris-primary", primaryProvider, primaryNet, regionConfig{
		Region:         cfg.PrimaryRegion,
		Image:          cfg.AppImage,
		Port:           cfg.AppPort,
		DBDSN:          database.PrimaryDSN,
		CertificateARN: cfg.PrimaryCertificateARN,
	})
	if err != nil {
		return err
	}

	secondary, err := newRegionStack(ctx, "golusoris-secondary", secondaryProvider, secondaryNet, regionConfig{
		Region:         cfg.SecondaryRegion,
		Image:          cfg.AppImage,
		Port:           cfg.AppPort,
		DBDSN:          database.SecondaryDSN,
		CertificateARN: cfg.SecondaryCertificateARN,
	})
	if err != nil {
		return err
	}

	if err = newGlobalDNS(
		ctx, primaryProvider, primary, secondary, cfg.Domain, cfg.HostedZoneID,
	); err != nil {
		return err
	}

	ctx.Export("primaryALBDNS", primary.ALBDNS)
	ctx.Export("secondaryALBDNS", secondary.ALBDNS)
	ctx.Export("globalURL", pulumi.String("https://"+cfg.Domain))
	return nil
}

// newRegionNetworks creates the application and database placement in both regions.
func newRegionNetworks(
	ctx *pulumi.Context,
	primaryProvider, secondaryProvider *aws.Provider,
	cfg stackConfig,
) (*regionNetwork, *regionNetwork, error) {
	primary, err := newRegionNetwork(
		ctx,
		"golusoris-primary",
		cfg.PrimaryRegion,
		pulumi.Provider(primaryProvider),
	)
	if err != nil {
		return nil, nil, err
	}
	secondary, err := newRegionNetwork(
		ctx,
		"golusoris-secondary",
		cfg.SecondaryRegion,
		pulumi.Provider(secondaryProvider),
	)
	if err != nil {
		return nil, nil, err
	}
	return primary, secondary, nil
}

// newProviders builds one explicit aws.Provider per region — the canonical multi-region pattern.
func newProviders(ctx *pulumi.Context, cfg stackConfig) (*aws.Provider, *aws.Provider, error) {
	primary, err := aws.NewProvider(ctx, "primary", &aws.ProviderArgs{
		Region: pulumi.String(cfg.PrimaryRegion),
	})
	if err != nil {
		return nil, nil, errorsWrap("primary", err)
	}
	secondary, err := aws.NewProvider(ctx, "secondary", &aws.ProviderArgs{
		Region: pulumi.String(cfg.SecondaryRegion),
	})
	if err != nil {
		return nil, nil, errorsWrap("secondary", err)
	}
	return primary, secondary, nil
}

// errorsWrap wraps a provider-creation failure with the region role.
func errorsWrap(role string, err error) error {
	return fmt.Errorf("pulumi: create %s provider: %w", role, err)
}

// loadConfig reads the stack config, applying defaults and requiring the secret password + appImage.
func loadConfig(ctx *pulumi.Context) (stackConfig, error) {
	cfg := config.New(ctx, "")
	out := stackConfig{
		PrimaryRegion:           orDefault(cfg.Get("primaryRegion"), "us-east-1"),
		SecondaryRegion:         orDefault(cfg.Get("secondaryRegion"), "us-west-2"),
		Domain:                  cfg.Get("domain"),
		HostedZoneID:            cfg.Get("hostedZoneId"),
		PrimaryCertificateARN:   cfg.Get("primaryCertificateArn"),
		SecondaryCertificateARN: cfg.Get("secondaryCertificateArn"),
		DBInstanceClass:         orDefault(cfg.Get("dbInstanceClass"), "db.r6g.large"),
		DBEngineVersion:         orDefault(cfg.Get("dbEngineVersion"), "16.6"),
		AppImage:                cfg.Get("appImage"),
		AppPort:                 orDefaultInt(cfg.GetInt("appPort"), 8080),
	}
	if err := validateStackConfig(out); err != nil {
		return stackConfig{}, err
	}
	out.DBPassword = cfg.RequireSecret("dbPassword")
	return out, nil
}

func validateStackConfig(cfg stackConfig) error {
	if cfg.Domain == "" {
		return errors.New("pulumi: config golusoris-multiregion:domain is required")
	}
	if cfg.HostedZoneID == "" {
		return errors.New("pulumi: config golusoris-multiregion:hostedZoneId is required")
	}
	if cfg.PrimaryCertificateARN == "" {
		return errors.New("pulumi: config golusoris-multiregion:primaryCertificateArn is required")
	}
	if cfg.SecondaryCertificateARN == "" {
		return errors.New("pulumi: config golusoris-multiregion:secondaryCertificateArn is required")
	}
	if cfg.AppImage == "" {
		return errors.New("pulumi: config golusoris-multiregion:appImage is required")
	}
	if err := imageref.ValidateImmutableSHA256(cfg.AppImage); err != nil {
		return fmt.Errorf("pulumi: validate appImage: %w", err)
	}
	return nil
}

// orDefault returns fallback when v is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// orDefaultInt returns fallback when v is zero.
func orDefaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
