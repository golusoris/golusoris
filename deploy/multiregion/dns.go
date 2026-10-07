// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// newGlobalDNS creates primary/secondary aliases in a caller-owned hosted zone.
func newGlobalDNS(
	ctx *pulumi.Context,
	provider *aws.Provider,
	primary, secondary *regionStack,
	domain, hostedZoneID string,
) error {
	opt := pulumi.Provider(provider)
	if err := newFailoverRecord(
		ctx, "primary", hostedZoneID, domain, primary, "PRIMARY", opt,
	); err != nil {
		return err
	}
	return newFailoverRecord(
		ctx, "secondary", hostedZoneID, domain, secondary, "SECONDARY", opt,
	)
}

// newFailoverRecord writes one alias A record carrying PRIMARY/SECONDARY failover routing.
func newFailoverRecord(
	ctx *pulumi.Context,
	name string,
	hostedZoneID string,
	domain string,
	region *regionStack,
	role string,
	opt pulumi.ResourceOption,
) error {
	_, err := route53.NewRecord(ctx, "golusoris-record-"+name, &route53.RecordArgs{
		ZoneId:        pulumi.String(hostedZoneID),
		Name:          pulumi.String(domain),
		Type:          pulumi.String("A"),
		SetIdentifier: pulumi.String(name),
		FailoverRoutingPolicies: route53.RecordFailoverRoutingPolicyArray{
			route53.RecordFailoverRoutingPolicyArgs{Type: pulumi.String(role)},
		},
		Aliases: route53.RecordAliasArray{route53.RecordAliasArgs{
			Name:                 region.ALBDNS,
			ZoneId:               region.ALBZone,
			EvaluateTargetHealth: pulumi.Bool(true),
		}},
	}, opt)
	if err != nil {
		return fmt.Errorf("pulumi: create failover record %s: %w", name, err)
	}
	return nil
}

// Latency-routing variant (alternative to failover): swap FailoverRoutingPolicies for
// LatencyRoutingPolicies{ Region: pulumi.String(cfg.Region) } on each record and drop the
// PRIMARY/SECONDARY roles — Route53 then serves the lowest-latency healthy region per client.
