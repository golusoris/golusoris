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
// Stack outputs (dsn, redisURL) map onto the app's APP_DB_DSN / APP_CACHE_ADDR
// env vars — the same keys deploy/helm injects, so config never drifts between
// Helm- and Pulumi-deployed instances.
package main

import (
	"errors"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
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
		Image:    cfg.AppImage,
		Replicas: cfg.AppReplicas,
		Port:     cfg.AppPort,
		Region:   cfg.Region,
	})
	if err != nil {
		return err
	}

	ctx.Export("dsn", db.DSN)
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
		MultiAZ:            cfg.GetBool("multiAZ"),
		DeletionProtection: cfg.GetBool("deletionProtection"),
		DBPassword:         cfg.RequireSecret("dbPassword"),
	}

	out.AppImage = cfg.Get("appImage")
	if out.AppImage == "" {
		return stackConfig{}, errors.New("pulumi: config golusoris-app:appImage is required")
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

// orDefaultInt returns fallback when v is zero (the config getter's empty value).
func orDefaultInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
