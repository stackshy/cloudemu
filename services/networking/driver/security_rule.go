package driver

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// MaxPort is the highest TCP/UDP port a security rule can name.
const MaxPort = 65535

// ValidateSecurityRule checks a rule's port range. Both ends must be in
// 0..65535 and FromPort must not exceed ToPort. Providers call it before
// they store a rule.
func ValidateSecurityRule(rule *SecurityRule) error {
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
