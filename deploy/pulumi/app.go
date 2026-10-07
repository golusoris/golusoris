// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecs"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/secretsmanager"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// appConfig is the knob set for the ECS Fargate service.
type appConfig struct {
	Image          string
	Replicas       int
	Port           int
	Region         string // for the awslogs driver
	Domain         string
	HostedZoneID   string
	CertificateARN string
}

// app exposes the public URL of the deployed service.
type app struct {
	Service *ecs.Service
	URL     pulumi.StringOutput // https://<configured-domain>
	ALBZone pulumi.StringOutput // for route53 alias records
	ALBDNS  pulumi.StringOutput
}

// appSecret keeps each secret value in the task-definition dependency graph.
type appSecret struct {
	Secret  *secretsmanager.Secret
	Version *secretsmanager.SecretVersion
}

// appExecutionRole keeps both resources required before ECS can use the role.
type appExecutionRole struct {
	Role          *iam.Role
	ManagedPolicy *iam.RolePolicyAttachment
}

type appTaskResources struct {
	executionRole *appExecutionRole
	secretPolicy  *iam.RolePolicy
	dsnSecret     *appSecret
	redisSecret   *appSecret
}

// taskCPU + taskMemory are the Fargate sizing defaults (0.25 vCPU / 512 MiB).
const (
	taskCPU      = "256"
	taskMemory   = "512"
	albTLSPolicy = "ELBSecurityPolicy-TLS13-1-2-Res-PQ-2025-09"
	albHSTS      = "max-age=31536000; includeSubDomains; preload"
)

