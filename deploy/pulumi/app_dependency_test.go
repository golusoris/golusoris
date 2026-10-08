// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ecs"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type dependencyMocks struct {
	mu       sync.Mutex
	taskDeps []string
	svcDeps  []string
	calls    []dependencyCall
}

type dependencyCall struct {
	typeToken string
	name      string
	inputs    resource.PropertyMap
}

func (m *dependencyMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.calls = append(m.calls, dependencyCall{
		typeToken: args.TypeToken,
		name:      args.Name,
		inputs:    args.Inputs,
	})
	m.mu.Unlock()
	state := args.Inputs.Copy()
	state["arn"] = resource.NewStringProperty("arn:aws:mock:" + args.TypeToken + ":" + args.Name)
	state["name"] = resource.NewStringProperty(args.Name)
	if args.TypeToken == "aws:ecs/taskDefinition:TaskDefinition" {
		m.mu.Lock()
		m.taskDeps = append([]string(nil), args.RegisterRPC.GetDependencies()...)
		m.mu.Unlock()
	}
	if args.TypeToken == "aws:ecs/service:Service" {
		m.mu.Lock()
		m.svcDeps = append([]string(nil), args.RegisterRPC.GetDependencies()...)
		m.mu.Unlock()
	}
	return args.Name + "-id", state, nil
}

func (m *dependencyMocks) find(typeToken string) (dependencyCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, call := range m.calls {
		if call.typeToken == typeToken {
			return call, true
		}
	}
	return dependencyCall{}, false
}

func (m *dependencyMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func (m *dependencyMocks) dependencies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.taskDeps...)
}

func (m *dependencyMocks) serviceDependencies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.svcDeps...)
}

func TestTaskDefinitionDependsOnSecretValues(t *testing.T) {
	t.Parallel()
	mocks := &dependencyMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		role, err := newExecutionRole(ctx, "test")
		if err != nil {
			return err
		}
		dsn, err := newSecret(ctx, "test-dsn", pulumi.String("postgres://db"))
		if err != nil {
			return err
		}
		redis, err := newSecret(ctx, "test-redis", pulumi.String("redis://cache"))
		if err != nil {
			return err
		}
		secretPolicy, err := grantSecretRead(ctx, "test", role.Role, dsn.Secret, redis.Secret)
		if err != nil {
			return err
		}
		_, err = newTaskDefinition(ctx, "test", role, secretPolicy, dsn, redis, appConfig{
			Image:  "example.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Port:   8080,
			Region: "us-east-1",
		})
		return err
	}, pulumi.WithMocks("golusoris-pulumi-test", "test", mocks))
	if err != nil {
		t.Fatalf("newTaskDefinition() error = %v", err)
	}

	dependencies := strings.Join(mocks.dependencies(), "\n")
	for _, name := range []string{"test-dsn-v", "test-redis-v"} {
		if !strings.Contains(dependencies, "secretVersion:SecretVersion::"+name) {
			t.Errorf("task dependencies do not contain %s secret version: %q", name, dependencies)
		}
	}
	for _, dependency := range []string{
		"aws:iam/rolePolicyAttachment:RolePolicyAttachment::test-exec-attach",
		"aws:iam/rolePolicy:RolePolicy::test-secret-read",
	} {
		if !strings.Contains(dependencies, dependency) {
			t.Errorf("task dependencies do not contain %s: %q", dependency, dependencies)
		}
	}
}

func TestServiceDependsOnLoadBalancerListener(t *testing.T) {
	t.Parallel()
	mocks := &dependencyMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		vpc, err := ec2.NewVpc(ctx, "test-vpc", &ec2.VpcArgs{
			CidrBlock: pulumi.String("10.0.0.0/16"),
		})
		if err != nil {
			return err
		}
		publicSubnet, err := ec2.NewSubnet(ctx, "test-public", &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.0.0/24"),
		})
		if err != nil {
			return err
		}
		privateSubnet, err := ec2.NewSubnet(ctx, "test-private", &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.10.0/24"),
		})
		if err != nil {
			return err
		}
		network := &network{
			VPC:            vpc,
			PublicSubnets:  []*ec2.Subnet{publicSubnet},
			PrivateSubnets: []*ec2.Subnet{privateSubnet},
		}
		albSG, taskSG, err := newAppSecurityGroups(ctx, "test", network, 8080)
		if err != nil {
			return err
		}
		front, err := newLoadBalancer(
			ctx,
			"test",
			network,
			albSG,
			8080,
			"arn:aws:acm:us-east-1:123456789012:certificate/test",
		)
		if err != nil {
			return err
		}
		taskDef, err := ecs.NewTaskDefinition(ctx, "test-task", &ecs.TaskDefinitionArgs{
			Family:                  pulumi.String("test"),
			Cpu:                     pulumi.String("256"),
			Memory:                  pulumi.String("512"),
			NetworkMode:             pulumi.String("awsvpc"),
			RequiresCompatibilities: pulumi.StringArray{pulumi.String("FARGATE")},
			ContainerDefinitions:    pulumi.String(`[{"name":"test","image":"example.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`),
		})
		if err != nil {
			return err
		}
		_, err = newService(ctx, "test", network, taskSG, taskDef, front, appConfig{
			Replicas: 1,
			Port:     8080,
		})
		return err
	}, pulumi.WithMocks("golusoris-pulumi-test", "test", mocks))
	if err != nil {
		t.Fatalf("newService() error = %v", err)
	}

	dependencies := strings.Join(mocks.serviceDependencies(), "\n")
	if !strings.Contains(dependencies, "aws:lb/listener:Listener::test-listener") {
		t.Errorf("service dependencies do not contain ALB listener: %q", dependencies)
	}
}

