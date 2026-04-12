package topology

import (
	"context"
)

// graphAdder is a function that adds resources to a dependency graph.
type graphAdder func(ctx context.Context, e *Engine, g *DependencyGraph) error

// graphAdders returns the ordered list of functions that populate the graph.
func graphAdders() []graphAdder {
	return []graphAdder{
		addVPCs,
		addSubnets,
		addSecurityGroups,
		addInstances,
		addRouteTables,
		addNATGateways,
		addInternetGateways,
		addPeeringConnections,
		addNetworkACLs,
		addDNSRecords,
	}
}

// buildGraph constructs a full DependencyGraph by scanning all services.
func buildGraph(ctx context.Context, e *Engine) (*DependencyGraph, error) {
	g := &DependencyGraph{}

	for _, add := range graphAdders() {
		if err := add(ctx, e, g); err != nil {
			return nil, err
		}
	}

	return g, nil
}

func addVPCs(ctx context.Context, e *Engine, g *DependencyGraph) error {
	vpcs, err := e.networking.DescribeVPCs(ctx, nil)
	if err != nil {
		return err
	}

	for _, v := range vpcs {
		g.Resources = append(g.Resources, ResourceRef{
			ID: v.ID, Type: "vpc", Name: v.CIDRBlock,
		})
	}

	return nil
}

// addVPCMemberResources is a generic helper for resources that belong to a VPC
// via a simple "member-of" dependency (subnets, SGs, route tables, ACLs).
func addVPCMemberResources(
	g *DependencyGraph,
	resourceID, resourceType, resourceName, vpcID string,
) {
	ref := ResourceRef{ID: resourceID, Type: resourceType, Name: resourceName}
	g.Resources = append(g.Resources, ref)
	g.Dependencies = append(g.Dependencies, Dependency{
		From: ref,
		To:   ResourceRef{ID: vpcID, Type: "vpc"},
		Type: "member-of",
	})
}

func addSubnets(ctx context.Context, e *Engine, g *DependencyGraph) error {
	subnets, err := e.networking.DescribeSubnets(ctx, nil)
	if err != nil {
		return err
	}

	for _, s := range subnets {
		addVPCMemberResources(g, s.ID, "subnet", s.CIDRBlock, s.VPCID)
	}

	return nil
}

func addSecurityGroups(ctx context.Context, e *Engine, g *DependencyGraph) error {
	sgs, err := e.networking.DescribeSecurityGroups(ctx, nil)
	if err != nil {
		return err
	}

	for _, sg := range sgs {
		addVPCMemberResources(g, sg.ID, "security-group", sg.Name, sg.VPCID)
	}

	return nil
}

func addInstances(ctx context.Context, e *Engine, g *DependencyGraph) error {
	instances, err := e.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return err
	}

	for i := range instances {
		inst := &instances[i]
		ref := ResourceRef{ID: inst.ID, Type: "instance", Name: inst.InstanceType}
		g.Resources = append(g.Resources, ref)
		addInstanceDeps(g, ref, inst.VPCID, inst.SubnetID, inst.SecurityGroups)
	}

	return nil
}

func addInstanceDeps(
	g *DependencyGraph,
	ref ResourceRef,
	vpcID, subnetID string,
	sgIDs []string,
) {
	if vpcID != "" {
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: vpcID, Type: "vpc"},
			Type: "member-of",
		})
	}

	if subnetID != "" {
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: subnetID, Type: "subnet"},
			Type: "member-of",
		})
	}

	for _, sgID := range sgIDs {
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: sgID, Type: "security-group"},
			Type: "secured-by",
		})
	}
}

func addRouteTables(ctx context.Context, e *Engine, g *DependencyGraph) error {
	rts, err := e.networking.DescribeRouteTables(ctx, nil)
	if err != nil {
		return err
	}

	for _, rt := range rts {
		addVPCMemberResources(g, rt.ID, "route-table", "", rt.VPCID)
	}

	return nil
}

func addNATGateways(ctx context.Context, e *Engine, g *DependencyGraph) error {
	nats, err := e.networking.DescribeNATGateways(ctx, nil)
	if err != nil {
		return err
	}

	for _, nat := range nats {
		ref := ResourceRef{ID: nat.ID, Type: "nat-gateway"}
		g.Resources = append(g.Resources, ref)
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: nat.VPCID, Type: "vpc"},
			Type: "member-of",
		})

		if nat.SubnetID != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: nat.SubnetID, Type: "subnet"},
				Type: "member-of",
			})
		}
	}

	return nil
}

func addInternetGateways(ctx context.Context, e *Engine, g *DependencyGraph) error {
	igws, err := e.networking.DescribeInternetGateways(ctx, nil)
	if err != nil {
		return err
	}

	for _, igw := range igws {
		ref := ResourceRef{ID: igw.ID, Type: "internet-gateway"}
		g.Resources = append(g.Resources, ref)

		if igw.VpcID != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: igw.VpcID, Type: "vpc"},
				Type: "attached-to",
			})
		}
	}

	return nil
}

func addPeeringConnections(ctx context.Context, e *Engine, g *DependencyGraph) error {
	peerings, err := e.networking.DescribePeeringConnections(ctx, nil)
	if err != nil {
		return err
	}

	for _, p := range peerings {
		ref := ResourceRef{ID: p.ID, Type: "peering-connection", Name: p.Status}
		g.Resources = append(g.Resources, ref)
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: p.RequesterVPC, Type: "vpc"},
			Type: "peers-with",
		})
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: p.AccepterVPC, Type: "vpc"},
			Type: "peers-with",
		})
	}

	return nil
}

func addNetworkACLs(ctx context.Context, e *Engine, g *DependencyGraph) error {
	acls, err := e.networking.DescribeNetworkACLs(ctx, nil)
	if err != nil {
		return err
	}

	for _, acl := range acls {
		addVPCMemberResources(g, acl.ID, "network-acl", "", acl.VPCID)
	}

	return nil
}

func addDNSRecords(ctx context.Context, e *Engine, g *DependencyGraph) error {
	zones, err := e.dns.ListZones(ctx)
	if err != nil {
		return err
	}

	for _, zone := range zones {
		zoneRef := ResourceRef{ID: zone.ID, Type: "dns-zone", Name: zone.Name}
		g.Resources = append(g.Resources, zoneRef)

		if err := addZoneRecords(ctx, e, g, zone.ID, zoneRef); err != nil {
			return err
		}
	}

	return nil
}

func addZoneRecords(
	ctx context.Context,
	e *Engine,
	g *DependencyGraph,
	zoneID string,
	zoneRef ResourceRef,
) error {
	records, err := e.dns.ListRecords(ctx, zoneID)
	if err != nil {
		return err
	}

	for _, rec := range records {
		recRef := ResourceRef{ID: rec.Name, Type: "dns-record", Name: rec.Type}
		g.Resources = append(g.Resources, recRef)
		g.Dependencies = append(g.Dependencies, Dependency{
			From: recRef,
			To:   zoneRef,
			Type: "member-of",
		})
	}

	return nil
}
