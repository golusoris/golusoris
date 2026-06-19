package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// readyzPath is the per-region health-check path the failover records evaluate.
const readyzPath = "/readyz"

// newGlobalDNS creates the hosted zone + primary/secondary failover records, each gated by a health check.
func newGlobalDNS(
	ctx *pulumi.Context,
	primary, secondary *regionStack,
	domain string,
) (*route53.Zone, error) {
	zone, err := route53.NewZone(ctx, "golusoris-zone", &route53.ZoneArgs{
		Name: pulumi.String(domain),
		Tags: pulumi.StringMap{"Name": pulumi.String(domain)},
	})
	if err != nil {
		return nil, fmt.Errorf("pulumi: create hosted zone %s: %w", domain, err)
	}

	primaryHC, err := newHealthCheck(ctx, "primary", primary.ALBDNS)
	if err != nil {
		return nil, err
	}
	secondaryHC, err := newHealthCheck(ctx, "secondary", secondary.ALBDNS)
	if err != nil {
		return nil, err
	}

	if err = newFailoverRecord(ctx, "primary", zone, domain, primary, primaryHC, "PRIMARY"); err != nil {
		return nil, err
	}
	if err = newFailoverRecord(ctx, "secondary", zone, domain, secondary, secondaryHC, "SECONDARY"); err != nil {
		return nil, err
	}
	return zone, nil
}

// newHealthCheck pings a region's ALB on HTTP /readyz so Route53 can detect a region outage.
func newHealthCheck(
	ctx *pulumi.Context,
	name string,
	albDNS pulumi.StringInput,
) (*route53.HealthCheck, error) {
	hc, err := route53.NewHealthCheck(ctx, "golusoris-hc-"+name, &route53.HealthCheckArgs{
		Fqdn:             albDNS,
		Type:             pulumi.String("HTTP"),
		Port:             pulumi.Int(80),
		ResourcePath:     pulumi.String(readyzPath),
		RequestInterval:  pulumi.Int(30),
		FailureThreshold: pulumi.Int(3),
		Tags:             pulumi.StringMap{"Name": pulumi.String("golusoris-hc-" + name)},
	})
	if err != nil {
		return nil, fmt.Errorf("pulumi: create health check %s: %w", name, err)
	}
	return hc, nil
}

// newFailoverRecord writes one alias A record carrying PRIMARY/SECONDARY failover routing.
func newFailoverRecord(
	ctx *pulumi.Context,
	name string,
	zone *route53.Zone,
	domain string,
	region *regionStack,
	hc *route53.HealthCheck,
	role string,
) error {
	_, err := route53.NewRecord(ctx, "golusoris-record-"+name, &route53.RecordArgs{
		ZoneId:        zone.ID(),
		Name:          pulumi.String(domain),
		Type:          pulumi.String("A"),
		SetIdentifier: pulumi.String(name),
		FailoverRoutingPolicies: route53.RecordFailoverRoutingPolicyArray{
			route53.RecordFailoverRoutingPolicyArgs{Type: pulumi.String(role)},
		},
		HealthCheckId: hc.ID(),
		Aliases: route53.RecordAliasArray{route53.RecordAliasArgs{
			Name:                 region.ALBDNS,
			ZoneId:               region.ALBZone,
			EvaluateTargetHealth: pulumi.Bool(true),
		}},
	})
	if err != nil {
		return fmt.Errorf("pulumi: create failover record %s: %w", name, err)
	}
	return nil
}

// Latency-routing variant (alternative to failover): swap FailoverRoutingPolicies for
// LatencyRoutingPolicies{ Region: pulumi.String(cfg.Region) } on each record and drop the
// PRIMARY/SECONDARY roles — Route53 then serves the lowest-latency healthy region per client.
