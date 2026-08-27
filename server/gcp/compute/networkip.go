package compute

import (
	"context"
	"encoding/binary"
	"net"
)

// subnetNameTag mirrors the tag the GCP VPC wire handler stamps the
// subnetwork's user-facing name into (server/gcp/vpc). Kept as a local copy so
// the compute handler can resolve a subnetwork reference to its stored subnet
// without importing the vpc handler package.
const subnetNameTag = "cloudemu:gcpSubnetName"

// ipReservedLowAddrs is the count of low addresses GCP reserves in every
// subnet (network, gateway, and two more). The broadcast (highest) address is
// reserved separately.
const ipReservedLowAddrs = 4

// privateIPFor decides the private networkIP for a launching instance. An
// explicit networkIP on the request is honored verbatim. Otherwise, when a
// subnetwork is referenced and resolvable to a CIDR, an address is allocated
// from that range (past the reserved low addresses, deduped against instances
// already in the subnet). It returns "" to defer to the provider's synthetic
// allocator when no subnetwork is referenced or the subnet can't be resolved.
func (h *Handler) privateIPFor(ctx context.Context, req *instanceRequest, subnet, zone string) string {
	if ip := firstNetworkIP(req.NetworkInterfaces); ip != "" {
		return ip
	}

	if h.net == nil || subnet == "" {
		return ""
	}

	cidr, ok := h.subnetCIDR(ctx, subnet, zone)
	if !ok || cidr == "" {
		return ""
	}

	return allocateFromCIDR(cidr, h.usedIPsInSubnet(ctx, subnet))
}

// firstNetworkIP returns the first explicit networkIP the caller set on a NIC.
func firstNetworkIP(nics []networkInterface) string {
	for i := range nics {
		if nics[i].NetworkIP != "" {
			return nics[i].NetworkIP
		}
	}

	return ""
}

// subnetCIDR resolves a subnetwork reference (self-link or bare name) to the
// stored subnet's CIDR, matching by the VPC handler's name tag and, when the
// reference carries a region, the subnet's region.
func (h *Handler) subnetCIDR(ctx context.Context, subnetRef, zone string) (string, bool) {
	subnets, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return "", false
	}

	region, name := parseSubnetRef(subnetRef, zone)

	for i := range subnets {
		s := &subnets[i]
		if tagOr(s.Tags, subnetNameTag, lastSegment(s.ID)) != name {
			continue
		}

		if s.AvailabilityZone != "" && region != "" && s.AvailabilityZone != region {
			continue
		}

		return s.CIDRBlock, true
	}

	return "", false
}

// usedIPsInSubnet collects the private IPs already assigned to instances in the
// referenced subnet, so a fresh allocation avoids colliding with them.
func (h *Handler) usedIPsInSubnet(ctx context.Context, subnetRef string) map[string]bool {
	used := make(map[string]bool)

	instances, err := h.compute.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return used
	}

	name := lastSegment(subnetRef)

	for i := range instances {
		if instances[i].PrivateIP != "" && lastSegment(instances[i].SubnetID) == name {
			used[instances[i].PrivateIP] = true
		}
	}

	return used
}

// allocateFromCIDR returns the lowest free IPv4 host address in cidr, skipping
// the reserved low addresses and the broadcast address and any address already
// in use. It returns "" for a non-IPv4 CIDR, a range too small to host a VM, or
// an exhausted range — leaving the caller to fall back to the provider.
func allocateFromCIDR(cidr string, used map[string]bool) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}

	base := ipnet.IP.To4()
	if base == nil || len(ipnet.Mask) != net.IPv4len {
		return ""
	}

	netInt := binary.BigEndian.Uint32(base)
	broadcast := netInt | ^binary.BigEndian.Uint32(ipnet.Mask)

	// The range must hold the reserved low addresses plus at least one host
	// below the broadcast address.
	if broadcast-netInt <= ipReservedLowAddrs {
		return ""
	}

	first := netInt + ipReservedLowAddrs
	last := broadcast - 1 // exclude the broadcast (highest) address

	for v := first; v <= last; v++ {
		var buf [net.IPv4len]byte

		binary.BigEndian.PutUint32(buf[:], v)

		ip := net.IP(buf[:]).String()
		if !used[ip] {
			return ip
		}
	}

	return ""
}
