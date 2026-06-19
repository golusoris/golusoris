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
)

// stackConfig is the resolved, typed view of the multiregion stack config.
type stackConfig struct {
	PrimaryRegion   string
	SecondaryRegion string
	Domain          string
	DBInstanceClass string
	DBEngineVersion string
	AppImage        string
	AppPort         int
	DBPassword      pulumi.StringInput
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

	if _, err = newGlobalDB(ctx, primaryProvider, secondaryProvider, dbConfig{
		InstanceClass: cfg.DBInstanceClass,
		EngineVersion: cfg.DBEngineVersion,
		Password:      cfg.DBPassword,
	}); err != nil {
		return err
	}

	primary, err := newRegionStack(ctx, "golusoris-primary", primaryProvider, regionConfig{
		Region: cfg.PrimaryRegion,
		Image:  cfg.AppImage,
		Port:   cfg.AppPort,
	})
	if err != nil {
		return err
	}

	secondary, err := newRegionStack(ctx, "golusoris-secondary", secondaryProvider, regionConfig{
		Region: cfg.SecondaryRegion,
		Image:  cfg.AppImage,
		Port:   cfg.AppPort,
	})
	if err != nil {
		return err
	}

	if _, err = newGlobalDNS(ctx, primary, secondary, cfg.Domain); err != nil {
		return err
	}

	ctx.Export("primaryURL", pulumi.Sprintf("http://%s", primary.ALBDNS))
	ctx.Export("secondaryURL", pulumi.Sprintf("http://%s", secondary.ALBDNS))
	ctx.Export("globalDomain", pulumi.String(cfg.Domain))
	return nil
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
		PrimaryRegion:   orDefault(cfg.Get("primaryRegion"), "us-east-1"),
		SecondaryRegion: orDefault(cfg.Get("secondaryRegion"), "us-west-2"),
		Domain:          cfg.Get("domain"),
		DBInstanceClass: orDefault(cfg.Get("dbInstanceClass"), "db.r6g.large"),
		DBEngineVersion: orDefault(cfg.Get("dbEngineVersion"), "16.6"),
		AppImage:        cfg.Get("appImage"),
		AppPort:         orDefaultInt(cfg.GetInt("appPort"), 8080),
		DBPassword:      cfg.RequireSecret("dbPassword"),
	}
	if out.Domain == "" {
		return stackConfig{}, errors.New("pulumi: config golusoris-multiregion:domain is required")
	}
	if out.AppImage == "" {
		return stackConfig{}, errors.New("pulumi: config golusoris-multiregion:appImage is required")
	}
	return out, nil
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
