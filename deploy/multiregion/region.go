package main

import (
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lb"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// regionConfig parameterizes one region's app stack.
type regionConfig struct {
	Region string
	Image  string
	Port   int
}

// regionStack is the per-region app + ALB; reused for both primary and secondary.
type regionStack struct {
	ALBDNS  pulumi.StringOutput
	ALBZone pulumi.StringOutput
}

// regionAZs pins the two AZ letters per region for the 2-public-subnet layout.
var regionAZs = []string{"a", "b"}

// newRegionStack builds a VPC + 2 public subnets + ALB + ECS Fargate service in the provider's region.
func newRegionStack(
	ctx *pulumi.Context,
	name string,
	prov *aws.Provider,
	cfg regionConfig,
) (*regionStack, error) {
	opt := pulumi.Provider(prov)

	vpc, subnets, err := newRegionNetwork(ctx, name, cfg.Region, opt)
	if err != nil {
		return nil, err
	}

	albSG, taskSG, err := newRegionSecurityGroups(ctx, name, vpc, cfg.Port, opt)
	if err != nil {
		return nil, err
	}

	alb, tg, err := newRegionFrontend(ctx, name, vpc, subnets, albSG, cfg.Port, opt)
	if err != nil {
		return nil, err
	}

	if err = newRegionService(ctx, name, subnets, taskSG, tg, cfg, opt); err != nil {
		return nil, err
	}

	return &regionStack{ALBDNS: alb.DnsName, ALBZone: alb.ZoneId}, nil
}

// newRegionNetwork builds a VPC with 2 internet-facing public subnets + an IGW route.
func newRegionNetwork(
	ctx *pulumi.Context,
	name, region string,
	opt pulumi.ResourceOption,
) (*ec2.Vpc, []*ec2.Subnet, error) {
	vpc, err := ec2.NewVpc(ctx, name+"-vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String("10.0.0.0/16"),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               pulumi.StringMap{"Name": pulumi.String(name + "-vpc")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create vpc %s: %w", name, err)
	}

	igw, err := ec2.NewInternetGateway(ctx, name+"-igw", &ec2.InternetGatewayArgs{
		VpcId: vpc.ID(),
		Tags:  pulumi.StringMap{"Name": pulumi.String(name + "-igw")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create igw %s: %w", name, err)
	}

	rt, err := ec2.NewRouteTable(ctx, name+"-rt", &ec2.RouteTableArgs{
		VpcId: vpc.ID(),
		Routes: ec2.RouteTableRouteArray{ec2.RouteTableRouteArgs{
			CidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId: igw.ID(),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-rt")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create route table %s: %w", name, err)
	}

	subnets, err := newRegionSubnets(ctx, name, region, vpc, rt, opt)
	if err != nil {
		return nil, nil, err
	}
	return vpc, subnets, nil
}

// newRegionSubnets creates the 2 public subnets and binds each to the public route table.
func newRegionSubnets(
	ctx *pulumi.Context,
	name, region string,
	vpc *ec2.Vpc,
	rt *ec2.RouteTable,
	opt pulumi.ResourceOption,
) ([]*ec2.Subnet, error) {
	subnets := make([]*ec2.Subnet, 0, len(regionAZs))
	for i, az := range regionAZs {
		sn, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-public-%s", name, az), &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String(fmt.Sprintf("10.0.%d.0/24", i)),
			AvailabilityZone:    pulumi.String(region + az),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags:                pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-public-%s", name, az))},
		}, opt)
		if err != nil {
			return nil, fmt.Errorf("pulumi: create subnet %s-%s: %w", name, az, err)
		}
		_, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-rta-%s", name, az), &ec2.RouteTableAssociationArgs{
			SubnetId:     sn.ID(),
			RouteTableId: rt.ID(),
		}, opt)
		if err != nil {
			return nil, fmt.Errorf("pulumi: associate subnet %s-%s: %w", name, az, err)
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

// newRegionSecurityGroups returns (alb-sg open to internet on :80, task-sg open to the alb on app port).
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
			FromPort:   pulumi.Int(80),
			ToPort:     pulumi.Int(80),
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
	opt pulumi.ResourceOption,
) (*lb.LoadBalancer, *lb.TargetGroup, error) {
	alb, err := lb.NewLoadBalancer(ctx, name+"-alb", &lb.LoadBalancerArgs{
		LoadBalancerType: pulumi.String("application"),
		Internal:         pulumi.Bool(false),
		SecurityGroups:   pulumi.StringArray{albSG.ID()},
		Subnets:          subnetIDs(subnets),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-alb")},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create alb %s: %w", name, err)
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
		return nil, nil, fmt.Errorf("pulumi: create target group %s: %w", name, err)
	}

	_, err = lb.NewListener(ctx, name+"-listener", &lb.ListenerArgs{
		LoadBalancerArn: alb.Arn,
		Port:            pulumi.Int(80),
		Protocol:        pulumi.String("HTTP"),
		DefaultActions: lb.ListenerDefaultActionArray{lb.ListenerDefaultActionArgs{
			Type:           pulumi.String("forward"),
			TargetGroupArn: tg.Arn,
		}},
	}, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create listener %s: %w", name, err)
	}
	return alb, tg, nil
}

// newRegionService runs the app on Fargate (rootless, read-only FS) registered with the ALB.
func newRegionService(
	ctx *pulumi.Context,
	name string,
	subnets []*ec2.Subnet,
	taskSG *ec2.SecurityGroup,
	tg *lb.TargetGroup,
	cfg regionConfig,
	opt pulumi.ResourceOption,
) error {
	execRole, err := newRegionExecRole(ctx, name, opt)
	if err != nil {
		return err
	}

	containers, err := regionContainer(name, cfg)
	if err != nil {
		return err
	}

	taskDef, err := ecs.NewTaskDefinition(ctx, name+"-task", &ecs.TaskDefinitionArgs{
		Family:                  pulumi.String(name),
		Cpu:                     pulumi.String("256"),
		Memory:                  pulumi.String("512"),
		NetworkMode:             pulumi.String("awsvpc"),
		RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		ExecutionRoleArn:        execRole.Arn,
		RuntimePlatform: &ecs.TaskDefinitionRuntimePlatformArgs{
			OperatingSystemFamily: pulumi.String("LINUX"),
			CpuArchitecture:       pulumi.String("ARM64"),
		},
		ContainerDefinitions: pulumi.String(containers),
		Tags:                 pulumi.StringMap{"Name": pulumi.String(name + "-task")},
	}, opt)
	if err != nil {
		return fmt.Errorf("pulumi: create task definition %s: %w", name, err)
	}

	cluster, err := ecs.NewCluster(ctx, name+"-cluster", &ecs.ClusterArgs{
		Name: pulumi.String(name + "-cluster"),
	}, opt)
	if err != nil {
		return fmt.Errorf("pulumi: create cluster %s: %w", name, err)
	}

	_, err = ecs.NewService(ctx, name+"-svc", &ecs.ServiceArgs{
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
			TargetGroupArn: tg.Arn,
			ContainerName:  pulumi.String(name),
			ContainerPort:  pulumi.Int(cfg.Port),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-svc")},
	}, opt)
	if err != nil {
		return fmt.Errorf("pulumi: create service %s: %w", name, err)
	}
	return nil
}

// newRegionExecRole creates the ECS task execution role (image pull + log write) in-region.
func newRegionExecRole(
	ctx *pulumi.Context,
	name string,
	opt pulumi.ResourceOption,
) (*iam.Role, error) {
	role, err := iam.NewRole(ctx, name+"-exec-role", &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(ecsAssumeRolePolicy),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-exec-role")},
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create exec role %s: %w", name, err)
	}
	_, err = iam.NewRolePolicyAttachment(ctx, name+"-exec-attach", &iam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"),
	}, opt)
	if err != nil {
		return nil, fmt.Errorf("pulumi: attach exec policy %s: %w", name, err)
	}
	return role, nil
}

// regionContainer renders the single-container JSON ECS expects (rootless, read-only FS).
func regionContainer(name string, cfg regionConfig) (string, error) {
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
