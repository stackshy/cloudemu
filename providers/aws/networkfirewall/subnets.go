package networkfirewall

import (
	"context"
	"hash/fnv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// azLetters are the zone suffixes the emulator hands out. It has no real
// subnet-to-zone mapping, so a subnet gets a zone derived from its id.
const azLetters = "abcdef"

// zoneForKey returns the Availability Zone a key (a subnet id) prefers: the same
// key always maps to the same zone.
func zoneForKey(region, key string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	return region + string(azLetters[int(h.Sum32()%uint32(len(azLetters)))])
}

// assignSubnet records a subnet's zone and endpoint on the firewall. The zone is
// the one the subnet really sits in when the networking mock knows it, and a
// firewall has at most one subnet per zone, as in AWS. For a subnet the
// networking mock does not know the zone is derived from the id (the next free
// zone if that one is taken). The assignment is stored and never recomputed.
func (m *Mock) assignSubnet(ctx context.Context, fw *nfdriver.Firewall, subnetID, ipType string) error {
	if fw.SubnetDetails == nil {
		fw.SubnetDetails = map[string]nfdriver.SubnetDetail{}
	}

	used := make(map[string]bool, len(fw.SubnetDetails))
	for _, d := range fw.SubnetDetails {
		used[d.AvailabilityZone] = true
	}

	zone := m.resolveZone(ctx, subnetID)

	switch {
	case zone != "" && used[zone]:
		return invalid("firewall %q already has a subnet in Availability Zone %q", fw.Name, zone)
	case zone != "":
	case len(fw.SubnetDetails) >= len(azLetters):
		return invalid("a firewall can have at most %d subnets, one per Availability Zone", len(azLetters))
	default:
		zone = zoneForKey(m.opts.Region, subnetID)

		for i := 0; used[zone] && i < len(azLetters); i++ {
			zone = m.opts.Region + string(azLetters[(indexOfLetter(zone)+1)%len(azLetters)])
		}
	}

	fw.SubnetDetails[subnetID] = nfdriver.SubnetDetail{
		IPAddressType:    ipType,
		AvailabilityZone: zone,
		EndpointID:       endpointID(fw.ID, zone),
	}

	return nil
}

func indexOfLetter(zone string) int {
	for i := range azLetters {
		if zone[len(zone)-1] == azLetters[i] {
			return i
		}
	}

	return 0
}

// endpointID names the firewall endpoint in one zone.
func endpointID(firewallID, zone string) string {
	return "vpce-" + firewallID + "-" + zone[len(zone)-1:]
}

// syncZones returns the zones a firewall runs in: its explicit zones for a
// transit gateway-attached firewall, else the zone of each attached subnet.
func syncZones(fw *nfdriver.Firewall) []string {
	if len(fw.AvailabilityZones) > 0 {
		return cloneStrings(fw.AvailabilityZones)
	}

	zones := make([]string, 0, len(fw.SubnetIDs))
	for _, s := range fw.SubnetIDs {
		zones = append(zones, fw.SubnetDetails[s].AvailabilityZone)
	}

	return zones
}

// validateSubnetID rejects an empty subnet id.
func validateSubnetID(id string) error {
	if id == "" {
		return invalid("SubnetId must not be empty")
	}

	return nil
}

func validateIPType(ipType string) error {
	if ipType != "" && !oneOf(ipType, ipAddressType...) {
		return cerrors.Newf(cerrors.InvalidArgument, "SubnetMapping.IPAddressType %q is not valid", ipType)
	}

	return nil
}

func cloneSubnetDetails(in map[string]nfdriver.SubnetDetail) map[string]nfdriver.SubnetDetail {
	if in == nil {
		return nil
	}

	out := make(map[string]nfdriver.SubnetDetail, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// zoneEndpoints names the endpoint of each Availability Zone of a transit
// gateway-attached firewall, in the same shape as a subnet's endpoint id.
func zoneEndpoints(fw *nfdriver.Firewall) map[string]string {
	if len(fw.AvailabilityZones) == 0 {
		return nil
	}

	out := make(map[string]string, len(fw.AvailabilityZones))
	for _, z := range fw.AvailabilityZones {
		out[z] = endpointID(fw.ID, z)
	}

	return out
}
