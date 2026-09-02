package topology

import (
	"context"
	"fmt"
	"slices"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// ruleSource describes the peer whose traffic a security rule is evaluated
// against: its IP address (IPv4 or IPv6) and the security groups it belongs to.
// The group memberships let a rule that references another security group
// (UserIdGroupPairs / ReferencedGroupID) resolve "is the peer a member of the
// referenced group?" — the most common real-cloud pattern (e.g. "allow the db
// SG ingress from the app SG"), which a CIDR-only matcher silently denies.
type ruleSource struct {
	ip       string
	groupIDs []string
}

// EvaluateSecurityGroups checks whether traffic from srcSG to dstSG is allowed
// on the given port and protocol. Both egress on src and ingress on dst must match.
func (e *Engine) EvaluateSecurityGroups(
	ctx context.Context,
	srcSGID, dstSGID string,
	port int,
	protocol string,
) (*TrafficVerdict, error) {
	srcGroups, err := e.networking.DescribeSecurityGroups(ctx, []string{srcSGID})
	if err != nil {
		return nil, err
	}

	if len(srcGroups) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "security group %s not found", srcSGID)
	}

	dstGroups, err := e.networking.DescribeSecurityGroups(ctx, []string{dstSGID})
	if err != nil {
		return nil, err
	}

	if len(dstGroups) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "security group %s not found", dstSGID)
	}

	return evaluateSGPair(&srcGroups[0], &dstGroups[0], port, protocol), nil
}

func evaluateSGPair(
	src, dst *netdriver.SecurityGroupInfo,
	port int,
	protocol string,
) *TrafficVerdict {
	// Egress on the source is evaluated against the destination group; ingress
	// on the destination against the source group. Carrying the peer's group id
	// lets a referenced-group ("allow from sg-app") rule resolve, while the
	// 0.0.0.0/0 ip preserves the existing all-traffic-CIDR matching.
	egressMatch := matchRules(src.EgressRules, src.ID, port, protocol,
		ruleSource{ip: "0.0.0.0/0", groupIDs: []string{dst.ID}})
	if egressMatch == nil {
		return &TrafficVerdict{
			Allowed: false,
			Reason:  fmt.Sprintf("no egress rule on %s allows %s:%d", src.ID, protocol, port),
		}
	}

	ingressMatch := matchRules(dst.IngressRules, dst.ID, port, protocol,
		ruleSource{ip: "0.0.0.0/0", groupIDs: []string{src.ID}})
	if ingressMatch == nil {
		return &TrafficVerdict{
			Allowed: false,
			Reason:  fmt.Sprintf("no ingress rule on %s allows %s:%d", dst.ID, protocol, port),
		}
	}

	return &TrafficVerdict{
		Allowed:      true,
		EgressMatch:  egressMatch,
		IngressMatch: ingressMatch,
		Reason:       "allowed by security group rules",
	}
}

func matchRules(
	rules []netdriver.SecurityRule,
	groupID string,
	port int,
	protocol string,
	src ruleSource,
) *RuleMatch {
	for i := range rules {
		r := &rules[i]

		if !protocolMatches(r.Protocol, protocol) {
			continue
		}

		if !portInRange(port, r.FromPort, r.ToPort) {
			continue
		}

		if !ruleSelectorMatches(r, src) {
			continue
		}

		return &RuleMatch{
			GroupID:  groupID,
			Protocol: r.Protocol,
			CIDR:     r.CIDR,
			FromPort: r.FromPort,
			ToPort:   r.ToPort,
		}
	}

	return nil
}

// ruleSelectorMatches reports whether the rule's source/destination selector
// admits the given peer. On the wire a rule carries exactly one of CIDR,
// IPv6CIDR, PrefixListID or ReferencedGroupID, but the matcher is defensive: it
// admits the peer when ANY populated selector matches. An empty selector never
// matches, so an unpopulated rule does not admit everything.
//
// PrefixListID is a documented limitation: managed prefix lists are not part of
// the topology engine's inputs (the networking driver it consumes exposes no
// prefix-list lookup), so a prefix-list rule cannot be resolved here and is
// treated as non-matching rather than crashing or silently admitting the peer.
func ruleSelectorMatches(r *netdriver.SecurityRule, src ruleSource) bool {
	if r.ReferencedGroupID != "" && slices.Contains(src.groupIDs, r.ReferencedGroupID) {
		return true
	}

	if r.CIDR != "" && ipInCIDR(src.ip, r.CIDR) {
		return true
	}

	if r.IPv6CIDR != "" && ipInCIDR(src.ip, r.IPv6CIDR) {
		return true
	}

	return false
}

// EvaluateNetworkACL evaluates a network ACL's rules against the given traffic.
// Rules are evaluated in order by rule number; the first matching rule wins.
// If no rule matches, traffic is implicitly denied.
func (e *Engine) EvaluateNetworkACL(
	ctx context.Context,
	aclID string,
	srcIP, dstIP string,
	port int,
	protocol string,
	ingress bool,
) (*ACLVerdict, error) {
	acls, err := e.networking.DescribeNetworkACLs(ctx, []string{aclID})
	if err != nil {
		return nil, err
	}

	if len(acls) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "network ACL %s not found", aclID)
	}

	return evaluateACLRules(&acls[0], srcIP, dstIP, port, protocol, ingress), nil
}

func evaluateACLRules(
	acl *netdriver.NetworkACL,
	srcIP, dstIP string,
	port int,
	protocol string,
	ingress bool,
) *ACLVerdict {
	sorted := sortACLRules(acl.Rules)

	checkIP := srcIP
	if !ingress {
		checkIP = dstIP
	}

	for _, rule := range sorted {
		if rule.Egress == ingress {
			continue
		}

		if !protocolMatches(rule.Protocol, protocol) {
			continue
		}

		if !portInRange(port, rule.FromPort, rule.ToPort) {
			continue
		}

		if !ipInCIDR(checkIP, rule.CIDR) {
			continue
		}

		allowed := rule.Action == "allow"

		return &ACLVerdict{
			Allowed:    allowed,
			RuleNumber: rule.RuleNumber,
			Action:     rule.Action,
			Reason:     fmt.Sprintf("matched ACL rule %d: %s", rule.RuleNumber, rule.Action),
		}
	}

	return &ACLVerdict{
		Allowed:    false,
		RuleNumber: -1,
		Action:     "deny",
		Reason:     "implicit deny (no matching ACL rule)",
	}
}
