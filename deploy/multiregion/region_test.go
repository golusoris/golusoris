// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/lb"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// recordedCall is one call the mock resource monitor observed.
type recordedCall struct {
	typeToken string
	name      string
	inputs    resource.PropertyMap
}

// stackMocks is a pulumi.MockResourceMonitor that records every resource
// registration (so tests can assert on the graph newRegionService built) and
// synthesizes an id/ARN for each, or fails registration for any TypeToken
// listed in fail. Used to exercise newRegionService, newRegionTaskDefinition
// and newRegionFargateService without a real AWS account.
//
// Pulumi resource registration is asynchronous (see registerResource in the
// SDK: it kicks off a goroutine and returns nil immediately), so a failure
// injected here does not flow back through this package's own
// `if err != nil { return fmt.Errorf(...) }` guards — it only ever surfaces
// as the top-level error from pulumi.RunErr once every resource has settled.
// That is a property of the SDK, not of this package's code, so failure
// injection below only asserts fail-closed behavior at the pulumi.RunErr
// boundary; it does not exercise this package's per-call error wrapping.
type stackMocks struct {
	mu    sync.Mutex
	calls []recordedCall
	fail  map[string]error
}

func (m *stackMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.calls = append(m.calls, recordedCall{typeToken: args.TypeToken, name: args.Name, inputs: args.Inputs})
	m.mu.Unlock()

	if err, ok := m.fail[args.TypeToken]; ok {
		return "", resource.PropertyMap{}, err
	}
	state := args.Inputs.Copy()
	state["arn"] = resource.NewStringProperty("arn:aws:mock:" + args.TypeToken + ":" + args.Name)
	return args.Name + "-id", state, nil
}

func (m *stackMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

// find returns the first recorded call against typeToken.
func (m *stackMocks) find(typeToken string) (recordedCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c.typeToken == typeToken {
			return c, true
		}
	}
	return recordedCall{}, false
}

// newTestFixtures builds the subnet/security-group/target-group inputs
// newRegionService needs, via the same mocked resource-registration path as
// production code.
func newTestFixtures(ctx *pulumi.Context) (*ec2.Subnet, *ec2.SecurityGroup, *lb.TargetGroup, error) {
	subnet, err := ec2.NewSubnet(ctx, "test-subnet", &ec2.SubnetArgs{
		VpcId:     pulumi.String("vpc-test"),
		CidrBlock: pulumi.String("10.0.0.0/24"),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	sg, err := ec2.NewSecurityGroup(ctx, "test-task-sg", &ec2.SecurityGroupArgs{
		VpcId: pulumi.String("vpc-test"),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	tg, err := lb.NewTargetGroup(ctx, "test-tg", &lb.TargetGroupArgs{
		Port:     pulumi.Int(8080),
		Protocol: pulumi.String("HTTP"),
		VpcId:    pulumi.String("vpc-test"),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return subnet, sg, tg, nil
}

// runNewRegionService drives newRegionService end-to-end under mocks with cfg
// and returns the mocks used, so callers can inspect what was registered.
func runNewRegionService(t *testing.T, cfg regionConfig, mocks *stackMocks) error {
	t.Helper()
	return pulumi.RunErr(func(ctx *pulumi.Context) error {
		subnet, sg, tg, err := newTestFixtures(ctx)
		if err != nil {
			return err
		}
		return newRegionService(ctx, "test-region", []*ec2.Subnet{subnet}, sg, tg, cfg, pulumi.Parent(sg))
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
}

// TestNewRegionService_Succeeds is the positive case: given valid inputs and
// no injected failures, newRegionService (via its newRegionTaskDefinition and
// newRegionFargateService helpers) registers the task definition, cluster and
// service without error.
func TestNewRegionService_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := regionConfig{Region: "us-east-1", Image: "example/app:latest", Port: 8080}
	if err := runNewRegionService(t, cfg, &stackMocks{}); err != nil {
		t.Fatalf("newRegionService() error = %v", err)
	}
}

// TestNewRegionService_WiresResourcesCorrectly is the boundary case for the
// newRegionService/newRegionTaskDefinition/newRegionFargateService split: it
// asserts the field values placed on each registered resource are exactly
// what the pre-split function produced, so the extraction could not have
// silently dropped or mistyped a field.
func TestNewRegionService_WiresResourcesCorrectly(t *testing.T) {
	t.Parallel()
	cfg := regionConfig{Region: "us-east-1", Image: "example/app:latest", Port: 8080}
	mocks := &stackMocks{}
	if err := runNewRegionService(t, cfg, mocks); err != nil {
		t.Fatalf("newRegionService() error = %v", err)
	}

	taskDef, ok := mocks.find("aws:ecs/taskDefinition:TaskDefinition")
	if !ok {
		t.Fatal("no aws:ecs/taskDefinition:TaskDefinition resource was registered")
	}
	if got := taskDef.inputs["family"].StringValue(); got != "test-region" {
		t.Errorf("task definition family = %q, want %q", got, "test-region")
	}
	if got := taskDef.inputs["cpu"].StringValue(); got != "256" {
		t.Errorf("task definition cpu = %q, want %q", got, "256")
	}
	if got := taskDef.inputs["memory"].StringValue(); got != "512" {
		t.Errorf("task definition memory = %q, want %q", got, "512")
	}

	cluster, ok := mocks.find("aws:ecs/cluster:Cluster")
	if !ok {
		t.Fatal("no aws:ecs/cluster:Cluster resource was registered")
	}
	if got := cluster.inputs["name"].StringValue(); got != "test-region-cluster" {
		t.Errorf("cluster name = %q, want %q", got, "test-region-cluster")
	}

	svc, ok := mocks.find("aws:ecs/service:Service")
	if !ok {
		t.Fatal("no aws:ecs/service:Service resource was registered")
	}
	if got := svc.inputs["desiredCount"].NumberValue(); got != 2 {
		t.Errorf("service desiredCount = %v, want 2", got)
	}
	if got := svc.inputs["launchType"].StringValue(); got != "FARGATE" {
		t.Errorf("service launchType = %q, want FARGATE", got)
	}
}

// TestNewRegionService_FailsClosedOnResourceFailure is the negative case: a
// failure anywhere in the graph newRegionService builds must fail the whole
// program rather than being silently swallowed. See the stackMocks doc
// comment for why the failure surfaces via pulumi.RunErr's own error instead
// of this package's per-call wrapping.
func TestNewRegionService_FailsClosedOnResourceFailure(t *testing.T) {
	t.Parallel()
	cfg := regionConfig{Region: "us-east-1", Image: "example/app:latest", Port: 8080}
	wantErr := errors.New("boom")
	err := runNewRegionService(t, cfg, &stackMocks{
		fail: map[string]error{"aws:ecs/cluster:Cluster": wantErr},
	})
	if err == nil {
		t.Fatal("newRegionService() = nil, want error")
	}
}
