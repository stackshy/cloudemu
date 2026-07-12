// Package topology provides a network topology simulation engine that evaluates
// actual connectivity between cloud resources.
package topology

// ConnectivityQuery holds the parameters for a CanConnect evaluation.
type ConnectivityQuery struct {
	SrcInstanceID string
	DstInstanceID string
	Port          int
	Protocol      string // "tcp", "udp", "icmp", "-1" (all)
}

// ConnectivityResult describes the result of a CanConnect evaluation.
type ConnectivityResult struct {
	Allowed    bool
	Reason     string
	Path       []RouteHop
	SGVerdict  TrafficVerdict
	ACLVerdict *ACLVerdict
}

// RouteHop represents one step in the network path.
type RouteHop struct {
	Type       string // "instance", "subnet", "route-table", "gateway", "nat-gateway", "peering", "local"
	ResourceID string
	Detail     string
}

// TrafficVerdict describes the result of security group evaluation.
type TrafficVerdict struct {
	Allowed      bool
	EgressMatch  *RuleMatch
	IngressMatch *RuleMatch
	Reason       string
}

// RuleMatch identifies which security rule matched.
type RuleMatch struct {
	GroupID  string
	Protocol string
	CIDR     string
	FromPort int
	ToPort   int
}

// ACLVerdict describes the result of network ACL evaluation.
type ACLVerdict struct {
	Allowed    bool
	RuleNumber int
	Action     string // "allow" or "deny"
	Reason     string
}
