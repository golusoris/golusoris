// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command golusoris-app is a reference Pulumi (Go) program deploying a
// golusoris application on AWS: VPC + RDS Postgres + ElastiCache Redis + ECS
// Fargate behind an ALB. It is documentation-grade IaC — copy and adapt.
//
// Run:
//
//	pulumi stack init dev
//	pulumi config set --secret golusoris-app:dbPassword <strong-password>
//	pulumi up
//
// Stack outputs map dsn/redisURL onto APP_DB_DSN/APP_CACHE_REDIS_ADDR and
// expose appURL as the configured HTTPS domain.
package main

import (
	"errors"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	"github.com/golusoris/golusoris/deploy/internal/imageref"
)

// stackConfig is the resolved, typed view of the Pulumi stack config.
type stackConfig struct {
	Region             string
	DBInstanceClass    string
	DBStorageGB        int
	DBEngineVersion    string
	RedisNodeType      string
	AppImage           string
	AppReplicas        int
	AppPort            int
	Domain             string
	HostedZoneID       string
	CertificateARN     string
	MultiAZ            bool
	DeletionProtection bool
	DBPassword         pulumi.StringInput
}

func main() {
	pulumi.Run(run)
}

// run is the program entrypoint; it wires network → postgres → redis → app and exports outputs.
func run(ctx *pulumi.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	net, err := newNetwork(ctx, "golusoris", cfg.Region)
	if err != nil {
		return err
	}

	db, err := newPostgres(ctx, "golusoris", net, pgConfig{
		InstanceClass:      cfg.DBInstanceClass,
		StorageGB:          cfg.DBStorageGB,
		EngineVersion:      cfg.DBEngineVersion,
		MultiAZ:            cfg.MultiAZ,
		DeletionProtection: cfg.DeletionProtection,
		Password:           cfg.DBPassword,
	})
	if err != nil {
		return err
	}

	cache, err := newRedis(ctx, "golusoris", net, redisConfig{
		NodeType: cfg.RedisNodeType,
		MultiAZ:  cfg.MultiAZ,
	})
	if err != nil {
		return err
	}

	application, err := newApp(ctx, "golusoris", net, db, cache, appConfig{
		Image:          cfg.AppImage,
		Replicas:       cfg.AppReplicas,
		Port:           cfg.AppPort,
		Region:         cfg.Region,
		Domain:         cfg.Domain,
		HostedZoneID:   cfg.HostedZoneID,
		CertificateARN: cfg.CertificateARN,
	})
	if err != nil {
		return err
	}

	ctx.Export("dsn", db.DSN)
	ctx.Export("redisAddress", cache.Address)
	ctx.Export("redisURL", cache.URL)
	ctx.Export("appURL", application.URL)
	return nil
}

// loadConfig reads the stack config, applying the documented defaults and requiring the secret password.
func loadConfig(ctx *pulumi.Context) (stackConfig, error) {
	cfg := config.New(ctx, "")
	out := stackConfig{
		Region:             orDefault(cfg.Get("region"), "us-east-1"),
		DBInstanceClass:    orDefault(cfg.Get("dbInstanceClass"), "db.t4g.small"),
		DBStorageGB:        orDefaultInt(cfg.GetInt("dbStorageGB"), 20),
		DBEngineVersion:    orDefault(cfg.Get("dbEngineVersion"), "17.2"),
		RedisNodeType:      orDefault(cfg.Get("redisNodeType"), "cache.t3.micro"),
		AppReplicas:        orDefaultInt(cfg.GetInt("appReplicas"), 2),
		AppPort:            orDefaultInt(cfg.GetInt("appPort"), 8080),
		Domain:             cfg.Get("domain"),
		HostedZoneID:       cfg.Get("hostedZoneId"),
		CertificateARN:     cfg.Get("certificateArn"),
		MultiAZ:            cfg.GetBool("multiAZ"),
		DeletionProtection: cfg.GetBool("deletionProtection"),
		AppImage:           cfg.Get("appImage"),
	}
	if err := validateStackConfig(out); err != nil {
		return stackConfig{}, err
	}
	out.DBPassword = cfg.RequireSecret("dbPassword")
	return out, nil
}

func validateStackConfig(cfg stackConfig) error {
	if cfg.AppImage == "" {
		return errors.New("pulumi: config golusoris-app:appImage is required")
	}
	if err := imageref.ValidateImmutableSHA256(cfg.AppImage); err != nil {
		return fmt.Errorf("pulumi: validate appImage: %w", err)
	}
	if cfg.Domain == "" {
		return errors.New("pulumi: config golusoris-app:domain is required")
	}
	if cfg.HostedZoneID == "" {
		return errors.New("pulumi: config golusoris-app:hostedZoneId is required")
	}
	if cfg.CertificateARN == "" {
		return errors.New("pulumi: config golusoris-app:certificateArn is required")
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

// orDefaultInt returns fallback when v is zero (the config getter's empty value).
func orDefaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
