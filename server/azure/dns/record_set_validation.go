package dns

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// writeInvalidAddress rejects an A or AAAA record set whose values are not
// addresses of the right family. Real Azure DNS answers 400 BadRequest and
// stores nothing, so one bad value fails the whole record set. It reports
// whether it wrote a response. It reads the raw entries, since recordValues
// drops empty ones and an entry with no address is also a bad request.
func writeInvalidAddress(w http.ResponseWriter, recordType string, props *recordSetProperties) bool {
	values := rawAddresses(recordType, props)

	i, bad := dnsdriver.InvalidAddressIndex(recordType, values)
	if !bad {
		return false
	}

	field := "ipv4Address"
	family := "IPv4"

	if strings.EqualFold(recordType, recTypeAAAA) {
		field = "ipv6Address"
		family = "IPv6"
	}

	msg := "The resource record is missing field '" + field + "'."
	if values[i] != "" {
		msg = "The value '" + values[i] + "' of field '" + field + "' is not a valid " + family + " address."
	}

	azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", msg)

	return true
}

// rawAddresses returns the address of every A or AAAA entry in the body, empty
// ones included.
func rawAddresses(recordType string, props *recordSetProperties) []string {
	if props == nil {
		return nil
	}

	var out []string

	switch strings.ToUpper(recordType) {
	case recTypeA:
		for _, a := range props.ARecords {
			out = append(out, a.IPv4Address)
		}
	case recTypeAAAA:
		for _, a := range props.AaaaRecords {
			out = append(out, a.IPv6Address)
		}
	}

	return out
}