func TestPublicLoadBalancerTerminatesTLS(t *testing.T) {
	t.Parallel()
	const certificateARN = "arn:aws:acm:us-east-1:123456789012:certificate/test"
	mocks := &dependencyMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		vpc, err := ec2.NewVpc(ctx, "test-vpc", &ec2.VpcArgs{
			CidrBlock: pulumi.String("10.0.0.0/16"),
		})
		if err != nil {
			return err
		}
		subnet, err := ec2.NewSubnet(ctx, "test-public", &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.0.0/24"),
		})
		if err != nil {
			return err
		}
		network := &network{VPC: vpc, PublicSubnets: []*ec2.Subnet{subnet}}
		albSG, _, err := newAppSecurityGroups(ctx, "test", network, 8080)
		if err != nil {
			return err
		}
		front, err := newLoadBalancer(ctx, "test", network, albSG, 8080, certificateARN)
		if err != nil {
			return err
		}
		return newAppAlias(ctx, "test", "app.example.com", "ZCALLER", front)
	}, pulumi.WithMocks("golusoris-pulumi-test", "test", mocks))
	if err != nil {
		t.Fatalf("newLoadBalancer() error = %v", err)
	}

	assertTLSListener(t, mocks, certificateARN)
	assertALBIngress(t, mocks)
	assertPrivateTargetHealth(t, mocks)
	assertAppAlias(t, mocks)
}

func assertTLSListener(t *testing.T, mocks *dependencyMocks, certificateARN string) {
	t.Helper()
	listener, ok := mocks.find("aws:lb/listener:Listener")
	if !ok {
		t.Fatal("no ALB listener registered")
	}
	if got := listener.inputs["protocol"].StringValue(); got != "HTTPS" {
		t.Errorf("listener protocol = %q, want HTTPS", got)
	}
	if got := listener.inputs["port"].NumberValue(); got != 443 {
		t.Errorf("listener port = %v, want 443", got)
	}
	if certificate, exists := listener.inputs["certificateArn"]; !exists || certificate.StringValue() != certificateARN {
		t.Errorf("listener certificate ARN = %v, want %q", certificate, certificateARN)
	}
	if policy, exists := listener.inputs["sslPolicy"]; !exists || policy.StringValue() != "ELBSecurityPolicy-TLS13-1-2-Res-PQ-2025-09" {
		t.Errorf("listener SSL policy = %v", policy)
	}
}

func assertALBIngress(t *testing.T, mocks *dependencyMocks) {
	t.Helper()
	albSG, ok := mocks.find("aws:ec2/securityGroup:SecurityGroup")
	if !ok {
		t.Fatal("no ALB security group registered")
	}
	ingress := albSG.inputs["ingress"].ArrayValue()
	if len(ingress) != 1 ||
		ingress[0].ObjectValue()["fromPort"].NumberValue() != 443 ||
		ingress[0].ObjectValue()["toPort"].NumberValue() != 443 {
		t.Errorf("ALB ingress = %v, want only TCP/443", ingress)
	}
}

func assertPrivateTargetHealth(t *testing.T, mocks *dependencyMocks) {
	t.Helper()
	targetGroup, ok := mocks.find("aws:lb/targetGroup:TargetGroup")
	if !ok {
		t.Fatal("no ALB target group registered")
	}
	if got := targetGroup.inputs["protocol"].StringValue(); got != "HTTP" {
		t.Errorf("target group protocol = %q, want HTTP", got)
	}
	health := targetGroup.inputs["healthCheck"].ObjectValue()
	if health["protocol"].StringValue() != "HTTP" || health["path"].StringValue() != "/readyz" {
		t.Errorf("target group health check = %v, want HTTP /readyz", health)
	}
}

func assertAppAlias(t *testing.T, mocks *dependencyMocks) {
	t.Helper()
	record, ok := mocks.find("aws:route53/record:Record")
	if !ok {
		t.Fatal("no Route53 alias registered")
	}
	if got := record.inputs["zoneId"].StringValue(); got != "ZCALLER" {
		t.Errorf("record zoneId = %q, want caller-owned ZCALLER", got)
	}
	aliases := record.inputs["aliases"].ArrayValue()
	if len(aliases) != 1 || !aliases[0].ObjectValue()["evaluateTargetHealth"].BoolValue() {
		t.Errorf("record aliases = %v, want EvaluateTargetHealth=true", aliases)
	}
}
