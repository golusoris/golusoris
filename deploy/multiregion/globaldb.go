package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/rds"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// dbConfig is the knob set for the Aurora Global Database.
type dbConfig struct {
	InstanceClass string
	EngineVersion string
	Password      pulumi.StringInput // from `pulumi config set --secret`
}

// globalDB bundles the global cluster plus its writer (primary) + replica (secondary) clusters.
type globalDB struct {
	Global    *rds.GlobalCluster
	Primary   *rds.Cluster
	Secondary *rds.Cluster
}

// auroraEngine + auroraDBName are fixed so the writer + replica stay in lockstep.
const (
	auroraEngine = "aurora-postgresql"
	auroraDBName = "app"
	auroraUser   = "appuser"
)

// newGlobalDB creates the global cluster, a writer in the primary region, and a replica in the secondary.
func newGlobalDB(
	ctx *pulumi.Context,
	primary, secondary *aws.Provider,
	cfg dbConfig,
) (*globalDB, error) {
	global, err := rds.NewGlobalCluster(ctx, "golusoris-global", &rds.GlobalClusterArgs{
		GlobalClusterIdentifier: pulumi.String("golusoris-global"),
		Engine:                  pulumi.String(auroraEngine),
		EngineVersion:           pulumi.String(cfg.EngineVersion),
		DatabaseName:            pulumi.String(auroraDBName),
		StorageEncrypted:        pulumi.Bool(true),
		DeletionProtection:      pulumi.Bool(true),
	}, pulumi.Provider(primary))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create global cluster: %w", err)
	}

	writer, err := newWriterCluster(ctx, global, cfg, primary)
	if err != nil {
		return nil, err
	}

	replica, err := newReplicaCluster(ctx, global, writer, cfg, secondary)
	if err != nil {
		return nil, err
	}

	return &globalDB{Global: global, Primary: writer, Secondary: replica}, nil
}

// newWriterCluster builds the primary-region Aurora writer joined to the global cluster.
func newWriterCluster(
	ctx *pulumi.Context,
	global *rds.GlobalCluster,
	cfg dbConfig,
	prov *aws.Provider,
) (*rds.Cluster, error) {
	cluster, err := rds.NewCluster(ctx, "golusoris-primary", &rds.ClusterArgs{
		ClusterIdentifier:       pulumi.String("golusoris-primary"),
		Engine:                  pulumi.String(auroraEngine),
		EngineVersion:           pulumi.String(cfg.EngineVersion),
		GlobalClusterIdentifier: global.ID(),
		DatabaseName:            pulumi.String(auroraDBName),
		MasterUsername:          pulumi.String(auroraUser),
		MasterPassword:          cfg.Password,
		StorageEncrypted:        pulumi.Bool(true),
		DeletionProtection:      pulumi.Bool(true),
		SkipFinalSnapshot:       pulumi.Bool(false),
		FinalSnapshotIdentifier: pulumi.String("golusoris-primary-final"),
	}, pulumi.Provider(prov))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create primary cluster: %w", err)
	}
	if err = newClusterInstance(ctx, "golusoris-primary", cluster, cfg, prov); err != nil {
		return nil, err
	}
	return cluster, nil
}

// newReplicaCluster builds the secondary-region read replica; it depends on the writer existing first.
func newReplicaCluster(
	ctx *pulumi.Context,
	global *rds.GlobalCluster,
	writer *rds.Cluster,
	cfg dbConfig,
	prov *aws.Provider,
) (*rds.Cluster, error) {
	cluster, err := rds.NewCluster(ctx, "golusoris-secondary", &rds.ClusterArgs{
		ClusterIdentifier:       pulumi.String("golusoris-secondary"),
		Engine:                  pulumi.String(auroraEngine),
		EngineVersion:           pulumi.String(cfg.EngineVersion),
		GlobalClusterIdentifier: global.ID(),
		StorageEncrypted:        pulumi.Bool(true),
		DeletionProtection:      pulumi.Bool(true),
		SkipFinalSnapshot:       pulumi.Bool(true),
	}, pulumi.Provider(prov), pulumi.DependsOn([]pulumi.Resource{writer}))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secondary cluster: %w", err)
	}
	if err = newClusterInstance(ctx, "golusoris-secondary", cluster, cfg, prov); err != nil {
		return nil, err
	}
	return cluster, nil
}

// newClusterInstance attaches one writer/reader instance to a cluster in the given region.
func newClusterInstance(
	ctx *pulumi.Context,
	name string,
	cluster *rds.Cluster,
	cfg dbConfig,
	prov *aws.Provider,
) error {
	_, err := rds.NewClusterInstance(ctx, name+"-instance", &rds.ClusterInstanceArgs{
		ClusterIdentifier:          cluster.ID(),
		Engine:                     rds.EngineType(auroraEngine),
		InstanceClass:              pulumi.String(cfg.InstanceClass),
		PerformanceInsightsEnabled: pulumi.Bool(true),
	}, pulumi.Provider(prov))
	if err != nil {
		return fmt.Errorf("pulumi: create cluster instance %s: %w", name, err)
	}
	return nil
}
