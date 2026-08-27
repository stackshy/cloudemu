package vnet

import (
	"context"
	"net"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// netcfgInvalidSubnetCode is the real ARM error code for both classes of
// subnet CIDR violation this file rejects: a prefix outside the vnet's
// address space, and a prefix overlapping a sibling subnet. Verified against
// reports of the real error (azure-cli #10917; Azure/azure-service-operator
// #997): the wire code is NetcfgInvalidSubnet, HTTP 400, with message
// "Subnet '{name}' is not valid in virtual network '{vnet}'", quoted verbatim.
const netcfgInvalidSubnetCode = "NetcfgInvalidSubnet"

// validateSubnetCIDR checks a candidate subnet prefix the way real ARM does
// before ever storing it: it must parse as a valid CIDR, fall entirely within
// one of the vnet's address-space prefixes, and not overlap any sibling
// subnet already in the same vnet (subnetName itself is excluded from the
// sibling scan so a same-CIDR re-PUT of an existing subnet isn't flagged as
// overlapping itself).
func (h *Handler) validateSubnetCIDR(ctx context.Context, vpcID, subnetName, cidr string) error {
	_, candidate, err := net.ParseCIDR(cidr)
	if err != nil {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid address prefix %q", cidr)
	}

	if !cidrWithinAny(candidate, h.vnetAddressPrefixesByID(ctx, vpcID)) {
		return azurearmSubnetError(subnetName,
			"subnet '"+subnetName+"' address range does not fall within the virtual network's address space")
	}

	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return err
	}

	if overlapsSiblingSubnet(candidate, subs, vpcID, subnetName) {
		return azurearmSubnetError(subnetName,
			"subnet '"+subnetName+"' address range overlaps an existing subnet in the same virtual network")
	}

	return nil
}

// cidrWithinAny reports whether candidate falls entirely within at least one
// of the given address-space prefix strings.
func cidrWithinAny(candidate *net.IPNet, prefixes []string) bool {
	for _, p := range prefixes {
		_, parent, err := net.ParseCIDR(p)
		if err != nil {
			continue
		}

		if cidrWithin(candidate, parent) {
			return true
		}
	}

	return false
}

// overlapsSiblingSubnet reports whether candidate overlaps any other subnet
// already stored in the same vnet (subnetName itself is excluded so a
// same-CIDR re-PUT of an existing subnet isn't flagged as overlapping itself).
func overlapsSiblingSubnet(candidate *net.IPNet, subs []netdriver.SubnetInfo, vpcID, subnetName string) bool {
	for i := range subs {
		if subs[i].VPCID != vpcID || tagOr(subs[i].Tags, armSubnetTag, "") == subnetName {
			continue
		}

		_, sibling, err := net.ParseCIDR(subs[i].CIDRBlock)
		if err != nil {
			continue
		}

		if sibling.Contains(candidate.IP) || candidate.Contains(sibling.IP) {
			return true
		}
	}

	return false
}

// azurearmSubnetError is a plain error carrying the NetcfgInvalidSubnet
// message; the server layer maps it to the wire response via
// writeSubnetValidationError since cerrors has no ARM-code-carrying variant.
type azurearmSubnetErr struct {
	subnetName string
	msg        string
}

func (e *azurearmSubnetErr) Error() string { return e.msg }

func azurearmSubnetError(subnetName, msg string) error {
	return &azurearmSubnetErr{subnetName: subnetName, msg: msg}
}

// writeSubnetValidationError renders a subnet-CIDR validation failure as the
// real ARM 400 NetcfgInvalidSubnet, falling back to the generic canonical-error
// mapping for anything else (so callers can pass any error from
// validateSubnetCIDR straight through).
func writeSubnetValidationError(w http.ResponseWriter, err error) {
	if _, ok := err.(*azurearmSubnetErr); ok { //nolint:errorlint // sentinel-free local type, never wrapped
		azurearm.WriteError(w, http.StatusBadRequest, netcfgInvalidSubnetCode, err.Error())
		return
	}

	azurearm.WriteCErr(w, err)
}

// vnetAddressPrefixesByID returns the full address-space prefix list for the
// vnet with driver id vpcID, preferring the Azure metadata store (which keeps
// every prefix) and falling back to the cross-cloud single CIDR.
func (h *Handler) vnetAddressPrefixesByID(ctx context.Context, vpcID string) []string {
	if meta, ok := h.azureMeta(); ok {
		if md, found := meta.GetAzureVNetMetadata(ctx, vpcID); found && len(md.AddressPrefixes) > 0 {
			return md.AddressPrefixes
		}
	}

	vpcs, err := h.net.DescribeVPCs(ctx, nil)
	if err != nil {
		return nil
	}

	for i := range vpcs {
		if vpcs[i].ID == vpcID {
			return []string{vpcs[i].CIDRBlock}
		}
	}

	return nil
}

