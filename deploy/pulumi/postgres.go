package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/rds"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// pgConfig is the production-default knob set for the managed Postgres instance.
type pgConfig struct {
	InstanceClass      string
	StorageGB          int
	EngineVersion      string
	MultiAZ            bool
	DeletionProtection bool
	Password           pulumi.StringInput // from `pulumi config set --secret`
}

// postgres exposes the stable connection surface the app tier consumes.
type postgres struct {
	Instance *rds.Instance
	DSN      pulumi.StringOutput // postgres://user:pass@host:port/db?sslmode=require
	Host     pulumi.StringOutput
	Port     pulumi.IntOutput
}

// pgDBName + pgUser are fixed so the exported DSN matches the helm/crossplane defaults.
const (
	pgDBName = "app"
	pgUser   = "appuser"
)

// newPostgres provisions an encrypted, IAM-auth RDS Postgres mirroring terraform/modules/postgres.
func newPostgres(
	ctx *pulumi.Context,
	name string,
	net *network,
	cfg pgConfig,
	opts ...pulumi.ResourceOption,
) (*postgres, error) {
	sg, err := newDBSecurityGroup(ctx, name, net, opts...)
	if err != nil {
		return nil, err
	}

	subnetGroup, err := rds.NewSubnetGroup(ctx, name+"-db-subnets", &rds.SubnetGroupArgs{
		SubnetIds: net.privateSubnetIDs(),
		Tags:      pulumi.StringMap{"Name": pulumi.String(name + "-db-subnets")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create db subnet group %s: %w", name, err)
	}

	inst, err := rds.NewInstance(ctx, name+"-db", pgInstanceArgs(name, cfg, sg, subnetGroup), opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create db instance %s: %w", name, err)
	}

	dsn := pulumi.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=require",
		pgUser, cfg.Password, inst.Address, inst.Port, pgDBName,
	)
	return &postgres{Instance: inst, DSN: dsn, Host: inst.Address, Port: inst.Port}, nil
}

// pgInstanceArgs centralises the hardened RDS defaults (encryption, gp3, PerfInsights, IAM auth, backups).
func pgInstanceArgs(
	name string,
	cfg pgConfig,
	sg *ec2.SecurityGroup,
	subnetGroup *rds.SubnetGroup,
) *rds.InstanceArgs {
	return &rds.InstanceArgs{
		Identifier:                       pulumi.String(name + "-db"),
		Engine:                           pulumi.String("postgres"),
		EngineVersion:                    pulumi.String(cfg.EngineVersion),
		InstanceClass:                    pulumi.String(cfg.InstanceClass),
		AllocatedStorage:                 pulumi.Int(cfg.StorageGB),
		StorageType:                      pulumi.String("gp3"),
		StorageEncrypted:                 pulumi.Bool(true),
		DbName:                           pulumi.String(pgDBName),
		Username:                         pulumi.String(pgUser),
		Password:                         cfg.Password,
		DbSubnetGroupName:                subnetGroup.Name,
		VpcSecurityGroupIds:              pulumi.StringArray{sg.ID()},
		MultiAz:                          pulumi.Bool(cfg.MultiAZ),
		PubliclyAccessible:               pulumi.Bool(false),
		IamDatabaseAuthenticationEnabled: pulumi.Bool(true),
		PerformanceInsightsEnabled:       pulumi.Bool(true),
		BackupRetentionPeriod:            pulumi.Int(7),
		DeletionProtection:               pulumi.Bool(cfg.DeletionProtection),
		SkipFinalSnapshot:                pulumi.Bool(!cfg.DeletionProtection),
		Tags:                             pulumi.StringMap{"Name": pulumi.String(name + "-db")},
	}
}

// newDBSecurityGroup allows Postgres only from inside the VPC (private CIDR), never the internet.
func newDBSecurityGroup(
	ctx *pulumi.Context,
	name string,
	net *network,
	opts ...pulumi.ResourceOption,
) (*ec2.SecurityGroup, error) {
	sg, err := ec2.NewSecurityGroup(ctx, name+"-db-sg", &ec2.SecurityGroupArgs{
		VpcId:       net.VPC.ID(),
		Description: pulumi.String("golusoris db ingress from vpc"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:   pulumi.String("tcp"),
			FromPort:   pulumi.Int(5432),
			ToPort:     pulumi.Int(5432),
			CidrBlocks: pulumi.StringArray{net.VPC.CidrBlock},
		}},
		Egress: ec2.SecurityGroupEgressArray{ec2.SecurityGroupEgressArgs{
			Protocol:   pulumi.String("-1"),
			FromPort:   pulumi.Int(0),
			ToPort:     pulumi.Int(0),
			CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-db-sg")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create db security group %s: %w", name, err)
	}
	return sg, nil
}
