# Network Topology Simulation

## What It Is

CloudEmu's topology engine is a network simulation layer that sits above the compute, networking, and DNS mock services. It reads the live state from those services -- VPCs, subnets, security groups, route tables, network ACLs, peering connections, NAT gateways, internet gateways, and DNS records -- and answers reachability questions: "Can instance A talk to instance B on port 443?" or "What path does a packet take from this subnet to the internet?" This enables integration tests that verify network architecture without deploying real infrastructure.

## Why It Is Unique

Most cloud mock libraries stop at CRUD operations: you can create a VPC and list it back, but the mock does not understand that two instances in different VPCs cannot reach each other unless a peering connection exists. CloudEmu's topology engine actually evaluates security group rules, network ACLs, route tables, and peering state to produce realistic connectivity answers. This means your tests can catch misconfigured security groups, missing routes, or broken peering connections before code reaches a real cloud environment.

## Architecture

```
┌──────────────────────────────────────────────────┐
│               Topology Engine                    │
│                                                  │
│   CanConnect()   TraceRoute()   Resolve()        │
│   EvaluateSecurityGroups()  EvaluateNetworkACL() │
└──────┬──────────────┬──────────────┬─────────────┘
       │              │              │
       ▼              ▼              ▼
  ┌─────────┐   ┌──────────┐   ┌─────────┐
  │ Compute │   │Networking│   │   DNS   │
  │ Driver  │   │  Driver  │   │ Driver  │
  └─────────┘   └──────────┘   └─────────┘
       │              │              │
       ▼              ▼              ▼
  ┌─────────┐   ┌──────────┐   ┌─────────┐
  │  EC2 /  │   │ VPC/VNet │   │Route53/ │
  │  GCE /  │   │  /GCPVPC │   │AzureDNS/│
  │  VMs    │   │          │   │CloudDNS │
  └─────────┘   └──────────┘   └─────────┘
```

The topology engine does not store its own state. It reads from the existing mock services on every call, so connectivity results always reflect the current configuration.

## CanConnect Flow

`CanConnect` determines whether traffic can flow between a source and destination.

**Step-by-step evaluation:**

1. **Resolve endpoints** -- Look up the source and destination instances by ID. Determine their VPC, subnet, and associated security groups.
2. **Same VPC check** -- If both instances are in the same VPC, proceed to security group evaluation. If in different VPCs, check for a peering connection.
3. **Peering check** -- If instances are in different VPCs, look for an active peering connection between those VPCs. If none exists (or it is not in "active" state), return unreachable.
4. **Route table evaluation** -- Check the route table associated with the source subnet for a route to the destination CIDR. Verify the route target is valid (not a blackhole).
5. **Network ACL evaluation** -- Evaluate inbound and outbound ACL rules in rule-number order. The first matching rule determines allow/deny.
6. **Security group evaluation** -- Check that the destination's inbound security group rules allow traffic from the source on the requested protocol and port. Check that the source's outbound rules allow the traffic.
7. **Return result** -- If all checks pass, the connection is allowed. The result includes which rule or component allowed or denied the traffic.

## TraceRoute Flow

`TraceRoute` produces a hop-by-hop path from source to destination, similar to the `traceroute` command but evaluated against the virtual network topology.

**Step-by-step evaluation:**

1. **Start at source** -- Record the source instance, its subnet, and VPC.
2. **Route table lookup** -- Find the route table for the source subnet. Determine the next hop based on the destination IP (longest prefix match).
3. **Hop through gateways** -- If the route points to a NAT gateway, internet gateway, or peering connection, record that as a hop and continue from the next network segment.
4. **Cross VPC** -- If the route goes through a peering connection, switch to the peer VPC and evaluate its route table for the destination.
5. **Arrive at destination** -- When the destination subnet is reached, record the final hop.
6. **Return trace** -- Return the ordered list of hops with their types (subnet, NAT gateway, internet gateway, peering connection, destination).

## API Reference

### CanConnect

Checks whether traffic can flow from source to destination on a given protocol and port.

```go
result, err := engine.CanConnect(ctx, CanConnectInput{
    SourceInstanceID: "i-abc123",
    DestInstanceID:   "i-def456",
    Protocol:         "tcp",
    Port:             443,
})
// result.Allowed  -- bool
// result.Reason   -- string explaining why allowed/denied
// result.DeniedBy -- which component denied (e.g., "security-group", "network-acl", "no-route")
```

### TraceRoute

Produces a hop-by-hop path between two endpoints.

```go
trace, err := engine.TraceRoute(ctx, TraceRouteInput{
    SourceInstanceID: "i-abc123",
    DestInstanceID:   "i-def456",
    Protocol:         "tcp",
    Port:             80,
})
// trace.Hops -- []Hop with Type, ID, and description
// trace.Reachable -- bool
```

### Resolve

Resolves a DNS name to IP addresses using the mock DNS service.

```go
ips, err := engine.Resolve(ctx, "api.example.com")
// ips -- []string{"10.0.1.50", "10.0.1.51"}
```

### EvaluateSecurityGroups

Evaluates whether a set of security groups allows traffic on a given protocol, port, and source CIDR.

```go
allowed, err := engine.EvaluateSecurityGroups(ctx, EvaluateSGInput{
    SecurityGroupIDs: []string{"sg-abc123"},
    Direction:        "inbound",  // or "outbound"
    Protocol:         "tcp",
    Port:             443,
    CIDR:             "10.0.0.0/16",
})
// allowed -- bool
```

### EvaluateNetworkACL

Evaluates a network ACL against a specific traffic pattern.

```go
allowed, err := engine.EvaluateNetworkACL(ctx, EvaluateACLInput{
    NetworkACLID: "acl-abc123",
    Egress:       false,
    Protocol:     "tcp",
    Port:         443,
    CIDR:         "10.0.1.0/24",
})
// allowed -- bool
```

## Result Types

### CanConnectResult

| Field | Type | Description |
|-------|------|-------------|
| `Allowed` | `bool` | Whether the connection is permitted |
| `Reason` | `string` | Human-readable explanation |
| `DeniedBy` | `string` | Component that denied traffic (empty if allowed): `"security-group"`, `"network-acl"`, `"no-route"`, `"no-peering"` |

### TraceRouteResult

| Field | Type | Description |
|-------|------|-------------|
| `Hops` | `[]Hop` | Ordered list of network hops |
| `Reachable` | `bool` | Whether the destination was reached |

### Hop

| Field | Type | Description |
|-------|------|-------------|
| `Type` | `string` | Hop type: `"subnet"`, `"nat-gateway"`, `"internet-gateway"`, `"peering"`, `"destination"` |
| `ID` | `string` | Resource ID of the hop |
| `Description` | `string` | Human-readable label |
