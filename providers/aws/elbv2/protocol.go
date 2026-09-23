package elbv2

import (
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// ELBv2 protocols by load balancer type. The full enum is their union plus
// GENEVE, which only target groups use.
//
//nolint:gochecknoglobals // read-only lookup tables.
var (
	albProtocols = []string{protocolHTTP, "HTTPS"}
	nlbProtocols = []string{"TCP", "TLS", "UDP", "TCP_UDP", "QUIC", "TCP_QUIC"}
)

const protocolGENEVE = "GENEVE"

// validProtocol reports whether p is in the ELBv2 protocol enum.
func validProtocol(p string) bool {
	return p == protocolGENEVE || slices.Contains(albProtocols, p) || slices.Contains(nlbProtocols, p)
}

// validateTargetGroupProtocol checks a target group's protocol. A lambda
// target group takes none. Any other protocol must be in the enum.
func validateTargetGroupProtocol(protocol, targetType string) error {
	if targetType == targetTypeLambda {
		if protocol != "" {
			return errors.New(errors.InvalidArgument,
				"Protocol cannot be specified for target groups with target type 'lambda'")
		}

		return nil
	}

	if protocol != "" && !validProtocol(protocol) {
		return invalidProtocol(protocol)
	}

	return nil
}

// validateListenerProtocol checks a listener protocol against its load
// balancer type. An empty type means an application load balancer.
func validateListenerProtocol(lbType, protocol string) error {
	if protocol == "" {
		return nil
	}

	if !validProtocol(protocol) {
		return invalidProtocol(protocol)
	}

	var allowed []string

	switch lbType {
	case "network":
		allowed = nlbProtocols
	case "gateway":
		return errors.New(errors.InvalidArgument, "A protocol cannot be specified for a Gateway Load Balancer listener")
	default:
		allowed = albProtocols
	}

	if !slices.Contains(allowed, protocol) {
		return errors.Newf(errors.InvalidArgument,
			"The value of 'Protocol' must be one of [%s] for a load balancer of type '%s'",
			strings.Join(allowed, ", "), typeName(lbType))
	}

	return nil
}

// validateNewListener checks a CreateListener request.
func validateNewListener(lbType, protocol string, certCount int) error {
	if err := validateListenerProtocol(lbType, protocol); err != nil {
		return err
	}

	if err := validateCertificateCount(certCount); err != nil {
		return err
	}

	return validateListenerCertificates(protocol, certCount)
}

// validateModifiedListener checks a listener after a ModifyListener request is
// merged into it. requested is the number of certificates the request named.
func validateModifiedListener(lbType string, li *driver.ListenerInfo, requested int) error {
	if err := validateListenerProtocol(lbType, li.Protocol); err != nil {
		return err
	}

	if err := validateCertificateCount(requested); err != nil {
		return err
	}

	return validateListenerCertificates(li.Protocol, len(li.Certificates))
}

// validateListenerCertificates checks that an HTTPS or TLS listener has a
// default certificate.
func validateListenerCertificates(protocol string, certCount int) error {
	if (protocol == "HTTPS" || protocol == "TLS") && certCount == 0 {
		return errors.Newf(errors.InvalidArgument, "A certificate must be specified for %s listeners", protocol)
	}

	return nil
}

// validateCertificateCount rejects a request that names more than one default
// certificate. Real ELBv2 takes exactly one here.
func validateCertificateCount(certCount int) error {
	if certCount > 1 {
		return errors.New(errors.InvalidArgument, "You can specify only one default certificate for a listener")
	}

	return nil
}

func typeName(lbType string) string {
	if lbType == "" {
		return "application"
	}

	return lbType
}

func invalidProtocol(protocol string) error {
	return errors.Newf(errors.InvalidArgument,
		"1 validation error detected: Value '%s' at 'protocol' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [HTTP, HTTPS, TCP, TLS, UDP, TCP_UDP, GENEVE, QUIC, TCP_QUIC]", protocol)
}
