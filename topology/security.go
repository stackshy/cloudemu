package topology

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

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
	egressMatch := matchRules(src.EgressRules, src.ID, port, protocol, "0.0.0.0/0")
	if egressMatch == nil {
		return &TrafficVerdict{
			Allowed: false,
			Reason:  fmt.Sprintf("no egress rule on %s allows %s:%d", src.ID, protocol, port),
		}
	}

	ingressMatch := matchRules(dst.IngressRules, dst.ID, port, protocol, "0.0.0.0/0")
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
	protocol, targetIP string,
) *RuleMatch {
	for _, r := range rules {
		if !protocolMatches(r.Protocol, protocol) {
			continue
		}

		if !portInRange(port, r.FromPort, r.ToPort) {
			continue
		}

		if !ipInCIDR(targetIP, r.CIDR) {
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
