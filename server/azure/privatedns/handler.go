// Package privatedns implements the Azure Private DNS
// (Microsoft.Network/privateDnsZones) ARM REST API — private zones,
// virtualNetworkLinks and record sets — as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns
// clients and the Terraform azurerm provider, configured with a custom endpoint,
// hit this handler the same way they hit management.azure.com, driving the
// dedicated privatedns driver.
//
// Private DNS shares the Microsoft.Network ARM provider with the VNet, public
// DNS, load-balancer, app-gateway and firewall handlers, but claims a disjoint
// resource type (privateDnsZones — distinct from the public DNS handler's
// dnsZones even case-insensitively), so registration order between them is
// unconstrained. It must register before the permissive BlobStorage fallback.
//
// Coverage:
//
//	PUT    .../privateDnsZones/{z}                          — PrivateZones.CreateOrUpdate (LRO, sync 201/200)
//	PATCH  .../privateDnsZones/{z}                          — PrivateZones.Update (tags replace)
//	GET    .../privateDnsZones/{z}                          — PrivateZones.Get
//	DELETE .../privateDnsZones/{z}                          — PrivateZones.Delete (LRO, sync-200)
//	GET    .../providers/Microsoft.Network/privateDnsZones  — PrivateZones.List
//	GET    .../resourceGroups/{rg}/…/privateDnsZones        — PrivateZones.ListByResourceGroup
//	PUT    .../privateDnsZones/{z}/virtualNetworkLinks/{l}  — VirtualNetworkLinks.CreateOrUpdate
//	GET    .../privateDnsZones/{z}/virtualNetworkLinks/{l}  — VirtualNetworkLinks.Get
//	PATCH  .../privateDnsZones/{z}/virtualNetworkLinks/{l}  — VirtualNetworkLinks.Update (tags replace)
//	DELETE .../privateDnsZones/{z}/virtualNetworkLinks/{l}  — VirtualNetworkLinks.Delete
//	GET    .../privateDnsZones/{z}/virtualNetworkLinks      — VirtualNetworkLinks.List
//	PUT    .../privateDnsZones/{z}/{type}/{name}            — RecordSets.CreateOrUpdate
//	GET    .../privateDnsZones/{z}/{type}/{name}            — RecordSets.Get
//	DELETE .../privateDnsZones/{z}/{type}/{name}            — RecordSets.Delete
//	GET    .../privateDnsZones/{z}/{type}                   — RecordSets.ListByType
//	GET    .../privateDnsZones/{z}/ALL                      — RecordSets.List
package privatedns

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	pddriver "github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// Handler serves Microsoft.Network/privateDnsZones ARM requests against a
// Private DNS driver.
type Handler struct {
	pdns pddriver.PrivateDNS
}

// New returns an Azure Private DNS handler backed by d.
func New(d pddriver.PrivateDNS) *Handler {
	return &Handler{pdns: d}
}

// isPrivateZonesType reports whether the ARM resource type is privateDnsZones,
// case-insensitively (the subscription-scoped list uses lowercase).
func isPrivateZonesType(resourceType string) bool {
	return strings.EqualFold(resourceType, typePrivateZones)
}

// isRecordType reports whether seg names one of the eight record-set types.
func isRecordType(seg string) bool {
	_, ok := validRecordTypes[strings.ToUpper(seg)]

	return ok
}

// Matches claims ARM URLs targeting Microsoft.Network/privateDnsZones. Disjoint
// from the public DNS handler (dnsZones) and every other Microsoft.Network
// handler, so registration order is unconstrained. Registered before the
// BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && isPrivateZonesType(rp.ResourceType)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.serveZoneCollection(w, r, &rp)
		return
	}

	switch {
	case rp.SubResource == "":
		h.serveZone(w, r, &rp)
	case strings.EqualFold(rp.SubResource, subVirtualNetworkLinks):
		h.serveLink(w, r, &rp)
	case strings.EqualFold(rp.SubResource, subAll):
		h.serveRecordListAll(w, r, &rp)
	case isRecordType(rp.SubResource):
		h.serveRecord(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"unsupported private dns sub-resource "+rp.SubResource)
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
	case http.MethodPatch:
		h.updateZoneTags(w, r, rp)
	case http.MethodGet:
		h.getZone(w, r, rp)
	case http.MethodDelete:
		h.deleteZone(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveLink(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listLinks(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateLink(w, r, rp)
	case http.MethodPatch:
		h.updateLinkTags(w, r, rp)
	case http.MethodGet:
		h.getLink(w, r, rp)
	case http.MethodDelete:
		h.deleteLink(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveRecord(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	// A type-only path (…/{type} with no record name) is a type-filtered list.
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listRecordsByType(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateRecord(w, r, rp)
	case http.MethodGet:
		h.getRecord(w, r, rp)
	case http.MethodDelete:
		h.deleteRecord(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveRecordListAll(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listRecordsAll(w, r, rp)
}

// PurgeResourceGroup deletes every Private DNS zone (cascading to its links and
// records) stored under the given resource group, backing the resource-group
// cascade delete. Best-effort: a single failure is returned but does not stop
// the remaining teardown. The subscription is unused (single-estate emulator).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	zones, err := h.pdns.ListPrivateZones(ctx, resourceGroup)

	var firstErr error
	if err != nil {
		firstErr = err
	}

	for i := range zones {
		if derr := h.pdns.DeletePrivateZone(ctx, zones[i].ResourceGroup, zones[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
