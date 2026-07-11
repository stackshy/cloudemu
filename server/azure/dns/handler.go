// Package dns implements the Azure DNS (Microsoft.Network/dnsZones) ARM REST
// API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns clients
// configured with a custom endpoint hit this handler the same way they hit
// management.azure.com, driving the shared dns driver.
//
// Azure DNS shares the Microsoft.Network ARM provider with the VNet handler
// (server/azure/network), but on a disjoint resource type — this handler
// claims dnsZones while the network handler claims virtualNetworks /
// networkSecurityGroups / locations — so registration order between the two is
// unconstrained. Both must register before the permissive BlobStorage
// fallback.
//
// Azure addresses zones by their user-assigned name in the URL
// (…/dnsZones/{zoneName}), while the dns driver keys zones on a generated
// zone-<uuid> id. The handler resolves the SDK-facing name to the driver id via
// a ListZones scan (resolveZoneID) so callers never see the internal id.
//
// Coverage:
//
//	PUT    .../dnsZones/{z}                              — Zones.CreateOrUpdate
//	GET    .../dnsZones/{z}                              — Zones.Get
//	DELETE .../dnsZones/{z}                              — Zones.Delete (LRO, completes inline)
//	GET    .../providers/Microsoft.Network/dnsZones      — Zones.List (subscription scope)
//	GET    .../resourceGroups/{rg}/…/dnsZones            — Zones.ListByResourceGroup
//	PUT    .../dnsZones/{z}/{type}/{name}                — RecordSets.CreateOrUpdate
//	GET    .../dnsZones/{z}/{type}/{name}                — RecordSets.Get
//	DELETE .../dnsZones/{z}/{type}/{name}                — RecordSets.Delete
//	GET    .../dnsZones/{z}/recordsets|all               — RecordSets.ListByDnsZone / ListAllByDnsZone
package dns

import (
	"net/http"
	"strings"

	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const (
	providerName = "Microsoft.Network"
	// typeZones is the RG-scoped resource type. Azure serializes the
	// subscription-scoped list path in lowercase ("dnszones"), so matching is
	// case-insensitive.
	typeZones = "dnsZones"

	// subRecordSets and subAll are the record-set list sub-paths.
	subRecordSets = "recordsets"
	subAll        = "all"
)

// Handler serves Microsoft.Network/dnsZones ARM requests against a dns driver.
type Handler struct {
	dns dnsdriver.DNS
}

// New returns an Azure DNS handler backed by d.
func New(d dnsdriver.DNS) *Handler {
	return &Handler{dns: d}
}

// isZonesType reports whether the ARM resource type is dnsZones, case-
// insensitively (the subscription-scoped list uses lowercase "dnszones").
func isZonesType(resourceType string) bool {
	return strings.EqualFold(resourceType, typeZones)
}

// Matches claims ARM URLs targeting Microsoft.Network/dnsZones. Disjoint from
// the network handler (virtualNetworks / networkSecurityGroups / locations) on
// the same provider, so registration order between them is unconstrained.
// Registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isZonesType(rp.ResourceType)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Collection list: no zone name (subscription- or RG-scoped list).
	if rp.ResourceName == "" {
		h.serveZoneCollection(w, r, &rp)
		return
	}

	switch rp.SubResource {
	case "":
		h.serveZone(w, r, &rp)
	case subRecordSets, subAll:
		h.serveRecordSetCollection(w, r, &rp)
	default:
		// .../dnsZones/{zone}/{recordType}/{name}
		h.serveRecordSet(w, r, &rp)
	}
}

func (h *Handler) serveZoneCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listZones(w, r, rp)
}

func (h *Handler) serveZone(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateZone(w, r, rp)
	case http.MethodGet:
		h.getZone(w, r, rp)
	case http.MethodDelete:
		h.deleteZone(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveRecordSetCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listRecordSets(w, r, rp)
}

func (h *Handler) serveRecordSet(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateRecordSet(w, r, rp)
	case http.MethodGet:
		h.getRecordSet(w, r, rp)
	case http.MethodDelete:
		h.deleteRecordSet(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
