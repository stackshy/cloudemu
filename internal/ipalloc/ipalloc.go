// Package ipalloc hands out host addresses from an IPv4 subnet range the way
// GCP does, so instances and reserved internal addresses draw from the same
// rules and never collide.
package ipalloc

import (
	"encoding/binary"
	"net"
	"sync"
)

// subnetLocks holds one mutex per subnet key. It is process-wide because the
// handlers that allocate from a subnet (reserved addresses, instance launches)
// are separate and must all serialize on the same lock.
var subnetLocks sync.Map //nolint:gochecknoglobals // shared by every allocator of a subnet

// LockSubnet serializes IP allocation in one subnet. Callers hold it from
// reading the subnet's used IPs until the new holder is stored, so two
// concurrent allocations never pick the same free IP. It returns the unlock
// function.
func LockSubnet(project, region, subnet string) func() {
	v, _ := subnetLocks.LoadOrStore(project+"/"+region+"/"+subnet, &sync.Mutex{})
	mu, _ := v.(*sync.Mutex)
	mu.Lock()

	return mu.Unlock
}

// reservedLowAddrs is the count of low addresses GCP reserves in every subnet
// (network, gateway, and two more). The broadcast (highest) address is
// reserved separately.
const reservedLowAddrs = 4

// FirstFree returns the lowest free IPv4 host address in cidr, skipping the
// reserved low addresses, the broadcast address and any address in used. It
// returns "" for a non-IPv4 CIDR, a range too small to host one address, or an
// exhausted range.
func FirstFree(cidr string, used map[string]bool) string {
	first, last, ok := hostRange(cidr)
	if !ok {
		return ""
	}

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

// Usable reports whether ip is an assignable host address of cidr: inside the
// range and not one of the reserved low or broadcast addresses.
func Usable(cidr, ip string) bool {
	first, last, ok := hostRange(cidr)
	if !ok {
		return false
	}

	v4 := net.ParseIP(ip).To4()
	if v4 == nil {
		return false
	}

	v := binary.BigEndian.Uint32(v4)

	return v >= first && v <= last
}

// hostRange returns the first and last assignable addresses of an IPv4 cidr.
func hostRange(cidr string) (first, last uint32, ok bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, 0, false
	}

	base := ipnet.IP.To4()
	if base == nil || len(ipnet.Mask) != net.IPv4len {
		return 0, 0, false
	}

	netInt := binary.BigEndian.Uint32(base)
	broadcast := netInt | ^binary.BigEndian.Uint32(ipnet.Mask)

	// The range must hold the reserved low addresses plus at least one host
	// below the broadcast address.
	if broadcast-netInt <= reservedLowAddrs {
		return 0, 0, false
	}

	return netInt + reservedLowAddrs, broadcast - 1, true
}
