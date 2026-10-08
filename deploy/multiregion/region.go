// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/secretsmanager"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// regionConfig parameterizes one region's app stack.
type regionConfig struct {
	Region         string
	Image          string
	Port           int
	DBDSN          pulumi.StringInput
	CertificateARN string
}

// regionNetwork is the VPC placement shared by the app and its regional Aurora cluster.
type regionNetwork struct {
	VPC            *ec2.Vpc
	PublicSubnets  []*ec2.Subnet
	PrivateSubnets []*ec2.Subnet
}

// regionStack is the per-region app + ALB; reused for both primary and secondary.
type regionStack struct {
	ALBDNS  pulumi.StringOutput
	ALBZone pulumi.StringOutput
}

// regionFrontend keeps the ALB association edge required before ECS service creation.
type regionFrontend struct {
	LoadBalancer *lb.LoadBalancer
	TargetGroup  *lb.TargetGroup
	Listener     *lb.Listener
}

// regionSecret keeps the secret version in the task-definition dependency graph.
type regionSecret struct {
	Secret  *secretsmanager.Secret
	Version *secretsmanager.SecretVersion
}

// regionExecutionRole keeps both resources required before ECS can use the role.
type regionExecutionRole struct {
	Role          *iam.Role
	ManagedPolicy *iam.RolePolicyAttachment
}

const (
	regionALBTLSPolicy = "ELBSecurityPolicy-TLS13-1-2-Res-PQ-2025-09"
	regionALBHSTS      = "max-age=31536000; includeSubDomains; preload"
)

// regionAZs pins the two AZ letters per region for the 2-public-subnet layout.
var regionAZs = []string{"a", "b"}

// newRegionStack builds a VPC + 2 public subnets + ALB + ECS Fargate service in the provider's region.
func newRegionStack(
	ctx *pulumi.Context,
	name string,
	prov *aws.Provider,
	net *regionNetwork,
	cfg regionConfig,
) (*regionStack, error) {
	opt := pulumi.Provider(prov)

	albSG, taskSG, err := newRegionSecurityGroups(ctx, name, net.VPC, cfg.Port, opt)
	if err != nil {
		return nil, err
	}

	front, err := newRegionFrontend(
		ctx, name, net.VPC, net.PublicSubnets, albSG, cfg.Port, cfg.CertificateARN, opt,
	)
	if err != nil {
		return nil, err
	}

	if err = newRegionService(ctx, name, net.PublicSubnets, taskSG, front, cfg, opt); err != nil {
		return nil, err
	}

	return &regionStack{ALBDNS: front.LoadBalancer.DnsName, ALBZone: front.LoadBalancer.ZoneId}, nil
}

// newRegionNetwork builds separate public app and private database subnet tiers.
func newRegionNetwork(
	ctx *pulumi.Context,
	name, region string,
	opt pulumi.ResourceOption,
) (*regionNetwork, error) {
	vpc, err := ec2.NewVpc(ctx, name+"-vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String("10.0.0.0/16"),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               pulumi.StringMap{"Name": pulumi.String(name + "-vpc")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create vpc %s: %w", name, err)
	}

	igw, err := ec2.NewInternetGateway(ctx, name+"-igw", &ec2.InternetGatewayArgs{
		VpcId: vpc.ID(),
		Tags:  pulumi.StringMap{"Name": pulumi.String(name + "-igw")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create igw %s: %w", name, err)
	}

	publicRT, err := newRegionRouteTable(ctx, name+"-public", vpc, ec2.RouteTableRouteArray{
		ec2.RouteTableRouteArgs{
			CidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId: igw.ID(),
		},
	}, opt)
	if err != nil {
		return nil, err
	}
	privateRT, err := newRegionRouteTable(ctx, name+"-private", vpc, nil, opt)
	if err != nil {
		return nil, err
	}
	publicSubnets, err := newRegionSubnets(ctx, name, region, "public", 0, true, vpc, publicRT, opt)
	if err != nil {
		return nil, err
	}
	privateSubnets, err := newRegionSubnets(ctx, name, region, "private", 10, false, vpc, privateRT, opt)
	if err != nil {
		return nil, err
	}
	return &regionNetwork{
		VPC:            vpc,
		PublicSubnets:  publicSubnets,
		PrivateSubnets: privateSubnets,
	}, nil
}

// newRegionRouteTable creates one explicitly managed regional route table.
func newRegionRouteTable(
	ctx *pulumi.Context,
	name string,
	vpc *ec2.Vpc,
	routes ec2.RouteTableRouteArray,
	opt pulumi.ResourceOption,
) (*ec2.RouteTable, error) {
	rt, err := ec2.NewRouteTable(ctx, name+"-rt", &ec2.RouteTableArgs{
		VpcId:  vpc.ID(),
		Routes: routes,
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-rt")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create route table %s: %w", name, err)
	}
	return rt, nil
}

// newRegionSubnets creates one two-AZ subnet tier and binds its route table.
func newRegionSubnets(
	ctx *pulumi.Context,
	name, region string,
	kind string,
	cidrOffset int,
	mapPublicIP bool,
	vpc *ec2.Vpc,
	rt *ec2.RouteTable,
	opt pulumi.ResourceOption,
) ([]*ec2.Subnet, error) {
	subnets := make([]*ec2.Subnet, 0, len(regionAZs))
	for i, az := range regionAZs {
		subnetName := fmt.Sprintf("%s-%s-%s", name, kind, az)
		sn, err := ec2.NewSubnet(ctx, subnetName, &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String(fmt.Sprintf("10.0.%d.0/24", i+cidrOffset)),
			AvailabilityZone:    pulumi.String(region + az),
			MapPublicIpOnLaunch: pulumi.Bool(mapPublicIP),
			Tags:                pulumi.StringMap{"Name": pulumi.String(subnetName)},
		}, opt)
		if err != nil {
			return nil, fmt.Errorf("pulumi: create subnet %s: %w", subnetName, err)
		}
		_, err = ec2.NewRouteTableAssociation(ctx, subnetName+"-rta", &ec2.RouteTableAssociationArgs{
			SubnetId:     sn.ID(),
			RouteTableId: rt.ID(),
		}, opt)
		if err != nil {
			return nil, fmt.Errorf("pulumi: associate subnet %s: %w", subnetName, err)
		}
		subnets = append(subnets, sn)
	}
	return subnets, nil
}

