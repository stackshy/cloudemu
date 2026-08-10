package vcn

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strconv"
	"strings"

	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Tags CloudEmu keeps on a resource to carry OCI attributes the portable
// projections have no field for. They are stripped from freeformTags.
const (
	internalTagPrefix   = "cloudemu:"
	tagDisplayName      = internalTagPrefix + "ociDisplayName"
	tagDNSLabel         = internalTagPrefix + "ociDnsLabel"
	tagSecurityLists    = internalTagPrefix + "ociSecurityListIds"
	tagDHCPOptions      = internalTagPrefix + "ociDhcpOptionsId"
	tagProhibitPublicIP = internalTagPrefix + "ociProhibitPublicIpOnVnic"
	tagServiceName      = internalTagPrefix + "ociServiceName"
	tagIsEnabled        = internalTagPrefix + "ociIsEnabled"
	tagBlockTraffic     = internalTagPrefix + "ociBlockTraffic"
	tagRouteTable       = internalTagPrefix + "ociRouteTableId"
)

// OCI protocol numbers, as they appear in security rules.
const (
	ociProtocolICMP = "1"
	ociProtocolTCP  = "6"
	ociProtocolUDP  = "17"
	ociProtocolAll  = "all"
)

// Portable protocol names, as the driver's SecurityRule carries them.
const (
	protocolICMP = "icmp"
	protocolTCP  = "tcp"
	protocolUDP  = "udp"
	protocolAll  = "-1"
)

// lifecycleAvailable is the state resources whose portable projection carries
// none report; every CloudEmu mutation completes synchronously.
const lifecycleAvailable = vcnprovider.StateAvailable

// Security rule vocabulary.
const (
	directionIngress = "INGRESS"
	directionEgress  = "EGRESS"
	entityCIDRBlock  = "CIDR_BLOCK"
	entityServiceGW  = "SERVICE_CIDR_BLOCK"
	entityNSG        = "NETWORK_SECURITY_GROUP"
	cidrAnywhere     = "0.0.0.0/0"
	actionAllow      = "allow"
	trueValue        = "true"
	// ruleIDLen is how much of a rule's digest identifies it, matching the
	// length of the opaque ids real OCI mints for NSG rules.
	ruleIDLen = 20
)

// ocidPrefixParts is the number of dot-separated segments before an OCID's
// resource type.
const ocidPrefixParts = 2

// ocidTypeNSG is the OCID type segment the provider mints NSGs with.
const ocidTypeNSG = "networksecuritygroup"

// pairStride steps a variadic key/value list.
const pairStride = 2

// ocidType returns the resource type segment of an OCID.
func ocidType(id string) string {
	parts := strings.SplitN(id, ".", ocidPrefixParts+1)
	if len(parts) <= ocidPrefixParts {
		return ""
	}

	return parts[1]
}

// withInternal returns the caller's freeform tags plus the internal keys
// carrying OCI attributes, skipping empty values.
func withInternal(freeform map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(freeform)+len(kv))

	for k, v := range freeform {
		if !strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	for i := 0; i+1 < len(kv); i += pairStride {
		if kv[i+1] != "" {
			out[kv[i]] = kv[i+1]
		}
	}

	return out
}

// freeformOf returns the tags a caller set, without CloudEmu's internal keys.
func freeformOf(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))

	for k, v := range tags {
		if !strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	return out
}

// tagOr reads an internal tag, falling back when it is absent.
func tagOr(tags map[string]string, key, fallback string) string {
	if v, ok := tags[key]; ok {
		return v
	}

	return fallback
}

// boolTag reads a boolean internal tag.
func boolTag(tags map[string]string, key string) bool {
	return tags[key] == trueValue
}

// boolText renders a boolean for storage in an internal tag.
func boolText(v bool) string {
	return strconv.FormatBool(v)
}

// firstHostIP is the address OCI reserves for a subnet's virtual router.
func firstHostIP(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	base := ipnet.IP.To4()
	if base == nil {
		return ""
	}

	out := make(net.IP, net.IPv4len)
	copy(out, base)

	out[net.IPv4len-1]++

	return out.String()
}

// domainName joins a DNS label onto its parent domain, OCI style.
func domainName(label, parent string) string {
	if label == "" {
		return ""
	}

	return label + "." + parent
}

// toDriverProtocol maps an OCI protocol number onto the portable name.
func toDriverProtocol(p string) string {
	switch p {
	case ociProtocolTCP:
		return protocolTCP
	case ociProtocolUDP:
		return protocolUDP
	case ociProtocolICMP:
		return protocolICMP
	case ociProtocolAll, "":
		return protocolAll
	default:
		return p
	}
}

