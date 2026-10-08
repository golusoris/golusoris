// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
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
	deps      []string
	provider  string
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

const testAppImage = "example.invalid/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func (m *stackMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	m.calls = append(m.calls, recordedCall{
		typeToken: args.TypeToken,
		name:      args.Name,
		inputs:    args.Inputs,
		deps:      append([]string(nil), args.RegisterRPC.GetDependencies()...),
		provider:  args.Provider,
	})
	m.mu.Unlock()

	if err, ok := m.fail[args.TypeToken]; ok {
		return "", resource.PropertyMap{}, err
	}
	state := args.Inputs.Copy()
	state["arn"] = resource.NewStringProperty("arn:aws:mock:" + args.TypeToken + ":" + args.Name)
	if args.TypeToken == "aws:rds/subnetGroup:SubnetGroup" {
		state["name"] = resource.NewStringProperty(args.Name)
	}
	if args.TypeToken == "aws:rds/cluster:Cluster" {
		state["endpoint"] = resource.NewStringProperty(args.Name + ".example.invalid")
	}
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

// findAll returns every recorded call against typeToken.
func (m *stackMocks) findAll(typeToken string) []recordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := make([]recordedCall, 0, len(m.calls))
	for _, call := range m.calls {
		if call.typeToken == typeToken {
			calls = append(calls, call)
		}
	}
	return calls
}

