package driver

import (
	"net"
	"strings"
)

// InvalidAddressIndex returns the index of the first A or AAAA value that is
// not an address of the right family. An A value must be IPv4 and an AAAA
// value must be IPv6. ok is false when every value is valid or the type holds
// no addresses.
func InvalidAddressIndex(recordType string, values []string) (idx int, ok bool) {
	var v4 bool

	switch strings.ToUpper(recordType) {
	case "A":
		v4 = true
	case "AAAA":
		v4 = false
	default:
		return 0, false
	}

	for i, v := range values {
		ip := net.ParseIP(v)
		if ip == nil || (ip.To4() != nil) != v4 {
			return i, true
		}
	}

	return 0, false
}
