package driver

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// MaxPort is the highest TCP/UDP port a security rule can name.
const MaxPort = 65535

// ICMP protocol names and numbers, shared by the port checks.
const (
	protoICMP      = "icmp"
	protoICMPv6    = "icmpv6"
	protoICMPNum   = "1"
	protoICMPv6Num = "58"
)

// ValidateSecurityRule checks a rule's port range. Both ends must be in
// 0..65535 and FromPort must not exceed ToPort. ICMP and all-protocol rules
// carry no ports, so they are not checked. Providers call it before they
// store a rule.
func ValidateSecurityRule(rule *SecurityRule) error {
	if !usesPorts(rule.Protocol) {
		return nil
	}

	return validatePortRange(rule)
}

// usesPorts reports whether a rule's protocol has a port range.
func usesPorts(protocol string) bool {
	switch strings.ToLower(protocol) {
	case protoICMP, protoICMPv6, protoICMPNum, protoICMPv6Num, "-1", "all":
		return false
	default:
		return true
	}
}

// validatePortRange checks that both ports are in 0..65535 and in order.
func validatePortRange(rule *SecurityRule) error {
	for _, p := range []int{rule.FromPort, rule.ToPort} {
		if p < 0 || p > MaxPort {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Invalid value '%d' for portRange. Must be between 0 and %d.", p, MaxPort)
		}
	}

	if rule.FromPort > rule.ToPort {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid port range %d-%d. The from port must not be greater than the to port.", rule.FromPort, rule.ToPort)
	}

	return nil
}