// newTestFixtures builds the subnet/security-group/target-group inputs
// newRegionService needs, via the same mocked resource-registration path as
// production code.
func newTestFixtures(ctx *pulumi.Context) (*ec2.Subnet, *ec2.SecurityGroup, *regionFrontend, error) {
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
	alb, err := lb.NewLoadBalancer(ctx, "test-alb", &lb.LoadBalancerArgs{
		Subnets:        pulumi.StringArray{subnet.ID()},
		SecurityGroups: pulumi.StringArray{sg.ID()},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	listener, err := lb.NewListener(ctx, "test-listener", &lb.ListenerArgs{
		LoadBalancerArn: alb.Arn,
		Port:            pulumi.Int(443),
		Protocol:        pulumi.String("HTTPS"),
		CertificateArn:  pulumi.String("arn:aws:acm:us-east-1:123456789012:certificate/test"),
		SslPolicy:       pulumi.String("ELBSecurityPolicy-TLS13-1-2-Res-PQ-2025-09"),
		DefaultActions: lb.ListenerDefaultActionArray{lb.ListenerDefaultActionArgs{
			Type:           pulumi.String("forward"),
			TargetGroupArn: tg.Arn,
		}},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return subnet, sg, &regionFrontend{LoadBalancer: alb, TargetGroup: tg, Listener: listener}, nil
}

// runNewRegionService drives newRegionService end-to-end under mocks with cfg
// and returns the mocks used, so callers can inspect what was registered.
func runNewRegionService(t *testing.T, cfg regionConfig, mocks *stackMocks) error {
	t.Helper()
	return pulumi.RunErr(func(ctx *pulumi.Context) error {
		subnet, sg, front, err := newTestFixtures(ctx)
		if err != nil {
			return err
		}
		return newRegionService(ctx, "test-region", []*ec2.Subnet{subnet}, sg, front, cfg, pulumi.Parent(sg))
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
}

func testRegionConfig() regionConfig {
	return regionConfig{
		Region: "us-east-1",
		Image:  testAppImage,
		Port:   8080,
		DBDSN:  pulumi.String("postgres://appuser:secret@db:5432/app?sslmode=require"),
	}
}

func TestRegionNetworkSeparatesPublicAndPrivateSubnets(t *testing.T) {
	t.Parallel()
	mocks := &stackMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "primary", &aws.ProviderArgs{
			Region: pulumi.String("us-east-1"),
		})
		if err != nil {
			return err
		}
		network, err := newRegionNetwork(ctx, "test", "us-east-1", pulumi.Provider(provider))
		if err != nil {
			return err
		}
		if len(network.PublicSubnets) != 2 || len(network.PrivateSubnets) != 2 {
			return errors.New("network does not expose two public and two private subnets")
		}
		return nil
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
	if err != nil {
		t.Fatalf("newRegionNetwork() error = %v", err)
	}

	for _, subnet := range mocks.findAll("aws:ec2/subnet:Subnet") {
		isPrivate := strings.Contains(subnet.name, "private")
		if got := subnet.inputs["mapPublicIpOnLaunch"].BoolValue(); got == isPrivate {
			t.Errorf("subnet %s mapPublicIpOnLaunch = %v, private = %v", subnet.name, got, isPrivate)
		}
	}
	for _, routeTable := range mocks.findAll("aws:ec2/routeTable:RouteTable") {
		routeCount := 0
		if routes, ok := routeTable.inputs["routes"]; ok {
			routeCount = len(routes.ArrayValue())
		}
		if strings.Contains(routeTable.name, "private") && routeCount != 0 {
			t.Errorf("private route table %s has %d internet routes", routeTable.name, routeCount)
		}
		if strings.Contains(routeTable.name, "public") && routeCount != 1 {
			t.Errorf("public route table %s has %d routes, want 1", routeTable.name, routeCount)
		}
	}
}

func TestRegionFrontendTerminatesTLS(t *testing.T) {
	t.Parallel()
	const certificateARN = "arn:aws:acm:us-east-1:123456789012:certificate/primary"
	mocks := &stackMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "primary", &aws.ProviderArgs{
			Region: pulumi.String("us-east-1"),
		})
		if err != nil {
			return err
		}
		opt := pulumi.Provider(provider)
		vpc, err := ec2.NewVpc(ctx, "test-vpc", &ec2.VpcArgs{
			CidrBlock: pulumi.String("10.0.0.0/16"),
		}, opt)
		if err != nil {
			return err
		}
		subnet, err := ec2.NewSubnet(ctx, "test-public", &ec2.SubnetArgs{
			VpcId:     vpc.ID(),
			CidrBlock: pulumi.String("10.0.0.0/24"),
		}, opt)
		if err != nil {
			return err
		}
		albSG, _, err := newRegionSecurityGroups(ctx, "test", vpc, 8080, opt)
		if err != nil {
			return err
		}
		_, err = newRegionFrontend(
			ctx, "test", vpc, []*ec2.Subnet{subnet}, albSG, 8080, certificateARN, opt,
		)
		return err
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
	if err != nil {
		t.Fatalf("newRegionFrontend() error = %v", err)
	}

	assertRegionTLSListener(t, mocks, certificateARN)
	assertRegionALBIngress(t, mocks)
	assertRegionPrivateTargetHealth(t, mocks)
}

func assertRegionTLSListener(t *testing.T, mocks *stackMocks, certificateARN string) {
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

func assertRegionALBIngress(t *testing.T, mocks *stackMocks) {
	t.Helper()
	securityGroups := mocks.findAll("aws:ec2/securityGroup:SecurityGroup")
	var albIngress []resource.PropertyValue
	for _, group := range securityGroups {
		if strings.HasSuffix(group.name, "-alb-sg") {
			albIngress = group.inputs["ingress"].ArrayValue()
			break
		}
	}
	if len(albIngress) != 1 ||
		albIngress[0].ObjectValue()["fromPort"].NumberValue() != 443 ||
		albIngress[0].ObjectValue()["toPort"].NumberValue() != 443 {
		t.Errorf("ALB ingress = %v, want only TCP/443", albIngress)
	}
}

func assertRegionPrivateTargetHealth(t *testing.T, mocks *stackMocks) {
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

func TestValidateStackConfigRequiresRegionalTLSAndDNSInputs(t *testing.T) {
	t.Parallel()
	valid := stackConfig{
		Domain:                  "app.example.com",
		HostedZoneID:            "ZCALLER",
		PrimaryCertificateARN:   "arn:aws:acm:us-east-1:123456789012:certificate/primary",
		SecondaryCertificateARN: "arn:aws:acm:us-west-2:123456789012:certificate/secondary",
		AppImage:                testAppImage,
	}
	if err := validateStackConfig(valid); err != nil {
		t.Fatalf("validateStackConfig(valid) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		field  string
		mutate func(*stackConfig)
	}{
		{name: "domain", field: "domain", mutate: func(cfg *stackConfig) { cfg.Domain = "" }},
		{name: "hosted zone", field: "hostedZoneId", mutate: func(cfg *stackConfig) { cfg.HostedZoneID = "" }},
		{name: "primary certificate", field: "primaryCertificateArn", mutate: func(cfg *stackConfig) { cfg.PrimaryCertificateARN = "" }},
		{name: "secondary certificate", field: "secondaryCertificateArn", mutate: func(cfg *stackConfig) { cfg.SecondaryCertificateARN = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			test.mutate(&cfg)
			err := validateStackConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validateStackConfig() error = %v, want required %s", err, test.field)
			}
		})
	}
}

// TestNewRegionService_Succeeds is the positive case: given valid inputs and
// no injected failures, newRegionService (via its newRegionTaskDefinition and
// newRegionFargateService helpers) registers the task definition, cluster and
// service without error.
func TestNewRegionService_Succeeds(t *testing.T) {
	t.Parallel()
	cfg := testRegionConfig()
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
	cfg := testRegionConfig()
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
	assertDatabaseSecret(t, taskDef)
	if deps := strings.Join(taskDef.deps, "\n"); !strings.Contains(deps, "secretVersion:SecretVersion::test-region-dsn-v") {
		t.Errorf("task dependencies do not contain database secret version: %q", deps)
	}
	if _, ok = mocks.find("aws:secretsmanager/secret:Secret"); !ok {
		t.Error("no regional database secret was registered")
	}
	policy, ok := mocks.find("aws:iam/rolePolicy:RolePolicy")
	if !ok {
		t.Fatal("no database secret-read policy was registered")
	}
	if got := policy.inputs["policy"].StringValue(); !strings.Contains(got, "secretsmanager:GetSecretValue") {
		t.Errorf("secret-read policy = %q, want GetSecretValue", got)
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

// TestNewRegionService_WaitsForExecutionPolicies protects the task-start IAM
// edge: the task definition waits for both execution-role policies, and the
// service waits for that task definition before ECS can launch a task.
func TestNewRegionService_WaitsForExecutionPolicies(t *testing.T) {
	t.Parallel()
	mocks := &stackMocks{}
	if err := runNewRegionService(t, testRegionConfig(), mocks); err != nil {
		t.Fatalf("newRegionService() error = %v", err)
	}

	taskDef, ok := mocks.find("aws:ecs/taskDefinition:TaskDefinition")
	if !ok {
		t.Fatal("no aws:ecs/taskDefinition:TaskDefinition resource was registered")
	}
	assertResourceDependency(t, taskDef, "aws:iam/rolePolicy:RolePolicy::test-region-secret-read")
	assertResourceDependency(t, taskDef, "aws:iam/rolePolicyAttachment:RolePolicyAttachment::test-region-exec-attach")

	service, ok := mocks.find("aws:ecs/service:Service")
	if !ok {
		t.Fatal("no aws:ecs/service:Service resource was registered")
	}
	assertResourceDependency(t, service, "aws:ecs/taskDefinition:TaskDefinition::test-region-task")
	assertResourceDependency(t, service, "aws:lb/listener:Listener::test-listener")
}

func assertResourceDependency(t *testing.T, call recordedCall, want string) {
	t.Helper()
	for _, dependency := range call.deps {
		if strings.Contains(dependency, want) {
			return
		}
	}
	t.Errorf("%s %q dependencies = %q, want %q", call.typeToken, call.name, call.deps, want)
}

func assertDatabaseSecret(t *testing.T, taskDef recordedCall) {
	t.Helper()
	var definitions []struct {
		Secrets []struct {
			Name      string `json:"name"`
			ValueFrom string `json:"valueFrom"` //nolint:tagliatelle // ECS JSON uses AWS's camelCase field.
		} `json:"secrets"`
	}
	rendered := taskDef.inputs["containerDefinitions"].StringValue()
	if err := json.Unmarshal([]byte(rendered), &definitions); err != nil {
		t.Fatalf("json.Unmarshal(containerDefinitions) error = %v", err)
	}
	if len(definitions) != 1 || len(definitions[0].Secrets) != 1 {
		t.Fatalf("database secret shape = %#v, want one container with one secret", definitions)
	}
	secret := definitions[0].Secrets[0]
	if secret.Name != "APP_DB_DSN" || secret.ValueFrom == "" {
		t.Errorf("database secret = %#v, want nonempty APP_DB_DSN reference", secret)
	}
}

// TestNewRegionService_FailsClosedOnResourceFailure is the negative case: a
// failure anywhere in the graph newRegionService builds must fail the whole
// program rather than being silently swallowed. See the stackMocks doc
// comment for why the failure surfaces via pulumi.RunErr's own error instead
// of this package's per-call wrapping.
func TestNewRegionService_FailsClosedOnResourceFailure(t *testing.T) {
	t.Parallel()
	cfg := testRegionConfig()
	wantErr := errors.New("boom")
	err := runNewRegionService(t, cfg, &stackMocks{
		fail: map[string]error{"aws:ecs/cluster:Cluster": wantErr},
	})
	if err == nil {
		t.Fatal("newRegionService() = nil, want error")
	}
}
