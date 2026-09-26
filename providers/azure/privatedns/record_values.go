package privatedns

import (
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// validateAddresses rejects an A or AAAA record set whose entries are not
// addresses of the right family. The entries sit in RecordData as the ARM
// body sent them (aRecords[].ipv4Address, aaaaRecords[].ipv6Address).
func validateAddresses(recordType string, data map[string]any) error {
	var arrayKey, field string

	switch strings.ToUpper(recordType) {
	case "A":
		arrayKey, field = "aRecords", "ipv4Address"
	case "AAAA":
		arrayKey, field = "aaaaRecords", "ipv6Address"
	default:
		return nil
	}

	return dnsdriver.ValidateAddresses(recordType, entryValues(data[arrayKey], field))
}

// entryValues reads field from each entry of an ARM record array. A missing
// or non-string field reads as "".
func entryValues(raw any, field string) []string {
	var entries []map[string]any

	switch v := raw.(type) {
	case []map[string]any:
		entries = v
	case []any:
		for _, e := range v {
			m, _ := e.(map[string]any)
			entries = append(entries, m)
		}
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		s, _ := e[field].(string)
		out = append(out, s)
	}

	return out
}
