package topology

import (
	"context"

	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
)

// graphAdder is a function that adds resources to a dependency graph.
type graphAdder func(ctx context.Context, e *Engine, g *DependencyGraph) error

// graphAdders returns the ordered list of functions that populate the graph.
func graphAdders() []graphAdder {
	return []graphAdder{
		// Core networking + compute + DNS.
		addVPCs,
		addSubnets,
		addSecurityGroups,
		addInstances,
		addVolumes,
		addRouteTables,
		addNATGateways,
		addInternetGateways,
		addPeeringConnections,
		addNetworkACLs,
		addDNSRecords,
		// Cross-service (no-op when driver is nil).
		addLoadBalancers,
		addTargetGroups,
		addListeners,
		addFunctions,
		addQueues,
		addAlarms,
		addNotificationChannels,
		addInstanceProfiles,
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

	setProvider(g, e.provider)

	return g, nil
}

// setProvider stamps every ResourceRef with the engine's provider name.
func setProvider(g *DependencyGraph, provider string) {
	if provider == "" {
		return
	}

	for i := range g.Resources {
		g.Resources[i].Provider = provider
	}
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

func addVolumes(ctx context.Context, e *Engine, g *DependencyGraph) error {
	volumes, err := e.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return err
	}

	for i := range volumes {
		vol := &volumes[i]
		ref := ResourceRef{ID: vol.ID, Type: "volume", Name: vol.VolumeType}
		g.Resources = append(g.Resources, ref)

		if vol.AttachedTo != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: vol.AttachedTo, Type: "instance"},
				Type: "attached-to",
			})
		}
	}

	return nil
}

func addLoadBalancers(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.loadbalancer == nil {
		return nil
	}

	lbs, err := e.loadbalancer.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return err
	}

	for i := range lbs {
		lb := &lbs[i]
		ref := ResourceRef{ID: lb.ARN, Type: "load-balancer", Name: lb.Name}
		g.Resources = append(g.Resources, ref)

		for _, subnetID := range lb.Subnets {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: subnetID, Type: "subnet"},
				Type: "member-of",
			})
		}
	}

	return nil
}

func addTargetGroups(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.loadbalancer == nil {
		return nil
	}

	tgs, err := e.loadbalancer.DescribeTargetGroups(ctx, nil)
	if err != nil {
		return err
	}

	for _, tg := range tgs {
		ref := ResourceRef{ID: tg.ARN, Type: "target-group", Name: tg.Name}
		g.Resources = append(g.Resources, ref)

		if tg.VPCID != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: tg.VPCID, Type: "vpc"},
				Type: "member-of",
			})
		}

		addTargetHealthDeps(ctx, e, g, tg.ARN, ref)
	}

	return nil
}

func addTargetHealthDeps(
	ctx context.Context,
	e *Engine,
	g *DependencyGraph,
	tgARN string,
	tgRef ResourceRef,
) {
	targets, err := e.loadbalancer.DescribeTargetHealth(ctx, tgARN)
	if err != nil {
		return
	}

	for _, th := range targets {
		g.Dependencies = append(g.Dependencies, Dependency{
			From: ResourceRef{ID: th.Target.ID, Type: "instance"},
			To:   tgRef,
			Type: "member-of",
		})
	}
}

func addListeners(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.loadbalancer == nil {
		return nil
	}

	lbs, err := e.loadbalancer.DescribeLoadBalancers(ctx, nil)
	if err != nil {
		return err
	}

	for i := range lbs {
		if err := addLBListeners(ctx, e.loadbalancer, g, lbs[i].ARN); err != nil {
			return err
		}
	}

	return nil
}

func addLBListeners(
	ctx context.Context,
	lb lbdriver.LoadBalancer,
	g *DependencyGraph,
	lbARN string,
) error {
	listeners, err := lb.DescribeListeners(ctx, lbARN)
	if err != nil {
		return err
	}

	for _, lis := range listeners {
		ref := ResourceRef{ID: lis.ARN, Type: "listener"}
		g.Resources = append(g.Resources, ref)

		g.Dependencies = append(g.Dependencies, Dependency{
			From: ref,
			To:   ResourceRef{ID: lis.LBARN, Type: "load-balancer"},
			Type: "belongs-to",
		})

		if lis.TargetGroupARN != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: lis.TargetGroupARN, Type: "target-group"},
				Type: "routes-to",
			})
		}
	}

	return nil
}

func addFunctions(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.serverless == nil {
		return nil
	}

	fns, err := e.serverless.ListFunctions(ctx)
	if err != nil {
		return err
	}

	for i := range fns {
		fn := &fns[i]
		fnRef := ResourceRef{ID: fn.ARN, Type: "function", Name: fn.Name}
		g.Resources = append(g.Resources, fnRef)

		addESMDeps(ctx, e, g, fn.Name, fnRef)
	}

	return nil
}

func addESMDeps(ctx context.Context, e *Engine, g *DependencyGraph, fnName string, fnRef ResourceRef) {
	esms, err := e.serverless.ListEventSourceMappings(ctx, fnName)
	if err != nil {
		return
	}

	for _, esm := range esms {
		esmRef := ResourceRef{ID: esm.UUID, Type: "event-source-mapping"}
		g.Resources = append(g.Resources, esmRef)

		g.Dependencies = append(g.Dependencies, Dependency{
			From: esmRef,
			To:   fnRef,
			Type: "triggers",
		})

		if esm.EventSourceArn != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: esmRef,
				To:   ResourceRef{ID: esm.EventSourceArn, Type: "queue"},
				Type: "triggered-by",
			})
		}
	}
}

func addQueues(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.messagequeue == nil {
		return nil
	}

	queues, err := e.messagequeue.ListQueues(ctx, "")
	if err != nil {
		return err
	}

	for _, q := range queues {
		g.Resources = append(g.Resources, ResourceRef{
			ID: q.ARN, Type: "queue", Name: q.Name,
		})
	}

	return nil
}

func addAlarms(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.monitoring == nil {
		return nil
	}

	alarms, err := e.monitoring.DescribeAlarms(ctx, nil)
	if err != nil {
		return err
	}

	for _, alarm := range alarms {
		g.Resources = append(g.Resources, ResourceRef{
			ID: alarm.Name, Type: "alarm", Name: alarm.MetricName,
		})
	}

	return nil
}

func addNotificationChannels(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.monitoring == nil {
		return nil
	}

	channels, err := e.monitoring.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}

	for _, ch := range channels {
		g.Resources = append(g.Resources, ResourceRef{
			ID: ch.ID, Type: "notification-channel", Name: ch.Name,
		})
	}

	return nil
}

func addInstanceProfiles(ctx context.Context, e *Engine, g *DependencyGraph) error {
	if e.iam == nil {
		return nil
	}

	profiles, err := e.iam.ListInstanceProfiles(ctx)
	if err != nil {
		return err
	}

	for _, p := range profiles {
		ref := ResourceRef{ID: p.Name, Type: "instance-profile", Name: p.RoleName}
		g.Resources = append(g.Resources, ref)

		if p.RoleName != "" {
			g.Dependencies = append(g.Dependencies, Dependency{
				From: ref,
				To:   ResourceRef{ID: p.RoleName, Type: "role"},
				Type: "uses",
			})
		}
	}

	return nil
}
