// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/elasticache"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// redisConfig is the knob set for the ElastiCache replication group.
type redisConfig struct {
	NodeType string
	MultiAZ  bool
}

// redis exposes the cache address surface the app tier consumes.
type redis struct {
	Group   *elasticache.ReplicationGroup
	Address pulumi.StringOutput // host:6379 for cache.redis.addr
	URL     pulumi.StringOutput // rediss://host:6379 for operator integrations
	Host    pulumi.StringOutput
}

// redisPort is fixed; ElastiCache Redis always listens on 6379.
const redisPort = 6379

// newRedis provisions an ElastiCache replication group mirroring the crossplane redis block.
func newRedis(
	ctx *pulumi.Context,
	name string,
	net *network,
	cfg redisConfig,
	opts ...pulumi.ResourceOption,
) (*redis, error) {
	sg, err := newCacheSecurityGroup(ctx, name, net, opts...)
	if err != nil {
		return nil, err
	}

	subnetGroup, err := elasticache.NewSubnetGroup(ctx, name+"-cache-subnets", &elasticache.SubnetGroupArgs{
		SubnetIds: net.privateSubnetIDs(),
		Tags:      pulumi.StringMap{"Name": pulumi.String(name + "-cache-subnets")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create cache subnet group %s: %w", name, err)
	}

	clusters := 1
	if cfg.MultiAZ {
		clusters = 2
	}
	group, err := elasticache.NewReplicationGroup(ctx, name+"-cache", &elasticache.ReplicationGroupArgs{
		Description:              pulumi.String("golusoris cache"),
		Engine:                   pulumi.String("redis"),
		NodeType:                 pulumi.String(cfg.NodeType),
		NumCacheClusters:         pulumi.Int(clusters),
		AutomaticFailoverEnabled: pulumi.Bool(cfg.MultiAZ),
		MultiAzEnabled:           pulumi.Bool(cfg.MultiAZ),
		Port:                     pulumi.Int(redisPort),
		AtRestEncryptionEnabled:  pulumi.Bool(true),
		TransitEncryptionEnabled: pulumi.Bool(true),
		SubnetGroupName:          subnetGroup.Name,
		SecurityGroupIds:         pulumi.StringArray{sg.ID()},
		Tags:                     pulumi.StringMap{"Name": pulumi.String(name + "-cache")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create cache replication group %s: %w", name, err)
	}

	address := group.PrimaryEndpointAddress.ApplyT(func(host string) string {
		value, _ := formatRedisConnection(host)
		return value
	}).(pulumi.StringOutput)
	connectionURL := group.PrimaryEndpointAddress.ApplyT(func(host string) string {
		_, value := formatRedisConnection(host)
		return value
	}).(pulumi.StringOutput)
	return &redis{
		Group:   group,
		Address: address,
		URL:     connectionURL,
		Host:    group.PrimaryEndpointAddress,
	}, nil
}

// formatRedisConnection returns the app's address and the operator-facing TLS URL.
func formatRedisConnection(host string) (string, string) {
	address := net.JoinHostPort(host, strconv.Itoa(redisPort))
	connectionURL := (&url.URL{Scheme: "rediss", Host: address}).String()
	return address, connectionURL
}

// newCacheSecurityGroup allows Redis only from inside the VPC, never the internet.
func newCacheSecurityGroup(
	ctx *pulumi.Context,
	name string,
	net *network,
	opts ...pulumi.ResourceOption,
) (*ec2.SecurityGroup, error) {
	sg, err := ec2.NewSecurityGroup(ctx, name+"-cache-sg", &ec2.SecurityGroupArgs{
		VpcId:       net.VPC.ID(),
		Description: pulumi.String("golusoris cache ingress from vpc"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:   pulumi.String("tcp"),
			FromPort:   pulumi.Int(redisPort),
			ToPort:     pulumi.Int(redisPort),
			CidrBlocks: pulumi.StringArray{net.VPC.CidrBlock},
		}},
		Egress: ec2.SecurityGroupEgressArray{ec2.SecurityGroupEgressArgs{
			Protocol:   pulumi.String("-1"),
			FromPort:   pulumi.Int(0),
			ToPort:     pulumi.Int(0),
			CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-cache-sg")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create cache security group %s: %w", name, err)
	}
	return sg, nil
}
