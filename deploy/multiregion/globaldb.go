// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"net"
	"net/url"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
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
	Global       *rds.GlobalCluster
	Primary      *rds.Cluster
	Secondary    *rds.Cluster
	PrimaryDSN   pulumi.StringOutput
	SecondaryDSN pulumi.StringOutput
}

// dbPlacement keeps one Aurora cluster in the same VPC as its regional app.
type dbPlacement struct {
	SubnetGroup   *rds.SubnetGroup
	SecurityGroup *ec2.SecurityGroup
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
	primaryNet, secondaryNet *regionNetwork,
	cfg dbConfig,
) (*globalDB, error) {
	primaryPlacement, err := newDBPlacement(ctx, "golusoris-primary", primaryNet, primary)
	if err != nil {
		return nil, err
	}
	secondaryPlacement, err := newDBPlacement(ctx, "golusoris-secondary", secondaryNet, secondary)
	if err != nil {
		return nil, err
	}

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

	writer, err := newWriterCluster(ctx, global, primaryPlacement, cfg, primary)
	if err != nil {
		return nil, err
	}

	replica, err := newReplicaCluster(ctx, global, writer, secondaryPlacement, cfg, secondary)
	if err != nil {
		return nil, err
	}

	return &globalDB{
		Global:       global,
		Primary:      writer,
		Secondary:    replica,
		PrimaryDSN:   clusterDSN(writer.Endpoint, cfg.Password),
		SecondaryDSN: clusterDSN(replica.Endpoint, cfg.Password),
	}, nil
}

// newDBPlacement creates private Aurora placement reachable only inside one regional VPC.
func newDBPlacement(
	ctx *pulumi.Context,
	name string,
	network *regionNetwork,
	provider *aws.Provider,
) (*dbPlacement, error) {
	opt := pulumi.Provider(provider)
	subnetGroup, err := rds.NewSubnetGroup(ctx, name+"-db-subnets", &rds.SubnetGroupArgs{
		SubnetIds: subnetIDs(network.PrivateSubnets),
		Tags:      pulumi.StringMap{"Name": pulumi.String(name + "-db-subnets")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create db subnet group %s: %w", name, err)
	}
	securityGroup, err := ec2.NewSecurityGroup(ctx, name+"-db-sg", &ec2.SecurityGroupArgs{
		VpcId:       network.VPC.ID(),
		Description: pulumi.String("golusoris aurora ingress from regional vpc"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:   pulumi.String("tcp"),
			FromPort:   pulumi.Int(5432),
			ToPort:     pulumi.Int(5432),
			CidrBlocks: pulumi.StringArray{network.VPC.CidrBlock},
		}},
		Egress: ec2.SecurityGroupEgressArray{anyEgress()},
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-db-sg")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create db security group %s: %w", name, err)
	}
	return &dbPlacement{SubnetGroup: subnetGroup, SecurityGroup: securityGroup}, nil
}

// newWriterCluster builds the primary-region Aurora writer joined to the global cluster.
func newWriterCluster(
	ctx *pulumi.Context,
	global *rds.GlobalCluster,
	placement *dbPlacement,
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
		DbSubnetGroupName:       placement.SubnetGroup.Name,
		VpcSecurityGroupIds:     pulumi.StringArray{placement.SecurityGroup.ID()},
		StorageEncrypted:        pulumi.Bool(true),
		DeletionProtection:      pulumi.Bool(true),
		SkipFinalSnapshot:       pulumi.Bool(false),
		FinalSnapshotIdentifier: pulumi.String("golusoris-primary-final"),
	}, pulumi.Provider(prov))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create primary cluster: %w", err)
	}
	if err = newClusterInstance(ctx, "golusoris-primary", cluster, placement, cfg, prov); err != nil {
		return nil, err
	}
	return cluster, nil
}

// newReplicaCluster builds the secondary-region read replica; it depends on the writer existing first.
func newReplicaCluster(
	ctx *pulumi.Context,
	global *rds.GlobalCluster,
	writer *rds.Cluster,
	placement *dbPlacement,
	cfg dbConfig,
	prov *aws.Provider,
) (*rds.Cluster, error) {
	cluster, err := rds.NewCluster(ctx, "golusoris-secondary", &rds.ClusterArgs{
		ClusterIdentifier:       pulumi.String("golusoris-secondary"),
		Engine:                  pulumi.String(auroraEngine),
		EngineVersion:           pulumi.String(cfg.EngineVersion),
		GlobalClusterIdentifier: global.ID(),
		DbSubnetGroupName:       placement.SubnetGroup.Name,
		VpcSecurityGroupIds:     pulumi.StringArray{placement.SecurityGroup.ID()},
		StorageEncrypted:        pulumi.Bool(true),
		DeletionProtection:      pulumi.Bool(true),
		SkipFinalSnapshot:       pulumi.Bool(true),
	}, pulumi.Provider(prov), pulumi.DependsOn([]pulumi.Resource{writer}))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secondary cluster: %w", err)
	}
	if err = newClusterInstance(ctx, "golusoris-secondary", cluster, placement, cfg, prov); err != nil {
		return nil, err
	}
	return cluster, nil
}

// newClusterInstance attaches one writer/reader instance to a cluster in the given region.
func newClusterInstance(
	ctx *pulumi.Context,
	name string,
	cluster *rds.Cluster,
	placement *dbPlacement,
	cfg dbConfig,
	prov *aws.Provider,
) error {
	_, err := rds.NewClusterInstance(ctx, name+"-instance", &rds.ClusterInstanceArgs{
		ClusterIdentifier:          cluster.ID(),
		DbSubnetGroupName:          placement.SubnetGroup.Name,
		Engine:                     rds.EngineType(auroraEngine),
		InstanceClass:              pulumi.String(cfg.InstanceClass),
		PerformanceInsightsEnabled: pulumi.Bool(true),
		PubliclyAccessible:         pulumi.Bool(false),
	}, pulumi.Provider(prov))
	if err != nil {
		return fmt.Errorf("pulumi: create cluster instance %s: %w", name, err)
	}
	return nil
}

// clusterDSN builds a secret-propagating DSN for a regional Aurora endpoint.
func clusterDSN(endpoint, password pulumi.StringInput) pulumi.StringOutput {
	return pulumi.All(endpoint, password).ApplyT(func(values []any) string {
		return formatClusterDSN(values[0].(string), values[1].(string))
	}).(pulumi.StringOutput)
}

// formatClusterDSN escapes credentials and brackets address literals correctly.
func formatClusterDSN(endpoint, password string) string {
	dsn := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(auroraUser, password),
		Host:   net.JoinHostPort(endpoint, "5432"),
		Path:   "/" + auroraDBName,
	}
	query := dsn.Query()
	query.Set("sslmode", "require")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}
