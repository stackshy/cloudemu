package vpclattice

import (
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Enum members and limits copied from the aws-sdk-go-v2 vpclattice model.
const (
	authTypeAWSIAM = "AWS_IAM"

	targetGroupTypeIP       = "IP"
	targetGroupTypeLambda   = "LAMBDA"
	targetGroupTypeInstance = "INSTANCE"
	targetGroupTypeALB      = "ALB"

	listenerProtocolHTTP           = "HTTP"
	listenerProtocolHTTPS          = "HTTPS"
	listenerProtocolTLSPassthrough = "TLS_PASSTHROUGH"

	defaultHTTPPort  = 80
	defaultHTTPSPort = 443
	maxPort          = 65535

	minRulePriority = 1
	maxRulePriority = 2000
)

func invalidf(format string, args ...any) error {
	return errors.Newf(errors.InvalidArgument, format, args...)
}

// validateAuthType accepts the empty string (the caller applies the default).
func validateAuthType(authType string) error {
	switch authType {
	case "", authTypeNone, authTypeAWSIAM:
		return nil
	default:
		return invalidf("authType %q is invalid; must be one of %s, %s", authType, authTypeNone, authTypeAWSIAM)
	}
}

func validateTargetGroupType(typ string) error {
	switch typ {
	case targetGroupTypeIP, targetGroupTypeLambda, targetGroupTypeInstance, targetGroupTypeALB:
		return nil
	default:
		return invalidf("target group type %q is invalid; must be one of %s, %s, %s, %s",
			typ, targetGroupTypeIP, targetGroupTypeLambda, targetGroupTypeInstance, targetGroupTypeALB)
	}
}

// validateListener checks protocol and port and returns the effective port: an
// omitted (zero) port defaults to 80 for HTTP and 443 for HTTPS.
func validateListener(protocol string, port int32) (int32, error) {
	switch protocol {
	case listenerProtocolHTTP, listenerProtocolHTTPS, listenerProtocolTLSPassthrough:
	default:
		return 0, invalidf("listener protocol %q is invalid; must be one of %s, %s, %s",
			protocol, listenerProtocolHTTP, listenerProtocolHTTPS, listenerProtocolTLSPassthrough)
	}

	if port < 0 || port > maxPort {
		return 0, invalidf("listener port %d is out of range 1-%d", port, maxPort)
	}

	if port != 0 {
		return port, nil
	}

	// The API documents defaults for HTTP and HTTPS only, and a listener's port
	// is always 1-65535, so TLS_PASSTHROUGH needs an explicit port.
	switch protocol {
	case listenerProtocolHTTP:
		return defaultHTTPPort, nil
	case listenerProtocolHTTPS:
		return defaultHTTPSPort, nil
	default:
		return 0, invalidf("listener port is required for protocol %s", protocol)
	}
}

func validateRulePriority(priority int32) error {
	if priority < minRulePriority || priority > maxRulePriority {
		return invalidf("rule priority %d is out of range %d-%d", priority, minRulePriority, maxRulePriority)
	}

	return nil
}

// validatePolicyJSON requires the auth policy to be a JSON object.
func validatePolicyJSON(policy string) error {
	var obj map[string]any

	if err := json.Unmarshal([]byte(strings.TrimSpace(policy)), &obj); err != nil || obj == nil {
		return invalidf("policy must be a valid JSON object")
	}

	return nil
}