// subnetIDs returns the subnet IDs as a pulumi input.
func subnetIDs(subnets []*ec2.Subnet) pulumi.StringArray {
	ids := make(pulumi.StringArray, 0, len(subnets))
	for _, sn := range subnets {
		ids = append(ids, sn.ID())
	}
	return ids
}

// newRegionSecurityGroups returns (alb-sg open to internet on :443, task-sg open to the alb on app port).
func newRegionSecurityGroups(
	ctx *pulumi.Context,
	name string,
	vpc *ec2.Vpc,
	port int,
	opt pulumi.ResourceOption,
) (*ec2.SecurityGroup, *ec2.SecurityGroup, error) {
	albSG, err := ec2.NewSecurityGroup(ctx, name+"-alb-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpc.ID(),
		Description: pulumi.String("golusoris alb ingress from internet"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:   pulumi.String("tcp"),
			FromPort:   pulumi.Int(443),
			ToPort:     pulumi.Int(443),
			CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
		}},
		Egress: ec2.SecurityGroupEgressArray{anyEgress()},
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-alb-sg")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create alb sg %s: %w", name, err)
	}

	taskSG, err := ec2.NewSecurityGroup(ctx, name+"-task-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpc.ID(),
		Description: pulumi.String("golusoris task ingress from alb"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:       pulumi.String("tcp"),
			FromPort:       pulumi.Int(port),
			ToPort:         pulumi.Int(port),
			SecurityGroups: pulumi.StringArray{albSG.ID()},
		}},
		Egress: ec2.SecurityGroupEgressArray{anyEgress()},
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-task-sg")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create task sg %s: %w", name, err)
	}
	return albSG, taskSG, nil
}

// anyEgress is the shared "any outbound" rule.
func anyEgress() ec2.SecurityGroupEgressArgs {
	return ec2.SecurityGroupEgressArgs{
		Protocol:   pulumi.String("-1"),
		FromPort:   pulumi.Int(0),
		ToPort:     pulumi.Int(0),
		CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
	}
}

