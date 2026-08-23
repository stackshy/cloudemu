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
result, err := engine.CanConnect(ctx, topology.ConnectivityQuery{
    SrcInstanceID: "i-abc123",
    DstInstanceID: "i-def456",
    Protocol:      "tcp", // "tcp", "udp", "icmp", or "-1" for all
    Port:          443,
})
// result.Allowed    -- bool
// result.Reason     -- string explaining why allowed/denied
// result.Path       -- []RouteHop, the network path that was walked
// result.SGVerdict  -- TrafficVerdict from the security-group evaluation
// result.ACLVerdict -- *ACLVerdict from the network-ACL evaluation (nil if not reached)
```

### TraceRoute

Produces a hop-by-hop path from a source instance to a destination IP.

```go
hops, err := engine.TraceRoute(ctx, "i-abc123", "10.0.2.50")
// hops -- []RouteHop with Type, ResourceID, and Detail
```

### Resolve

Resolves a DNS name to IP addresses using the mock DNS service.

```go
ips, err := engine.Resolve(ctx, "api.example.com")
// ips -- []string{"10.0.1.50", "10.0.1.51"}
```

### EvaluateSecurityGroups

Evaluates whether traffic is allowed between a source and destination security
group on a given port and protocol.

```go
verdict, err := engine.EvaluateSecurityGroups(ctx, "sg-src", "sg-dst", 443, "tcp")
// verdict.Allowed      -- bool
// verdict.IngressMatch -- *RuleMatch (the destination-group rule that matched)
// verdict.EgressMatch  -- *RuleMatch (the source-group rule that matched)
// verdict.Reason       -- string
```

### EvaluateNetworkACL

Evaluates a network ACL against a specific traffic pattern. Rules are evaluated
in order, first match wins.

```go
verdict, err := engine.EvaluateNetworkACL(ctx, "acl-abc123", "10.0.1.5", "10.0.2.9", 443, "tcp", true /* ingress */)
// verdict.Allowed    -- bool
// verdict.RuleNumber -- int, the ACL rule that decided
// verdict.Action     -- "allow" or "deny"
// verdict.Reason     -- string
```

## Result Types

### ConnectivityResult (from `CanConnect`)

| Field | Type | Description |
|-------|------|-------------|
| `Allowed` | `bool` | Whether the connection is permitted |
| `Reason` | `string` | Human-readable explanation |
| `Path` | `[]RouteHop` | Ordered network path that was walked |
| `SGVerdict` | `TrafficVerdict` | Security-group evaluation result |
| `ACLVerdict` | `*ACLVerdict` | Network-ACL evaluation result (nil when not reached) |

### RouteHop (path element, also returned by `TraceRoute`)

| Field | Type | Description |
|-------|------|-------------|
| `Type` | `string` | `"instance"`, `"subnet"`, `"route-table"`, `"gateway"`, `"nat-gateway"`, `"peering"`, `"local"` |
| `ResourceID` | `string` | Resource ID of the hop |
| `Detail` | `string` | Human-readable label |

### TrafficVerdict (from `EvaluateSecurityGroups`)

| Field | Type | Description |
|-------|------|-------------|
| `Allowed` | `bool` | Whether the security groups permit the traffic |
| `IngressMatch` | `*RuleMatch` | Destination-group rule that matched (nil if none) |
| `EgressMatch` | `*RuleMatch` | Source-group rule that matched (nil if none) |
| `Reason` | `string` | Human-readable explanation |

### ACLVerdict (from `EvaluateNetworkACL`)

| Field | Type | Description |
|-------|------|-------------|
| `Allowed` | `bool` | Whether the ACL permits the traffic |
| `RuleNumber` | `int` | The ACL rule number that decided |
| `Action` | `string` | `"allow"` or `"deny"` |
| `Reason` | `string` | Human-readable explanation |
