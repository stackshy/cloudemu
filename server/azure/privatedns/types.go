package privatedns

// Azure ARM JSON wire structures for Microsoft.Network/privateDnsZones and its
// virtualNetworkLinks and record-set children (api-version 2018-09-01). Only the
// subset a request carries plus the explicitly-computed read-only outputs is
// typed; a record set's type-specific arrays (aRecords, cnameRecord, ...) ride
// through as generic JSON so every record type round-trips verbatim.

const (
	providerName = "Microsoft.Network"

	// typePrivateZones is the ARM resource type this handler claims. Disjoint
	// (case-insensitively) from the public DNS handler's "dnsZones".
	typePrivateZones = "privateDnsZones"

	zoneResourceType   = "Microsoft.Network/privateDnsZones"
	linkResourceType   = "Microsoft.Network/privateDnsZones/virtualNetworkLinks"
	recordResourceType = "Microsoft.Network/privateDnsZones/" // + recordType

	// subVirtualNetworkLinks is the child segment for a vnet link; subAll is the
	// list-all record-set pseudo-type.
	subVirtualNetworkLinks = "virtualNetworkLinks"
	subAll                 = "ALL"

	// provisioningStateSucceeded is the terminal state the SDK poller and
	// Terraform wait for.
	provisioningStateSucceeded = "Succeeded"
	// virtualNetworkLinkStateCompleted is the terminal link state.
	virtualNetworkLinkStateCompleted = "Completed"

	// defaultLocation is the location Private DNS resources always report.
	defaultLocation = "global"

	// Fixed per-zone quota constants real Azure reports on every zone.
	maxNumberOfRecordSets                          = 25000
	maxNumberOfVirtualNetworkLinks                 = 1000
	maxNumberOfVirtualNetworkLinksWithRegistration = 100

	// Property keys computed on read and therefore stripped from a record set's
	// echoed RecordData so they never double up.
	ttlKey              = "ttl"
	fqdnKey             = "fqdn"
	isAutoRegisteredKey = "isAutoRegistered"
	provisioningKey     = "provisioningState"
)

// subResource is an ARM {id} reference (the linked virtual network).
type subResource struct {
	ID string `json:"id,omitempty"`
}

// zonePropsJSON is the computed properties object of a privateDnsZone. Every
// field is read-only; a request that supplies them is ignored.
type zonePropsJSON struct {
	MaxNumberOfRecordSets                          int64  `json:"maxNumberOfRecordSets"`
	NumberOfRecordSets                             int64  `json:"numberOfRecordSets"`
	MaxNumberOfVirtualNetworkLinks                 int64  `json:"maxNumberOfVirtualNetworkLinks"`
	NumberOfVirtualNetworkLinks                    int64  `json:"numberOfVirtualNetworkLinks"`
	MaxNumberOfVirtualNetworkLinksWithRegistration int64  `json:"maxNumberOfVirtualNetworkLinksWithRegistration"`
	NumberOfVirtualNetworkLinksWithRegistration    int64  `json:"numberOfVirtualNetworkLinksWithRegistration"`
	ProvisioningState                              string `json:"provisioningState"`
}

// zoneJSON is the ARM privateDnsZones wire body.
type zoneJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *zonePropsJSON    `json:"properties,omitempty"`
}

// linkPropsJSON is the properties object of a virtualNetworkLink.
// RegistrationEnabled is a pointer with omitempty so a request that omits it is
// distinguishable from an explicit false; a response always sets a non-nil
// pointer, so false is emitted (never swallowed) — the #1 Terraform drift.
type linkPropsJSON struct {
	RegistrationEnabled     *bool        `json:"registrationEnabled,omitempty"`
	VirtualNetwork          *subResource `json:"virtualNetwork,omitempty"`
	VirtualNetworkLinkState string       `json:"virtualNetworkLinkState,omitempty"`
	ProvisioningState       string       `json:"provisioningState,omitempty"`
}

// linkJSON is the ARM virtualNetworkLinks wire body.
type linkJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *linkPropsJSON    `json:"properties,omitempty"`
}

// recordJSON is the ARM record-set wire body. Properties is generic so the
// type-specific record arrays plus metadata coexist with the modeled ttl and the
// computed fqdn/isAutoRegistered/provisioningState in one object.
type recordJSON struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Type       string         `json:"type,omitempty"`
	Etag       string         `json:"etag,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// zoneListResult is the ARM list envelope for privateDnsZones.
type zoneListResult struct {
	Value []zoneJSON `json:"value"`
}

// linkListResult is the ARM list envelope for virtualNetworkLinks.
type linkListResult struct {
	Value []linkJSON `json:"value"`
}

// recordListResult is the ARM list envelope for record sets.
type recordListResult struct {
	Value []recordJSON `json:"value"`
}

// validRecordTypes is the set of record-set types this handler accepts as a
// URL segment (case-insensitive), used to reject a malformed sub-path cleanly.
//
//nolint:gochecknoglobals // fixed, read-only registry of valid record types.
var validRecordTypes = map[string]struct{}{
	"A": {}, "AAAA": {}, "CNAME": {}, "MX": {}, "PTR": {}, "SOA": {}, "SRV": {}, "TXT": {},
}
