package driver

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ICMP type and code bounds. EC2 uses -1 to mean "any type" or "any code".
const (
	minICMPValue = -1
	maxICMPValue = 255
)

// ValidateAWSSecurityRule checks a rule's ports the way EC2 does. TCP and UDP
// use the 0..65535 range check. For ICMP the ports hold the type and code,
// each in -1..255. The all-protocols rule and other protocol numbers ignore
// ports. The AWS provider and the EC2 wire layer both call it.
func ValidateAWSSecurityRule(rule *SecurityRule) error {
	switch strings.ToLower(rule.Protocol) {
	case "tcp", "udp", "6", "17":
		return validatePortRange(rule)
	case protoICMP, protoICMPv6, protoICMPNum, protoICMPv6Num:
		for _, v := range []int{rule.FromPort, rule.ToPort} {
			if v < minICMPValue || v > maxICMPValue {
				return cerrors.Newf(cerrors.InvalidArgument,
					"Invalid value '%d' for ICMP type or code. Must be between %d and %d.", v, minICMPValue, maxICMPValue)
			}
		}
	}

	return nil
}
