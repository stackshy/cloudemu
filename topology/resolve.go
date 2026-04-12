package topology

import (
	"context"
	"strings"
)

// Resolve walks through DNS zones and returns matching record values for the hostname.
func (e *Engine) Resolve(ctx context.Context, hostname string) ([]string, error) {
	zones, err := e.dns.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	for _, zone := range zones {
		if !isSubdomain(hostname, zone.Name) {
			continue
		}

		records, err := e.dns.ListRecords(ctx, zone.ID)
		if err != nil {
			continue
		}

		for _, rec := range records {
			if rec.Name == hostname && isResolvableType(rec.Type) {
				return rec.Values, nil
			}
		}
	}

	return nil, nil
}

// isSubdomain checks if hostname is equal to or a subdomain of the zone name.
func isSubdomain(hostname, zoneName string) bool {
	hostname = strings.TrimSuffix(hostname, ".")
	zoneName = strings.TrimSuffix(zoneName, ".")

	if hostname == zoneName {
		return true
	}

	return strings.HasSuffix(hostname, "."+zoneName)
}

// isResolvableType returns true for record types that resolve to addresses.
func isResolvableType(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME":
		return true
	default:
		return false
	}
}
