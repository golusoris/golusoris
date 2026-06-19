package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// network holds the VPC plus the public/private subnets the data + app tiers attach to.
type network struct {
	VPC            *ec2.Vpc
	PublicSubnets  []*ec2.Subnet
	PrivateSubnets []*ec2.Subnet
}

// azSuffixes pins the two AZ letters used for the 2-public/2-private layout.
var azSuffixes = []string{"a", "b"}

// newNetwork builds a VPC with 2 public + 2 private subnets and a single NAT gateway.
func newNetwork(ctx *pulumi.Context, name, region string, opts ...pulumi.ResourceOption) (*network, error) {
	vpc, err := ec2.NewVpc(ctx, name+"-vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String("10.0.0.0/16"),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               pulumi.StringMap{"Name": pulumi.String(name + "-vpc")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create vpc %s: %w", name, err)
	}

	igw, err := ec2.NewInternetGateway(ctx, name+"-igw", &ec2.InternetGatewayArgs{
		VpcId: vpc.ID(),
		Tags:  pulumi.StringMap{"Name": pulumi.String(name + "-igw")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create igw %s: %w", name, err)
	}

	public, err := newPublicSubnets(ctx, name, region, vpc, opts...)
	if err != nil {
		return nil, err
	}

	private, err := newPrivateSubnets(ctx, name, region, vpc, opts...)
	if err != nil {
		return nil, err
	}

	if err = wireRouting(ctx, name, vpc, igw, public, private, opts...); err != nil {
		return nil, err
	}

	return &network{VPC: vpc, PublicSubnets: public, PrivateSubnets: private}, nil
}

// newPublicSubnets creates the internet-facing subnets that host the ALB + NAT.
func newPublicSubnets(
	ctx *pulumi.Context,
	name, region string,
	vpc *ec2.Vpc,
	opts ...pulumi.ResourceOption,
) ([]*ec2.Subnet, error) {
	subnets := make([]*ec2.Subnet, 0, len(azSuffixes))
	for i, az := range azSuffixes {
		sn, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-public-%s", name, az), &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String(fmt.Sprintf("10.0.%d.0/24", i)),
			AvailabilityZone:    pulumi.String(region + az),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags:                pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-public-%s", name, az))},
		}, opts...)
		if err != nil {
			return nil, fmt.Errorf("pulumi: create public subnet %s: %w", az, err)
		}
		subnets = append(subnets, sn)
	}
	return subnets, nil
}

// newPrivateSubnets creates the egress-only subnets that host the DB, cache, and ECS tasks.
func newPrivateSubnets(
	ctx *pulumi.Context,
	name, region string,
	vpc *ec2.Vpc,
	opts ...pulumi.ResourceOption,
) ([]*ec2.Subnet, error) {
	subnets := make([]*ec2.Subnet, 0, len(azSuffixes))
	for i, az := range azSuffixes {
		sn, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-private-%s", name, az), &ec2.SubnetArgs{
			VpcId:            vpc.ID(),
			CidrBlock:        pulumi.String(fmt.Sprintf("10.0.%d.0/24", i+10)),
			AvailabilityZone: pulumi.String(region + az),
			Tags:             pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-%s", name, az))},
		}, opts...)
		if err != nil {
			return nil, fmt.Errorf("pulumi: create private subnet %s: %w", az, err)
		}
		subnets = append(subnets, sn)
	}
	return subnets, nil
}

// wireRouting attaches public subnets to the IGW and private subnets to a single NAT gateway.
func wireRouting(
	ctx *pulumi.Context,
	name string,
	vpc *ec2.Vpc,
	igw *ec2.InternetGateway,
	public, private []*ec2.Subnet,
	opts ...pulumi.ResourceOption,
) error {
	publicRT, err := ec2.NewRouteTable(ctx, name+"-public-rt", &ec2.RouteTableArgs{
		VpcId: vpc.ID(),
		Routes: ec2.RouteTableRouteArray{ec2.RouteTableRouteArgs{
			CidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId: igw.ID(),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-public-rt")},
	}, opts...)
	if err != nil {
		return fmt.Errorf("pulumi: create public route table %s: %w", name, err)
	}
	if err = associate(ctx, name+"-public", public, publicRT, opts...); err != nil {
		return err
	}

	nat, err := newNAT(ctx, name, public[0], opts...)
	if err != nil {
		return err
	}

	privateRT, err := ec2.NewRouteTable(ctx, name+"-private-rt", &ec2.RouteTableArgs{
		VpcId: vpc.ID(),
		Routes: ec2.RouteTableRouteArray{ec2.RouteTableRouteArgs{
			CidrBlock:    pulumi.String("0.0.0.0/0"),
			NatGatewayId: nat.ID(),
		}},
		Tags: pulumi.StringMap{"Name": pulumi.String(name + "-private-rt")},
	}, opts...)
	if err != nil {
		return fmt.Errorf("pulumi: create private route table %s: %w", name, err)
	}
	return associate(ctx, name+"-private", private, privateRT, opts...)
}

// newNAT provisions an Elastic IP plus a NAT gateway in the first public subnet.
func newNAT(
	ctx *pulumi.Context,
	name string,
	publicSubnet *ec2.Subnet,
	opts ...pulumi.ResourceOption,
) (*ec2.NatGateway, error) {
	eip, err := ec2.NewEip(ctx, name+"-nat-eip", &ec2.EipArgs{
		Domain: pulumi.String("vpc"),
		Tags:   pulumi.StringMap{"Name": pulumi.String(name + "-nat-eip")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create nat eip %s: %w", name, err)
	}
	nat, err := ec2.NewNatGateway(ctx, name+"-nat", &ec2.NatGatewayArgs{
		AllocationId: eip.ID(),
		SubnetId:     publicSubnet.ID(),
		Tags:         pulumi.StringMap{"Name": pulumi.String(name + "-nat")},
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("pulumi: create nat gateway %s: %w", name, err)
	}
	return nat, nil
}

// associate binds each subnet to the given route table.
func associate(
	ctx *pulumi.Context,
	name string,
	subnets []*ec2.Subnet,
	rt *ec2.RouteTable,
	opts ...pulumi.ResourceOption,
) error {
	for i, sn := range subnets {
		_, err := ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-rta-%d", name, i), &ec2.RouteTableAssociationArgs{
			SubnetId:     sn.ID(),
			RouteTableId: rt.ID(),
		}, opts...)
		if err != nil {
			return fmt.Errorf("pulumi: associate route table %s-%d: %w", name, i, err)
		}
	}
	return nil
}

// privateSubnetIDs returns the private subnet IDs as a pulumi input for DB/cache subnet groups.
func (n *network) privateSubnetIDs() pulumi.StringArray {
	ids := make(pulumi.StringArray, 0, len(n.PrivateSubnets))
	for _, sn := range n.PrivateSubnets {
		ids = append(ids, sn.ID())
	}
	return ids
}

// publicSubnetIDs returns the public subnet IDs as a pulumi input for the ALB.
func (n *network) publicSubnetIDs() pulumi.StringArray {
	ids := make(pulumi.StringArray, 0, len(n.PublicSubnets))
	for _, sn := range n.PublicSubnets {
		ids = append(ids, sn.ID())
	}
	return ids
}
