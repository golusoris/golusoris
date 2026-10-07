// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestGlobalDNSUsesExplicitPrimaryProvider(t *testing.T) {
	t.Parallel()
	mocks := &stackMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "primary", &aws.ProviderArgs{
			Region: pulumi.String("us-east-1"),
		})
		if err != nil {
			return err
		}
		primary := &regionStack{
			ALBDNS:  pulumi.String("primary.example.invalid").ToStringOutput(),
			ALBZone: pulumi.String("zone-primary").ToStringOutput(),
		}
		secondary := &regionStack{
			ALBDNS:  pulumi.String("secondary.example.invalid").ToStringOutput(),
			ALBZone: pulumi.String("zone-secondary").ToStringOutput(),
		}
		return newGlobalDNS(ctx, provider, primary, secondary, "app.example.invalid", "ZCALLER")
	}, pulumi.WithMocks("golusoris-multiregion-test", "test", mocks))
	if err != nil {
		t.Fatalf("newGlobalDNS() error = %v", err)
	}

	for _, typeToken := range []string{
		"aws:route53/record:Record",
	} {
		calls := mocks.findAll(typeToken)
		if len(calls) == 0 {
			t.Fatalf("no %s resources registered", typeToken)
		}
		for _, call := range calls {
			if !strings.Contains(call.provider, "pulumi:providers:aws::primary") {
				t.Errorf("%s %q provider = %q, want explicit primary provider", typeToken, call.name, call.provider)
			}
		}
	}
	if calls := mocks.findAll("aws:route53/zone:Zone"); len(calls) != 0 {
		t.Errorf("created %d hosted zones; expected caller-owned zone", len(calls))
	}
	if calls := mocks.findAll("aws:route53/healthCheck:HealthCheck"); len(calls) != 0 {
		t.Errorf("created %d direct health checks; expected ALB alias target health", len(calls))
	}
	for _, record := range mocks.findAll("aws:route53/record:Record") {
		if got := record.inputs["zoneId"].StringValue(); got != "ZCALLER" {
			t.Errorf("record %q zoneId = %q, want caller-owned ZCALLER", record.name, got)
		}
		if _, exists := record.inputs["healthCheckId"]; exists {
			t.Errorf("record %q has direct healthCheckId", record.name)
		}
		aliases := record.inputs["aliases"].ArrayValue()
		if len(aliases) != 1 || !aliases[0].ObjectValue()["evaluateTargetHealth"].BoolValue() {
			t.Errorf("record %q does not evaluate ALB target health: %v", record.name, aliases)
		}
	}
}
