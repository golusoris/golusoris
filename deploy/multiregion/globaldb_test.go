// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestGlobalDBUsesRegionalVPCPlacement(t *testing.T) {
	t.Parallel()
	mocks := &stackMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		primaryProvider, err := aws.NewProvider(ctx, "primary", &aws.ProviderArgs{Region: pulumi.String("us-east-1")})
		if err != nil {
			return err
		}
		secondaryProvider, err := aws.NewProvider(ctx, "secondary", &aws.ProviderArgs{Region: pulumi.String("us-west-2")})
		if err != nil {
			return err
		}
		primaryNet, err := newMockRegionNetwork(ctx, "primary")
		if err != nil {
			return err
		}
		secondaryNet, err := newMockRegionNetwork(ctx, "secondary")
		if err != nil {
			return err
		}
		_, err = newGlobalDB(ctx, primaryProvider, secondaryProvider, primaryNet, secondaryNet, dbConfig{
			InstanceClass: "db.r6g.large",
			EngineVersion: "16.6",
			Password:      pulumi.ToSecret(pulumi.String("p@ss/word")).(pulumi.StringOutput),
		})
		return err
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
	if err != nil {
		t.Fatalf("newGlobalDB() error = %v", err)
	}

	clusters := mocks.findAll("aws:rds/cluster:Cluster")
	if len(clusters) != 2 {
		t.Fatalf("regional cluster count = %d, want 2", len(clusters))
	}
	for _, cluster := range clusters {
		if got := cluster.inputs["dbSubnetGroupName"].StringValue(); got == "" {
			t.Errorf("cluster %s has empty dbSubnetGroupName", cluster.name)
		}
		if got := cluster.inputs["vpcSecurityGroupIds"].ArrayValue(); len(got) != 1 {
			t.Errorf("cluster %s security group count = %d, want 1", cluster.name, len(got))
		}
	}
	for _, subnetGroup := range mocks.findAll("aws:rds/subnetGroup:SubnetGroup") {
		for _, subnetID := range subnetGroup.inputs["subnetIds"].ArrayValue() {
			if !strings.Contains(subnetID.StringValue(), "private") {
				t.Errorf("subnet group %s contains non-private subnet %q", subnetGroup.name, subnetID.StringValue())
			}
		}
	}

	instances := mocks.findAll("aws:rds/clusterInstance:ClusterInstance")
	if len(instances) != 2 {
		t.Fatalf("regional instance count = %d, want 2", len(instances))
	}
	for _, instance := range instances {
		if got := instance.inputs["dbSubnetGroupName"].StringValue(); got == "" {
			t.Errorf("instance %s has empty dbSubnetGroupName", instance.name)
		}
		if instance.inputs["publiclyAccessible"].BoolValue() {
			t.Errorf("instance %s is publicly accessible", instance.name)
		}
	}
}

func newMockRegionNetwork(ctx *pulumi.Context, name string) (*regionNetwork, error) {
	vpc, err := ec2.NewVpc(ctx, name+"-vpc", &ec2.VpcArgs{CidrBlock: pulumi.String("10.0.0.0/16")})
	if err != nil {
		return nil, err
	}
	publicSubnets := make([]*ec2.Subnet, 0, 2)
	privateSubnets := make([]*ec2.Subnet, 0, 2)
	for i := range 2 {
		publicSubnet, createErr := ec2.NewSubnet(ctx, name+"-public-"+string(rune('a'+i)), &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.0.0/24"),
		})
		if createErr != nil {
			return nil, createErr
		}
		privateSubnet, createErr := ec2.NewSubnet(ctx, name+"-private-"+string(rune('a'+i)), &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.10.0/24"),
		})
		if createErr != nil {
			return nil, createErr
		}
		publicSubnets = append(publicSubnets, publicSubnet)
		privateSubnets = append(privateSubnets, privateSubnet)
	}
	return &regionNetwork{
		VPC:            vpc,
		PublicSubnets:  publicSubnets,
		PrivateSubnets: privateSubnets,
	}, nil
}

func TestFormatClusterDSNEscapesCredentialsAndAddress(t *testing.T) {
	t.Parallel()
	got := formatClusterDSN("2001:db8::1", "p@ss/word")
	want := "postgres://appuser:p%40ss%2Fword@[2001:db8::1]:5432/app?sslmode=require"
	if got != want {
		t.Fatalf("formatClusterDSN() = %q, want %q", got, want)
	}
}
