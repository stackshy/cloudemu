package compute

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/ipalloc"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// subnetNameTag mirrors the tag the GCP VPC wire handler stamps the
// subnetwork's user-facing name into (server/gcp/vpc). Kept as a local copy so
// the compute handler can resolve a subnetwork reference to its stored subnet
// without importing the vpc handler package.
const subnetNameTag = "cloudemu:gcpSubnetName"

// subnetNetworkTag mirrors the VPC handler's tag holding the subnetwork's
// parent network name.
const subnetNetworkTag = "cloudemu:gcpSubnetNet"

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

	used := h.usedIPsInSubnet(ctx, subnet)
	h.addReservedInternalIPs(ctx, subnet, zone, used)

	return ipalloc.FirstFree(cidr, used)
}

// autoSubnetFor resolves the subnetwork a NIC lands in when it names only a
// network: real GCP places it in the network's subnetwork of the instance's
// region, which for an auto mode network is the one named after the network.
// It returns "" when the network has no such subnetwork.
func (h *Handler) autoSubnetFor(ctx context.Context, network, zone string) string {
	if h.net == nil || network == "" {
		return ""
	}

	subnets, err := h.net.DescribeSubnets(projectctx.WithProject(ctx, projectctx.FromPath(network)), nil)
	if err != nil {
		return ""
	}

	netName := lastSegment(network)
	region := regionFromZone(zone)

	for i := range subnets {
		s := &subnets[i]
		if s.Tags[subnetNameTag] == netName && s.Tags[subnetNetworkTag] == netName && s.AvailabilityZone == region {
			return "regions/" + region + "/subnetworks/" + netName
		}
	}

	return ""
}

// addReservedInternalIPs marks the IPs held by INTERNAL addresses reserved in
// the subnet as used, so an instance never takes an IP someone reserved.
func (h *Handler) addReservedInternalIPs(ctx context.Context, subnetRef, zone string, used map[string]bool) {
	store, ok := h.net.(netdriver.GCPAddressStore)
	if !ok {
		return
	}

	region, name := parseSubnetRef(subnetRef, zone)
	project := subnetProject(subnetRef, projectctx.ProjectOr(ctx, ""))

	addrs, err := store.ListGCPAddresses(ctx, project, region)
	if err != nil {
		return
	}

	for i := range addrs {
		var a struct {
			Address    string `json:"address"`
			Subnetwork string `json:"subnetwork"`
		}

		if json.Unmarshal(addrs[i].Body, &a) == nil && a.Address != "" && lastSegment(a.Subnetwork) == name {
			used[a.Address] = true
		}
	}
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
	// A Shared VPC reference names the host project's subnet by URL.
	subnets, err := h.net.DescribeSubnets(projectctx.WithProject(ctx, projectctx.FromPath(subnetRef)), nil)
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
// referenced subnet, so a fresh allocation avoids colliding with them. Every
// project is scanned, because Shared VPC instances of other projects draw from
// the same subnet; an instance counts only when its subnet is in the same
// project as the referenced one.
func (h *Handler) usedIPsInSubnet(ctx context.Context, subnetRef string) map[string]bool {
	used := make(map[string]bool)

	instances, err := h.compute.DescribeInstances(projectctx.AllProjects(ctx), nil, nil)
	if err != nil {
		return used
	}

	name := lastSegment(subnetRef)
	project := subnetProject(subnetRef, projectctx.ProjectOr(ctx, ""))

	for i := range instances {
		inst := &instances[i]
		owner := subnetProject(inst.SubnetID, tagOr(inst.Tags, keyProject, projectctx.ProjectOr(ctx, "")))

		if inst.PrivateIP != "" && lastSegment(inst.SubnetID) == name && owner == project {
			used[instances[i].PrivateIP] = true
		}
	}

	return used
}

// subnetProject returns the project a subnet reference names, or fallback for
// a bare name or a relative reference with no project.
func subnetProject(ref, fallback string) string {
	if p := projectctx.FromPath(ref); p != "" {
		return p
	}

	return fallback
}
