package vpc

import (
	"encoding/binary"
	"fmt"
	"net"
)

// defaultIPv4Base is the space a top-level pool provisions from when a caller
// asks for a netmask-only CIDR (real AWS carves that from a parent pool; this
// single-account emulator has no pool hierarchy, so it hands out from a large
// private base). /8 is ample for any realistic requested netmask.
const defaultIPv4Base = "10.0.0.0/8"

// ipv4Bits is the width of an IPv4 address, used for netmask/host arithmetic.
const ipv4Bits = 32

// ipv4Range returns the inclusive [start,end] address range of an IPv4 CIDR.
func ipv4Range(cidr string) (start, end uint32, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet.IP.To4() == nil {
		return 0, 0, false
	}

	base := binary.BigEndian.Uint32(ipnet.IP.To4())
	ones, bits := ipnet.Mask.Size()
	size := uint32(1) << uint(bits-ones) //nolint:gosec // bits-ones is 0..32, no overflow

	return base, base + size - 1, true
}

// blockFree reports whether the /blockSize block starting at b overlaps none of
// the used ranges.
func blockFree(b, blockSize uint32, used [][2]uint32) bool {
	end := b + blockSize - 1

	for _, u := range used {
		if b <= u[1] && u[0] <= end {
			return false
		}
	}

	return true
}

// nextFreeIPv4Block finds the first netmask-aligned /netmask block inside
// [parentStart,parentEnd] that doesn't overlap any used range, and returns it
// as a CIDR string. Sequential carve: it does not reclaim gaps between used
// ranges beyond simple alignment scanning, which is enough for realistic
// allocate-then-allocate usage.
func nextFreeIPv4Block(parentStart, parentEnd uint32, used [][2]uint32, netmask int) (string, bool) {
	if netmask < 0 || netmask > ipv4Bits {
		return "", false
	}

	blockSize := uint32(1) << uint(ipv4Bits-netmask) //nolint:gosec // 32-netmask is 0..32, no overflow

	// Align the first candidate up to a blockSize boundary.
	start := parentStart
	if rem := start % blockSize; rem != 0 {
		start += blockSize - rem
	}

	for b := start; b >= parentStart && b+blockSize-1 <= parentEnd; b += blockSize {
		if blockFree(b, blockSize, used) {
			ip := make(net.IP, net.IPv4len)
			binary.BigEndian.PutUint32(ip, b)

			return fmt.Sprintf("%s/%d", ip.String(), netmask), true
		}

		if b+blockSize < b { // guard against uint32 wraparound at the top of the space
			break
		}
	}

	return "", false
}

// usedRanges converts a list of CIDR strings to inclusive IPv4 ranges, skipping
// any that aren't parseable IPv4.
func usedRanges(cidrs []string) [][2]uint32 {
	out := make([][2]uint32, 0, len(cidrs))

	for _, c := range cidrs {
		if s, e, ok := ipv4Range(c); ok {
			out = append(out, [2]uint32{s, e})
		}
	}

	return out
}

// deriveAllocationCIDR carves a free /netmask block from a pool's provisioned
// supply, skipping blocks already handed out as allocations. Caller holds mu.
func (m *Mock) deriveAllocationCIDR(poolID string, netmask int) (string, bool) {
	used := usedRanges(m.poolAllocationCIDRs(poolID))

	for _, sup := range m.poolProvisionedCIDRs(poolID) {
		s, e, ok := ipv4Range(sup)
		if !ok {
			continue
		}

		if cidr, found := nextFreeIPv4Block(s, e, used, netmask); found {
			return cidr, true
		}
	}

	return "", false
}

// deriveProvisionCIDR synthesizes a /netmask block for a top-level pool from
// the default private base, skipping CIDRs already provisioned into the pool.
// Caller holds mu.
func (m *Mock) deriveProvisionCIDR(_ string, netmask int) (string, bool) {
	s, e, _ := ipv4Range(defaultIPv4Base)
	// Every top-level pool carves from the same shared base, so a new block must
	// avoid CIDRs already provisioned into ANY pool — otherwise two pools each
	// asking for a /16 would both receive 10.0.0.0/16 and overlap, defeating
	// IPAM's non-overlap guarantee.
	used := usedRanges(m.allProvisionedCIDRs())

	return nextFreeIPv4Block(s, e, used, netmask)
}

// poolProvisionedCIDRs / poolAllocationCIDRs list the non-empty CIDR strings a
// pool has provisioned / allocated. Caller holds mu.
func (m *Mock) poolProvisionedCIDRs(poolID string) []string {
	var out []string

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if m.ipamPoolByCidr[c.ID] == poolID && c.CIDR != "" {
			out = append(out, c.CIDR)
		}
	}

	return out
}

// allProvisionedCIDRs lists every non-empty provisioned CIDR across all pools.
// Used to keep provisioning non-overlapping when pools share a carve base.
// Caller holds mu.
func (m *Mock) allProvisionedCIDRs() []string {
	var out []string

	for _, c := range m.ipamPoolCidrs.SortedValues() {
		if c.CIDR != "" {
			out = append(out, c.CIDR)
		}
	}

	return out
}

func (m *Mock) poolAllocationCIDRs(poolID string) []string {
	var out []string

	for _, a := range m.ipamAllocations.SortedValues() {
		if m.ipamPoolByAllocation[a.ID] == poolID && a.CIDR != "" {
			out = append(out, a.CIDR)
		}
	}

	return out
}
