package driver

import "hash/fnv"

// azLetters are the zone suffixes used to give a firewall's subnets stable,
// synthetic Availability Zone names (the emulator has no real subnet to AZ
// mapping).
const azLetters = "abcdef"

// AvailabilityZoneFor returns the synthetic Availability Zone name for the
// index-th subnet of a firewall in region (index 0 -> "<region>a").
func AvailabilityZoneFor(region string, index int) string {
	if index < 0 {
		index = -index
	}

	return region + string(azLetters[index%len(azLetters)])
}

// SyncZones returns the Availability Zones a firewall runs in: its explicit
// zones for a transit gateway-attached firewall, else one synthetic zone per
// subnet.
func SyncZones(f *Firewall, region string) []string {
	if len(f.AvailabilityZones) > 0 {
		return append([]string(nil), f.AvailabilityZones...)
	}

	zones := make([]string, 0, len(f.SubnetIDs))
	for i := range f.SubnetIDs {
		zones = append(zones, AvailabilityZoneFor(region, i))
	}

	return zones
}

// ZoneForKey returns a stable synthetic Availability Zone name for an opaque key
// (a subnet id): the same key always maps to the same zone.
func ZoneForKey(region, key string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	return AvailabilityZoneFor(region, int(h.Sum32()%uint32(len(azLetters))))
}