// cidrWithin reports whether candidate's entire address range falls inside
// parent: same IP family, a mask at least as specific, and both the network
// and broadcast addresses of candidate contained in parent.
func cidrWithin(candidate, parent *net.IPNet) bool {
	cOnes, cBits := candidate.Mask.Size()
	pOnes, pBits := parent.Mask.Size()

	if cBits != pBits {
		return false
	}

	if cOnes < pOnes {
		return false
	}

	return parent.Contains(candidate.IP) && parent.Contains(lastAddr(candidate))
}

// lastAddr returns the broadcast (highest) address of an IPv4 CIDR block.
func lastAddr(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	if ip == nil {
		ip = n.IP
	}

	mask := n.Mask
	last := make(net.IP, len(ip))

	for i := range ip {
		last[i] = ip[i] | ^mask[i]
	}

	return last
}

// availableIPSuggestionLimit caps the alternate addresses CheckIPAddressAvailability
// suggests when the queried address is taken, matching the real API's sample
// response (5 suggestions).
const availableIPSuggestionLimit = 5

// checkIPAddrReservedHosts mirrors the Azure VNet subnet-address FAQ: the
// first four host addresses of every subnet are reserved (network address,
// default gateway, two Azure-internal), so suggestions start past them.
const checkIPAddrReservedHosts = 4

// checkIPAddressAvailability serves VirtualNetworksClient.CheckIPAddressAvailability:
// GET .../virtualNetworks/{name}/CheckIPAddressAvailability?ipAddress={ip}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) checkIPAddressAvailability(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	vnet, err := findVNetByName(r.Context(), h.net, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	ipAddress := r.URL.Query().Get("ipAddress")

	ip := net.ParseIP(ipAddress).To4()
	if ip == nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "invalid ipAddress query parameter")
		return
	}

	subs, err := h.net.DescribeSubnets(r.Context(), nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	target := containingSubnet(subs, vnet.ID, ip)

	result := ipAddressAvailabilityResult{Available: true}

	if target != nil {
		used := h.usedIPsInSubnet(r.Context(), target.ID)

		if used[ipAddress] {
			result.Available = false
			result.AvailableIPAddresses = nextFreeIPs(target.CIDRBlock, used, availableIPSuggestionLimit)
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, result)
}

// containingSubnet returns the subnet of vpcID whose CIDR block contains ip,
// or nil when no subnet in the vnet covers it.
func containingSubnet(subs []netdriver.SubnetInfo, vpcID string, ip net.IP) *netdriver.SubnetInfo {
	for i := range subs {
		if subs[i].VPCID != vpcID {
			continue
		}

		_, cidr, err := net.ParseCIDR(subs[i].CIDRBlock)
		if err != nil {
			continue
		}

		if cidr.Contains(ip) {
			return &subs[i]
		}
	}

	return nil
}

// usedIPsInSubnet collects the private IPs already assigned to any NIC
// ipConfiguration in the subnet identified by its driver id.
func (h *Handler) usedIPsInSubnet(ctx context.Context, subnetDriverID string) map[string]bool {
	used := make(map[string]bool)

	svc, ok := h.net.(netdriver.AzureNetworkInterfaces)
	if !ok {
		return used
	}

	nics, err := svc.ListNetworkInterfaces(ctx, "")
	if err != nil {
		return used
	}

	subs, err := h.net.DescribeSubnets(ctx, nil)
	if err != nil {
		return used
	}

	for i := range nics {
		for j := range nics[i].IPConfigs {
			if nics[i].IPConfigs[j].PrivateIP == "" {
				continue
			}

			if resolveSubnetDriverID(ctx, h.net, subs, nics[i].IPConfigs[j].SubnetID) == subnetDriverID {
				used[nics[i].IPConfigs[j].PrivateIP] = true
			}
		}
	}

	return used
}

// nextFreeIPs returns up to limit host addresses in cidr, starting past
// Azure's reserved addresses, that are not present in used.
func nextFreeIPs(cidr string, used map[string]bool, limit int) []string {
	base, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}

	baseV4 := base.To4()
	if baseV4 == nil {
		return nil
	}

	out := make([]string, 0, limit)

	for host := checkIPAddrReservedHosts; len(out) < limit; host++ {
		ip := addHostOffset(baseV4, host)
		if !ipNet.Contains(ip) {
			break
		}

		s := ip.String()
		if !used[s] {
			out = append(out, s)
		}
	}

	return out
}

// addHostOffset returns base (an IPv4 network address) plus n host addresses.
func addHostOffset(base net.IP, n int) net.IP {
	const octetSpan = 256

	v4 := base.To4()
	out := make(net.IP, len(v4))
	copy(out, v4)

	carry := n
	for i := len(out) - 1; i >= 0 && carry > 0; i-- {
		sum := int(out[i]) + carry
		out[i] = byte(sum % octetSpan) //nolint:gosec // sum % 256 is always within byte range
		carry = sum / octetSpan
	}

	return out
}

// ipAddressAvailabilityResult is the wire shape of IPAddressAvailabilityResult.
type ipAddressAvailabilityResult struct {
	Available            bool     `json:"available"`
	AvailableIPAddresses []string `json:"availableIPAddresses,omitempty"`
}