// toOCIProtocol maps a portable protocol name onto OCI's number.
func toOCIProtocol(p string) string {
	switch p {
	case protocolTCP:
		return ociProtocolTCP
	case protocolUDP:
		return ociProtocolUDP
	case protocolICMP:
		return ociProtocolICMP
	case protocolAll, "":
		return ociProtocolAll
	default:
		return p
	}
}

// toDriverRule converts a wire security rule into the portable one. Direction
// comes from the caller when the rule does not carry it, as security list
// rules do not.
func toDriverRule(r *securityRule, egress bool) netdriver.SecurityRule {
	out := netdriver.SecurityRule{
		Protocol: toDriverProtocol(r.Protocol),
		CIDR:     r.Source,
	}

	if egress {
		out.CIDR = r.Destination
	}

	if out.CIDR == "" {
		out.CIDR = cidrAnywhere
	}

	if opts := portOptions(r); opts != nil && opts.DestinationPortRange != nil {
		out.FromPort = opts.DestinationPortRange.Min
		out.ToPort = opts.DestinationPortRange.Max
	}

	return out
}

// entityTypeOf classifies what a rule's source or destination names. The
// portable rule carries the value but not its kind, so the kind is read back
// off the value: an OCI service label or NSG OCID is not a CIDR block.
func entityTypeOf(value string) string {
	switch {
	case ocidType(value) == ocidTypeNSG:
		return entityNSG
	case isCIDR(value):
		return entityCIDRBlock
	default:
		return entityServiceGW
	}
}

// isCIDR reports whether a value parses as a CIDR block.
func isCIDR(value string) bool {
	_, _, err := net.ParseCIDR(value)

	return err == nil
}

// portOptions returns whichever protocol option block the rule carries.
func portOptions(r *securityRule) *protocolOptions {
	if r.TCPOptions != nil {
		return r.TCPOptions
	}

	return r.UDPOptions
}

// toWireRule converts a portable security rule back to OCI's shape.
func toWireRule(r *netdriver.SecurityRule, egress bool) securityRule {
	out := securityRule{
		Protocol: toOCIProtocol(r.Protocol),
	}

	if egress {
		out.Direction = directionEgress
		out.Destination = r.CIDR
		out.DestinationType = entityTypeOf(r.CIDR)
	} else {
		out.Direction = directionIngress
		out.Source = r.CIDR
		out.SourceType = entityTypeOf(r.CIDR)
	}

	if r.FromPort != 0 || r.ToPort != 0 {
		opts := &protocolOptions{DestinationPortRange: &portRange{Min: r.FromPort, Max: r.ToPort}}

		switch r.Protocol {
		case protocolUDP:
			out.UDPOptions = opts
		default:
			out.TCPOptions = opts
		}
	}

	out.ID = ruleID(out.Direction, r)

	return out
}

// ruleID derives the opaque handle OCI gives an NSG rule. The portable rule
// carries no identity of its own, so the digest of its contents stands in and
// stays stable for as long as the rule does.
func ruleID(direction string, r *netdriver.SecurityRule) string {
	key := strings.Join([]string{
		direction, r.Protocol, r.CIDR,
		strconv.Itoa(r.FromPort), strconv.Itoa(r.ToPort),
	}, "|")
	sum := sha256.Sum256([]byte(key))

	return hex.EncodeToString(sum[:])[:ruleIDLen]
}

// toWireRoutes converts portable routes into OCI route rules.
func toWireRoutes(routes []netdriver.Route) []routeRule {
	out := make([]routeRule, 0, len(routes))

	for i := range routes {
		out = append(out, routeRule{
			Destination:     routes[i].DestinationCIDR,
			CIDRBlock:       routes[i].DestinationCIDR,
			DestinationType: entityCIDRBlock,
			NetworkEntityID: routes[i].TargetID,
		})
	}

	return out
}

// toDriverRoutes converts OCI route rules into portable routes.
func toDriverRoutes(rules []routeRule) []netdriver.Route {
	out := make([]netdriver.Route, 0, len(rules))

	for i := range rules {
		dest := rules[i].Destination
		if dest == "" {
			dest = rules[i].CIDRBlock
		}

		out = append(out, netdriver.Route{
			DestinationCIDR: dest,
			TargetID:        rules[i].NetworkEntityID,
			TargetType:      vcnprovider.TargetTypeOf(rules[i].NetworkEntityID),
			State:           "active",
		})
	}

	return out
}
