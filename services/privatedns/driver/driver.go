// Package driver defines the storage contract for Azure Private DNS
// (Microsoft.Network/privateDnsZones) and its two child resources —
// virtualNetworkLinks and record sets.
//
// Private DNS is an Azure-only ARM "echo-class" resource with no cross-cloud
// equivalent (AWS Route 53 private hosted zones and GCP Cloud DNS private zones
// have a different shape), so — like AzureFirewalls and AzureLoadBalancers — the
// Azure provider stores the ARM bodies natively and exposes them through this
// dedicated, single-provider interface. AWS and GCP have no counterpart.
//
// The pieces the server-wide unmodeled-property echo cannot preserve are modeled
// explicitly. On a zone the numberOf* counts are computed from the live link and
// record stores, so they cannot be echoed from a request. On a virtual network
// link registrationEnabled is a tri-state pointer: an explicit false must
// round-trip false (the #1 Terraform drift), which a plain bool echo cannot
// distinguish from "unset". On a record set the type-specific record arrays
// (aRecords, cnameRecord, ...) plus metadata round-trip verbatim in RecordData,
// while ttl is modeled as an int64 (so it serializes as an integer, not a JSON
// float) and fqdn / isAutoRegistered are computed on read.
package driver

import "context"

// RecordType names the eight Private DNS record-set types. SOA is seeded
// automatically when a zone is created and can be updated but not created.
const (
	RecordTypeA     = "A"
	RecordTypeAAAA  = "AAAA"
	RecordTypeCNAME = "CNAME"
	RecordTypeMX    = "MX"
	RecordTypePTR   = "PTR"
	RecordTypeSOA   = "SOA"
	RecordTypeSRV   = "SRV"
	RecordTypeTXT   = "TXT"
)

// PrivateZone is a natively-stored Microsoft.Network/privateDnsZones body. Only
// Location, Tags and the ETag are persistent request state; the NumberOf* counts
// are computed on read from the link and record stores.
type PrivateZone struct {
	Name          string
	ResourceGroup string
	Location      string
	Tags          map[string]string
	ETag          string

	// Computed on read from the live child stores.
	NumberOfRecordSets                          int64
	NumberOfVirtualNetworkLinks                 int64
	NumberOfVirtualNetworkLinksWithRegistration int64
}

// VirtualNetworkLink is a natively-stored
// Microsoft.Network/privateDnsZones/virtualNetworkLinks body. RegistrationEnabled
// is a pointer so an explicit false round-trips distinctly from "unset".
type VirtualNetworkLink struct {
	Name                string
	ZoneName            string
	ResourceGroup       string
	Location            string
	Tags                map[string]string
	RegistrationEnabled *bool
	VirtualNetworkID    string
	ETag                string
}

// RecordSet is a natively-stored Private DNS record set. RecordType is one of the
// RecordType* constants; TTL is modeled explicitly to serialize as an integer;
// RecordData carries the type-specific record arrays/objects plus metadata
// verbatim so every record type round-trips without per-type field modeling.
type RecordSet struct {
	Name          string
	ZoneName      string
	ResourceGroup string
	RecordType    string
	TTL           *int64
	RecordData    map[string]any
	ETag          string
}

// PrivateDNS is the Azure-only Private DNS store: zones plus their
// virtualNetworkLinks and record sets. All three are keyed to match ARM
// addressing; a zone Delete cascades to its links and records. CreateOrUpdate is
// a full replace, matching ARM PUT semantics. Link and record writes require the
// parent zone to exist (ParentResourceNotFound otherwise). An empty resourceGroup
// on ListPrivateZones means subscription-wide.
type PrivateDNS interface {
	// CreateOrUpdatePrivateZone stores zone as a full replace and reports whether
	// it did not previously exist (created==true → HTTP 201, else 200). On create
	// a default SOA record set (@) is seeded, so numberOfRecordSets starts at 1.
	CreateOrUpdatePrivateZone(ctx context.Context, rg, name string, zone PrivateZone) (stored *PrivateZone, created bool, err error)
	// GetPrivateZone returns the zone with its computed counts, or NotFound.
	GetPrivateZone(ctx context.Context, rg, name string) (*PrivateZone, error)
	// DeletePrivateZone removes the zone and cascades to its links and records,
	// or returns NotFound.
	DeletePrivateZone(ctx context.Context, rg, name string) error
	// ListPrivateZones returns the zones in rg (all when rg is empty), ordered by
	// key, each with its computed counts.
	ListPrivateZones(ctx context.Context, rg string) ([]PrivateZone, error)

	// CreateOrUpdateVirtualNetworkLink stores link as a full replace under an
	// existing zone, reporting whether it did not previously exist.
	CreateOrUpdateVirtualNetworkLink(
		ctx context.Context, rg, zone, name string, link VirtualNetworkLink,
	) (stored *VirtualNetworkLink, created bool, err error)
	// GetVirtualNetworkLink returns the link, or NotFound.
	GetVirtualNetworkLink(ctx context.Context, rg, zone, name string) (*VirtualNetworkLink, error)
	// DeleteVirtualNetworkLink removes the link, or returns NotFound.
	DeleteVirtualNetworkLink(ctx context.Context, rg, zone, name string) error
	// ListVirtualNetworkLinks returns the links under a zone, ordered by key.
	ListVirtualNetworkLinks(ctx context.Context, rg, zone string) ([]VirtualNetworkLink, error)

	// CreateOrUpdateRecordSet stores rs as a full replace under an existing zone,
	// reporting whether it did not previously exist.
	CreateOrUpdateRecordSet(
		ctx context.Context, rg, zone, recordType, name string, rs RecordSet,
	) (stored *RecordSet, created bool, err error)
	// GetRecordSet returns the record set, or NotFound.
	GetRecordSet(ctx context.Context, rg, zone, recordType, name string) (*RecordSet, error)
	// DeleteRecordSet removes the record set, or returns NotFound.
	DeleteRecordSet(ctx context.Context, rg, zone, recordType, name string) error
	// ListRecordSets returns every record set under a zone, ordered by key.
	ListRecordSets(ctx context.Context, rg, zone string) ([]RecordSet, error)
	// ListRecordSetsByType returns the zone's record sets of one type, ordered by
	// key.
	ListRecordSetsByType(ctx context.Context, rg, zone, recordType string) ([]RecordSet, error)
}