// newApp wires an ECS Fargate service (rootless, read-only FS) behind an ALB with a /readyz check.
func newApp(
	ctx *pulumi.Context,
	name string,
	net *network,
	db *postgres,
	cache *redis,
	cfg appConfig,
	opts ...pulumi.ResourceOption,
) (*app, error) {
	albSG, taskSG, err := newAppSecurityGroups(ctx, name, net, cfg.Port, opts...)
	if err != nil {
		return nil, err
	}

	front, err := newLoadBalancer(ctx, name, net, albSG, cfg.Port, cfg.CertificateARN, opts...)
	if err != nil {
		return nil, err
	}
	if err = newAppAlias(ctx, name, cfg.Domain, cfg.HostedZoneID, front, opts...); err != nil {
		return nil, err
	}

	taskResources, err := newAppTaskResources(ctx, name, db, cache, opts...)
	if err != nil {
		return nil, err
	}

	taskDef, err := newTaskDefinition(
		ctx, name,
		taskResources.executionRole,
		taskResources.secretPolicy,
		taskResources.dsnSecret,
		taskResources.redisSecret,
		cfg,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	svc, err := newService(ctx, name, net, taskSG, taskDef, front, cfg, opts...)
	if err != nil {
		return nil, err
	}

	return &app{
		Service: svc,
		URL:     pulumi.String("https://" + cfg.Domain).ToStringOutput(),
		ALBZone: front.lb.ZoneId,
		ALBDNS:  front.lb.DnsName,
	}, nil
}

func newAppTaskResources(
	ctx *pulumi.Context,
	name string,
	db *postgres,
	cache *redis,
	opts ...pulumi.ResourceOption,
) (*appTaskResources, error) {
	executionRole, err := newExecutionRole(ctx, name, opts...)
	if err != nil {
		return nil, err
	}
	dsnSecret, err := newSecret(ctx, name+"-dsn", db.DSN, opts...)
	if err != nil {
		return nil, err
	}
	redisSecret, err := newSecret(ctx, name+"-redis", cache.Address, opts...)
	if err != nil {
		return nil, err
	}
	secretPolicy, err := grantSecretRead(
		ctx, name, executionRole.Role, dsnSecret.Secret, redisSecret.Secret, opts...,
	)
	if err != nil {
		return nil, err
	}
	return &appTaskResources{
		executionRole: executionRole,
		secretPolicy:  secretPolicy,
		dsnSecret:     dsnSecret,
		redisSecret:   redisSecret,
	}, nil
}

// frontend bundles the ALB plus its target group + listener.
type frontend struct {
	lb          *lb.LoadBalancer
	targetGroup *lb.TargetGroup
	listener    *lb.Listener
}

// newLoadBalancer builds an internet-facing ALB forwarding to an IP target group with a /readyz check.
func newLoadBalancer(
	ctx *pulumi.Context,
	name string,
	net *network,
	albSG *ec2.SecurityGroup,
	port int,
	certificateARN string,
	opts ...pulumi.ResourceOption,
) (*frontend, error) {
	alb, err := lb.NewLoadBalancer(ctx, name+"-alb", &lb.LoadBalancerArgs{
		LoadBalancerType: pulumi.String("application"),
		Internal:         pulumi.Bool(false),
		SecurityGroups:   pulumi.StringArray{albSG.ID()},
		Subnets:          net.publicSubnetIDs(),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-alb")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create alb %s: %w", name, err)
	}

	tg, err := lb.NewTargetGroup(ctx, name+"-tg", &lb.TargetGroupArgs{
		Port:       pulumi.Int(port),
		Protocol:   pulumi.String("HTTP"),
		TargetType: pulumi.String("ip"),
		VpcId:      net.VPC.ID(),
		HealthCheck: &lb.TargetGroupHealthCheckArgs{
			Enabled:  pulumi.Bool(true),
			Path:     pulumi.String("/readyz"),
			Protocol: pulumi.String("HTTP"),
			Matcher:  pulumi.String("200"),
		},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-tg")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create target group %s: %w", name, err)
	}

	listener, err := lb.NewListener(ctx, name+"-listener", &lb.ListenerArgs{
		LoadBalancerArn: alb.Arn,
		Port:            pulumi.Int(443),
		Protocol:        pulumi.String("HTTPS"),
		CertificateArn:  pulumi.String(certificateARN),
		SslPolicy:       pulumi.String(albTLSPolicy),
		RoutingHttpResponseStrictTransportSecurityHeaderValue: pulumi.String(albHSTS),
		DefaultActions: lb.ListenerDefaultActionArray{lb.ListenerDefaultActionArgs{
			Type:           pulumi.String("forward"),
			TargetGroupArn: tg.Arn,
		}},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create listener %s: %w", name, err)
	}
	return &frontend{lb: alb, targetGroup: tg, listener: listener}, nil
}

func newAppAlias(
	ctx *pulumi.Context,
	name, domain, hostedZoneID string,
	front *frontend,
	opts ...pulumi.ResourceOption,
) error {
	_, err := route53.NewRecord(ctx, name+"-alias", &route53.RecordArgs{
		ZoneId: pulumi.String(hostedZoneID),
		Name:   pulumi.String(domain),
		Type:   pulumi.String("A"),
		Aliases: route53.RecordAliasArray{route53.RecordAliasArgs{
			Name:                 front.lb.DnsName,
			ZoneId:               front.lb.ZoneId,
			EvaluateTargetHealth: pulumi.Bool(true),
		}},
	}, opts...)
	if err != nil {
		return fmt.Errorf("pulumi: create app alias %s: %w", domain, err)
	}
	return nil
}

// newService runs the task definition on Fargate in private subnets, registered with the ALB.
func newService(
	ctx *pulumi.Context,
	name string,
	net *network,
	taskSG *ec2.SecurityGroup,
	taskDef *ecs.TaskDefinition,
	front *frontend,
	cfg appConfig,
	opts ...pulumi.ResourceOption,
) (*ecs.Service, error) {
	cluster, err := ecs.NewCluster(ctx, name+"-cluster", &ecs.ClusterArgs{
		Name: pulumi.String(name + "-cluster"),
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-cluster")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create ecs cluster %s: %w", name, err)
	}

	serviceOpts := make([]pulumi.ResourceOption, 0, len(opts)+1)
	serviceOpts = append(serviceOpts, opts...)
	serviceOpts = append(serviceOpts, pulumi.DependsOn([]pulumi.Resource{front.listener}))
	svc, err := ecs.NewService(ctx, name+"-svc", &ecs.ServiceArgs{
		Cluster:        cluster.Arn,
		TaskDefinition: taskDef.Arn,
		DesiredCount:   pulumi.Int(cfg.Replicas),
		LaunchType:     pulumi.String("FARGATE"),
		NetworkConfiguration: &ecs.ServiceNetworkConfigurationArgs{
			Subnets:        net.privateSubnetIDs(),
			SecurityGroups: pulumi.StringArray{taskSG.ID()},
			AssignPublicIp: pulumi.Bool(false),
		},
		LoadBalancers: ecs.ServiceLoadBalancerArray{ecs.ServiceLoadBalancerArgs{
			TargetGroupArn: front.targetGroup.Arn,
			ContainerName:  pulumi.String(name),
			ContainerPort:  pulumi.Int(cfg.Port),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-svc")},
	}, serviceOpts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create ecs service %s: %w", name, err)
	}
	return svc, nil
}

// newAppSecurityGroups returns (alb-sg open to internet on :443, task-sg open to the alb on app port).
func newAppSecurityGroups(
	ctx *pulumi.Context,
	name string,
	net *network,
	port int,
	opts ...pulumi.ResourceOption,
) (*ec2.SecurityGroup, *ec2.SecurityGroup, error) {
	albSG, err := ec2.NewSecurityGroup(ctx, name+"-alb-sg", &ec2.SecurityGroupArgs{
		VpcId:       net.VPC.ID(),
		Description: pulumi.String("golusoris alb ingress from internet"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:   pulumi.String("tcp"),
			FromPort:   pulumi.Int(443),
			ToPort:     pulumi.Int(443),
			CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
		}},
		Egress: ec2.SecurityGroupEgressArray{allowAllEgress()},
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-alb-sg")},
	}, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create alb security group %s: %w", name, err)
	}

	taskSG, err := ec2.NewSecurityGroup(ctx, name+"-task-sg", &ec2.SecurityGroupArgs{
		VpcId:       net.VPC.ID(),
		Description: pulumi.String("golusoris task ingress from alb"),
		Ingress: ec2.SecurityGroupIngressArray{ec2.SecurityGroupIngressArgs{
			Protocol:       pulumi.String("tcp"),
			FromPort:       pulumi.Int(port),
			ToPort:         pulumi.Int(port),
			SecurityGroups: pulumi.StringArray{albSG.ID()},
		}},
		Egress: ec2.SecurityGroupEgressArray{allowAllEgress()},
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-task-sg")},
	}, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("pulumi: create task security group %s: %w", name, err)
	}
	return albSG, taskSG, nil
}

// allowAllEgress is the shared "any outbound" rule for app-tier security groups.
func allowAllEgress() ec2.SecurityGroupEgressArgs {
	return ec2.SecurityGroupEgressArgs{
		Protocol:   pulumi.String("-1"),
		FromPort:   pulumi.Int(0),
		ToPort:     pulumi.Int(0),
		CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
	}
}

// newSecret stores a connection string in Secrets Manager so the task reads it as an ECS secret.
func newSecret(
	ctx *pulumi.Context,
	name string,
	value pulumi.StringInput,
	opts ...pulumi.ResourceOption,
) (*appSecret, error) {
	secret, err := secretsmanager.NewSecret(ctx, name, &secretsmanager.SecretArgs{
		NamePrefix:           pulumi.String(name + "-"),
		RecoveryWindowInDays: pulumi.Int(0),
		Tags:                 pulumi.StringMap{"Name": pulumi.String(name)},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secret %s: %w", name, err)
	}
	version, err := secretsmanager.NewSecretVersion(ctx, name+"-v", &secretsmanager.SecretVersionArgs{
		SecretId:     secret.ID(),
		SecretString: value,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create secret version %s: %w", name, err)
	}
	return &appSecret{Secret: secret, Version: version}, nil
}

// newExecutionRole creates the ECS task execution role (image pull + log write).
func newExecutionRole(
	ctx *pulumi.Context,
	name string,
	opts ...pulumi.ResourceOption,
) (*appExecutionRole, error) {
	role, err := iam.NewRole(ctx, name+"-exec-role", &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(ecsAssumeRolePolicy),
		Tags:             pulumi.StringMap{"Name": pulumi.String(name + "-exec-role")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create execution role %s: %w", name, err)
	}
	managedPolicy, err := iam.NewRolePolicyAttachment(ctx, name+"-exec-attach", &iam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"),
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: attach execution policy %s: %w", name, err)
	}
	return &appExecutionRole{Role: role, ManagedPolicy: managedPolicy}, nil
}

// grantSecretRead lets the execution role read the DSN + Redis secrets at task start.
func grantSecretRead(
	ctx *pulumi.Context,
	name string,
	role *iam.Role,
	dsn, redis *secretsmanager.Secret,
	opts ...pulumi.ResourceOption,
) (*iam.RolePolicy, error) {
	policy := pulumi.All(dsn.Arn, redis.Arn).ApplyT(func(arns []any) (string, error) {
		return secretReadPolicy(arns[0].(string), arns[1].(string))
	}).(pulumi.StringOutput)

	policyResource, err := iam.NewRolePolicy(ctx, name+"-secret-read", &iam.RolePolicyArgs{
		Role:   role.ID(),
		Policy: policy,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: attach secret-read policy %s: %w", name, err)
	}
	return policyResource, nil
}

// secretReadPolicy renders the inline IAM policy granting GetSecretValue on the two secret ARNs.
func secretReadPolicy(dsnArn, redisArn string) (string, error) {
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":   "Allow",
			"Action":   []string{"secretsmanager:GetSecretValue"},
			"Resource": []string{dsnArn, redisArn},
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("pulumi: marshal secret-read policy: %w", err)
	}
	return string(b), nil
}

// newTaskDefinition builds the Fargate task: rootless, read-only FS, secrets injected as env.
func newTaskDefinition(
	ctx *pulumi.Context,
	name string,
	execRole *appExecutionRole,
	secretPolicy *iam.RolePolicy,
	dsn, redis *appSecret,
	cfg appConfig,
	opts ...pulumi.ResourceOption,
) (*ecs.TaskDefinition, error) {
	logGroup, err := cloudwatch.NewLogGroup(ctx, name+"-logs", &cloudwatch.LogGroupArgs{
		NamePrefix:      pulumi.String("/ecs/" + name + "-"),
		RetentionInDays: pulumi.Int(30),
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create log group %s: %w", name, err)
	}

	containers := containerDefinitions(name, cfg, dsn.Secret, redis.Secret, logGroup)

	taskDef, err := ecs.NewTaskDefinition(ctx, name+"-task", &ecs.TaskDefinitionArgs{
		Family:                  pulumi.String(name),
		Cpu:                     pulumi.String(taskCPU),
		Memory:                  pulumi.String(taskMemory),
		NetworkMode:             pulumi.String("awsvpc"),
		RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
		ExecutionRoleArn:        execRole.Role.Arn,
		RuntimePlatform: &ecs.TaskDefinitionRuntimePlatformArgs{
			OperatingSystemFamily: pulumi.String("LINUX"),
			CpuArchitecture:       pulumi.String("ARM64"),
		},
		ContainerDefinitions: containers,
		Tags:                 pulumi.StringMap{"Name": pulumi.String(name + "-task")},
	}, append(opts, pulumi.DependsOn([]pulumi.Resource{
		dsn.Version,
		redis.Version,
		execRole.ManagedPolicy,
		secretPolicy,
	}))...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create task definition %s: %w", name, err)
	}
	return taskDef, nil
}

// containerDefinitions renders the single-container JSON ECS expects, injecting secrets by ARN.
func containerDefinitions(
	name string,
	cfg appConfig,
	dsn, redis *secretsmanager.Secret,
	logGroup *cloudwatch.LogGroup,
) pulumi.StringOutput {
	return pulumi.All(dsn.Arn, redis.Arn, logGroup.Name).ApplyT(
		func(v []any) (string, error) {
			return renderContainer(name, cfg, v[0].(string), v[1].(string), v[2].(string))
		},
	).(pulumi.StringOutput)
}

// renderContainer marshals one rootless, read-only container def with health-check + secret env refs.
func renderContainer(name string, cfg appConfig, dsnArn, redisArn, logGroup string) (string, error) {
	def := []map[string]any{{
		"name":                   name,
		"image":                  cfg.Image,
		"essential":              true,
		"readonlyRootFilesystem": true,
		"user":                   "65534",
		"portMappings":           []map[string]any{{"containerPort": cfg.Port, "protocol": "tcp"}},
		"environment": []map[string]any{
			{"name": "APP_HTTP_ADDR", "value": fmt.Sprintf(":%d", cfg.Port)},
			{"name": "APP_CACHE_REDIS_TLS", "value": "true"},
		},
		"secrets": []map[string]any{
			{"name": "APP_DB_DSN", "valueFrom": dsnArn},
			{"name": "APP_CACHE_REDIS_ADDR", "valueFrom": redisArn},
		},
		"logConfiguration": map[string]any{
			"logDriver": "awslogs",
			"options": map[string]string{
				"awslogs-group":         logGroup,
				"awslogs-region":        cfg.Region,
				"awslogs-stream-prefix": "app",
			},
		},
	}}
	b, err := json.Marshal(def)
	if err != nil {
		return "", fmt.Errorf("pulumi: marshal container definitions %s: %w", name, err)
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
