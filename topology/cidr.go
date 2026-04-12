package topology

import (
	"net"
	"sort"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

// ipInCIDR checks whether the given IP address is contained in the CIDR block.
func ipInCIDR(ip, cidr string) bool {
	if cidr == "0.0.0.0/0" {
		return true
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}

	return network.Contains(parsed)
}

// cidrPrefixLen returns the prefix length of a CIDR block, or -1 on error.
func cidrPrefixLen(cidr string) int {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return -1
	}

	ones, _ := network.Mask.Size()

	return ones
}

// findMatchingRoute finds the route with the longest prefix match for the
// destination IP. Returns nil if no route matches.
func findMatchingRoute(routes []netdriver.Route, destIP string) *netdriver.Route {
	var best *netdriver.Route

	bestPrefix := -1

	for i := range routes {
		r := &routes[i]
		if !ipInCIDR(destIP, r.DestinationCIDR) {
			continue
		}

		prefix := cidrPrefixLen(r.DestinationCIDR)
		if prefix > bestPrefix {
			bestPrefix = prefix
			best = r
		}
	}

	return best
}

// portInRange checks whether the port falls within [fromPort, toPort].
// A range of (0, 0) with protocol "-1" means all ports.
func portInRange(port, fromPort, toPort int) bool {
	if fromPort == 0 && toPort == 0 {
		return true
	}

	return port >= fromPort && port <= toPort
}

// protocolMatches checks if a rule's protocol matches the query protocol.
// Protocol "-1" matches all protocols.
func protocolMatches(ruleProtocol, queryProtocol string) bool {
	if ruleProtocol == "-1" || queryProtocol == "-1" {
		return true
	}

	return ruleProtocol == queryProtocol
}

// sortACLRules sorts network ACL rules by rule number ascending.
func sortACLRules(rules []netdriver.NetworkACLRule) []netdriver.NetworkACLRule {
	sorted := make([]netdriver.NetworkACLRule, len(rules))
	copy(sorted, rules)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].RuleNumber < sorted[j].RuleNumber
	})

	return sorted
}