// newRegionFrontend builds the internet-facing ALB + target group + listener with a /readyz check.
func newRegionFrontend(
	ctx *pulumi.Context,
	name string,
	vpc *ec2.Vpc,
	subnets []*ec2.Subnet,
	albSG *ec2.SecurityGroup,
	port int,
	certificateARN string,
	opt pulumi.ResourceOption,
) (*regionFrontend, error) {
	alb, err := lb.NewLoadBalancer(ctx, name+"-alb", &lb.LoadBalancerArgs{
		LoadBalancerType: pulumi.String("application"),
		Internal:         pulumi.Bool(false),
		SecurityGroups:   pulumi.StringArray{albSG.ID()},
		Subnets:          subnetIDs(subnets),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-alb")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create alb %s: %w", name, err)
	}

	tg, err := lb.NewTargetGroup(ctx, name+"-tg", &lb.TargetGroupArgs{
		Port:       pulumi.Int(port),
		Protocol:   pulumi.String("HTTP"),
		TargetType: pulumi.String("ip"),
		VpcId:      vpc.ID(),
		HealthCheck: &lb.TargetGroupHealthCheckArgs{
			Enabled:  pulumi.Bool(true),
			Path:     pulumi.String("/readyz"),
			Protocol: pulumi.String("HTTP"),
			Matcher:  pulumi.String("200"),
		},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-tg")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create target group %s: %w", name, err)
	}

	listener, err := lb.NewListener(ctx, name+"-listener", &lb.ListenerArgs{
		LoadBalancerArn: alb.Arn,
		Port:            pulumi.Int(443),
		Protocol:        pulumi.String("HTTPS"),
		CertificateArn:  pulumi.String(certificateARN),
		SslPolicy:       pulumi.String(regionALBTLSPolicy),
		RoutingHttpResponseStrictTransportSecurityHeaderValue: pulumi.String(regionALBHSTS),
		DefaultActions: lb.ListenerDefaultActionArray{lb.ListenerDefaultActionArgs{
			Type:           pulumi.String("forward"),
			TargetGroupArn: tg.Arn,
		}},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create listener %s: %w", name, err)
	}
	return &regionFrontend{LoadBalancer: alb, TargetGroup: tg, Listener: listener}, nil
}

// newRegionService runs the app on Fargate (rootless, read-only FS) registered with the ALB.
func newRegionService(
	ctx *pulumi.Context,
	name string,
	subnets []*ec2.Subnet,
	taskSG *ec2.SecurityGroup,
	front *regionFrontend,
	cfg regionConfig,
	opt pulumi.ResourceOption,
) error {
	taskDef, err := newRegionTaskDefinition(ctx, name, cfg, opt)
	if err != nil {
		return err
	}

	cluster, err := ecs.NewCluster(ctx, name+"-cluster", &ecs.ClusterArgs{
		Name: pulumi.String(name + "-cluster"),
	}, opt)
	if err != nil {
		return fmt.Errorf("pulumi: create cluster %s: %w", name, err)
	}

	return newRegionFargateService(ctx, name, subnets, taskSG, front, cfg, taskDef, cluster, opt)
}

// newRegionTaskDefinition builds the Fargate task definition (rootless ARM64,
// 256 CPU units / 512 MiB) that runs cfg's container under name.
func newRegionTaskDefinition(
	ctx *pulumi.Context,
	name string,
	cfg regionConfig,
	opt pulumi.ResourceOption,
) (*ecs.TaskDefinition, error) {
	execRole, err := newRegionExecRole(ctx, name, opt)
	if err != nil {
		return nil, err
	}

	dsn, err := newRegionSecret(ctx, name+"-dsn", cfg.DBDSN, opt)
	if err != nil {
		return nil, err
	}
	secretPolicy, err := grantRegionSecretRead(ctx, name, execRole.Role, dsn.Secret, opt)
	if err != nil {
		return nil, err
	}

	containers := dsn.Secret.Arn.ApplyT(func(arn string) (string, error) {
		return regionContainer(name, cfg, arn)
	}).(pulumi.StringOutput)

	taskDef, err := ecs.NewTaskDefinition(ctx, name+"-task", &ecs.TaskDefinitionArgs{
		Family:                  pulumi.String(name),
		Cpu:                     pulumi.String("256"),
		Memory:                  pulumi.String("512"),
		NetworkMode:             pulumi.String("awsvpc"),
		RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		ExecutionRoleArn:        execRole.Role.Arn,
		RuntimePlatform: &ecs.TaskDefinitionRuntimePlatformArgs{
			OperatingSystemFamily: pulumi.String("LINUX"),
			CpuArchitecture:       pulumi.String("ARM64"),
		},
		ContainerDefinitions: containers,
		Tags:                 pulumi.StringMap{"Name": pulumi.String(name + "-task")},
	}, opt, pulumi.DependsOn([]pulumi.Resource{dsn.Version, execRole.ManagedPolicy, secretPolicy}))
	if err != nil {
		return nil, fmt.Errorf("pulumi: create task definition %s: %w", name, err)
	}
	return taskDef, nil
}

// newRegionFargateService registers the Fargate service (2 desired tasks,
// public IP assigned) with the ALB target group tg.
func newRegionFargateService(
	ctx *pulumi.Context,
	name string,
	subnets []*ec2.Subnet,
	taskSG *ec2.SecurityGroup,
	front *regionFrontend,
	cfg regionConfig,
	taskDef *ecs.TaskDefinition,
	cluster *ecs.Cluster,
	opt pulumi.ResourceOption,
) error {
	_, err := ecs.NewService(ctx, name+"-svc", &ecs.ServiceArgs{
		Cluster:        cluster.Arn,
		TaskDefinition: taskDef.Arn,
		DesiredCount:   pulumi.Int(2),
		LaunchType:     pulumi.String("FARGATE"),
		NetworkConfiguration: &ecs.ServiceNetworkConfigurationArgs{
			Subnets:        subnetIDs(subnets),
			SecurityGroups: pulumi.StringArray{taskSG.ID()},
			AssignPublicIp: pulumi.Bool(true),
		},
		LoadBalancers: ecs.ServiceLoadBalancerArray{ecs.ServiceLoadBalancerArgs{
			TargetGroupArn: front.TargetGroup.Arn,
			ContainerName:  pulumi.String(name),
			ContainerPort:  pulumi.Int(cfg.Port),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-svc")},
	}, opt, pulumi.DependsOn([]pulumi.Resource{front.Listener}))
	if err != nil {
		return fmt.Errorf("pulumi: create service %s: %w", name, err)
	}
	return nil
}

// newRegionExecRole creates the ECS task execution role and its managed policy attachment.
func newRegionExecRole(
	ctx *pulumi.Context,
	name string,
	opt pulumi.ResourceOption,
) (*regionExecutionRole, error) {
	role, err := iam.NewRole(ctx, name+"-exec-role", &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(ecsAssumeRolePolicy),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-exec-role")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create exec role %s: %w", name, err)
	}
	managedPolicy, err := iam.NewRolePolicyAttachment(ctx, name+"-exec-attach", &iam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"),
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: attach exec policy %s: %w", name, err)
	}
	return &regionExecutionRole{Role: role, ManagedPolicy: managedPolicy}, nil
}

// newRegionSecret stores a regional Aurora DSN for ECS secret injection.
func newRegionSecret(
	ctx *pulumi.Context,
	name string,
	value pulumi.StringInput,
	opt pulumi.ResourceOption,
) (*regionSecret, error) {
	secret, err := secretsmanager.NewSecret(ctx, name, &secretsmanager.SecretArgs{
		NamePrefix:           pulumi.String(name + "-"),
		RecoveryWindowInDays: pulumi.Int(0),
		Tags:                 pulumi.StringMap{"Name": pulumi.String(name)},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secret %s: %w", name, err)
	}
	version, err := secretsmanager.NewSecretVersion(ctx, name+"-v", &secretsmanager.SecretVersionArgs{
		SecretId:     secret.ID(),
		SecretString: value,
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secret version %s: %w", name, err)
	}
	return &regionSecret{Secret: secret, Version: version}, nil
}

// grantRegionSecretRead lets the ECS execution role resolve the regional DSN.
func grantRegionSecretRead(
	ctx *pulumi.Context,
	name string,
	role *iam.Role,
	dsn *secretsmanager.Secret,
	opt pulumi.ResourceOption,
) (*iam.RolePolicy, error) {
	policy := dsn.Arn.ApplyT(regionSecretReadPolicy).(pulumi.StringOutput)
	rolePolicy, err := iam.NewRolePolicy(ctx, name+"-secret-read", &iam.RolePolicyArgs{
		Role:   role.ID(),
		Policy: policy,
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: attach secret-read policy %s: %w", name, err)
	}
	return rolePolicy, nil
}

// regionSecretReadPolicy grants only the secret read needed at task start.
func regionSecretReadPolicy(dsnARN string) (string, error) {
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":   "Allow",
			"Action":   []string{"secretsmanager:GetSecretValue"},
			"Resource": []string{dsnARN},
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("pulumi: marshal secret-read policy: %w", err)
	}
	return string(b), nil
}

// regionContainer renders the single-container JSON ECS expects (rootless, read-only FS).
func regionContainer(name string, cfg regionConfig, dsnARN string) (string, error) {
	def := []map[string]any{{
		"name":                   name,
		"image":                  cfg.Image,
		"essential":              true,
		"readonlyRootFilesystem": true,
		"user":                   "65534",
		"portMappings":           []map[string]any{{"containerPort": cfg.Port, "protocol": "tcp"}},
		"environment": []map[string]any{
			{"name": "APP_HTTP_ADDR", "value": fmt.Sprintf(":%d", cfg.Port)},
		},
		"secrets": []map[string]any{
			{"name": "APP_DB_DSN", "valueFrom": dsnARN},
		},
	}}
	b, err := json.Marshal(def)
	if err != nil {
		return "", fmt.Errorf("pulumi: marshal container %s: %w", name, err)
	}
	return string(b), nil
}

// ecsAssumeRolePolicy is the trust policy letting ECS tasks assume the execution role.
const ecsAssumeRolePolicy = `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Service": "ecs-tasks.amazonaws.com"},
    "Action": "sts:AssumeRole"
  }]
}`
