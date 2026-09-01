package vnet

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	// typePublicIPPrefix is the Microsoft.Network resource type for public IP prefixes.
	typePublicIPPrefix = "publicIPPrefixes"
	// armPublicIPPrefixTag records, on a public IP, the ARM id of the prefix it was
	// carved from. It is internal (cloudemu: prefix) so stripInternal keeps it out
	// of the public IP's own tags; the prefix's read-only publicIPAddresses[]
	// back-reference is rebuilt by scanning public IPs for it.
	armPublicIPPrefixTag = "cloudemu:azurePublicIPPrefixId"
)

// ARM JSON shapes for Microsoft.Network/publicIPPrefixes. The sku is TOP-LEVEL
// (not nested under properties), matching the armnetwork PublicIPPrefix model.

type publicIPPrefixRequest struct {
	Location   string                 `json:"location"`
	Tags       map[string]string      `json:"tags,omitempty"`
	SKU        *publicIPPrefixSKU     `json:"sku,omitempty"`
	Zones      []string               `json:"zones,omitempty"`
	Properties publicIPPrefixReqProps `json:"properties"`
}

type publicIPPrefixSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type publicIPPrefixReqProps struct {
	PrefixLength int32 `json:"prefixLength,omitempty"`
}

type publicIPPrefixResponse struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Location   string                  `json:"location"`
	Tags       map[string]string       `json:"tags,omitempty"`
	SKU        *publicIPPrefixSKU      `json:"sku,omitempty"`
	Zones      []string                `json:"zones,omitempty"`
	Etag       string                  `json:"etag,omitempty"`
	Properties publicIPPrefixRespProps `json:"properties"`
}

type publicIPPrefixRespProps struct {
	ProvisioningState string `json:"provisioningState"`
	PrefixLength      int32  `json:"prefixLength,omitempty"`
	IPPrefix          string `json:"ipPrefix,omitempty"`
	// PublicIPAddresses is read-only: a public IP joins a prefix by setting its own
	// publicIPPrefix property, so this list is rebuilt by scanning public IPs that
	// reference this prefix, never written directly.
	PublicIPAddresses []armIDRef `json:"publicIPAddresses,omitempty"`
}

type publicIPPrefixListResponse struct {
	Value []publicIPPrefixResponse `json:"value"`
}

// prefixCap returns the public-IP-prefix surface if the networking driver
// implements it (the Azure provider does; AWS/GCP do not).
func (h *Handler) prefixCap() (netdriver.AzurePublicIPPrefixes, bool) {
	svc, ok := h.net.(netdriver.AzurePublicIPPrefixes)

	return svc, ok
}

// routePublicIPPrefix dispatches Microsoft.Network/publicIPPrefixes requests.
// armnetwork's PublicIPPrefixesClient uses BeginCreateOrUpdate / BeginDelete
// pollers, so PUT and DELETE ride the shared async plumbing (writeAcceptedAsync +
// the locations/operationStatuses responder) exactly like NAT gateways.
//
//nolint:gocritic,dupl // rp is request-scoped; mirrors routeASG's capability-gated dispatch over a distinct resource type
func (h *Handler) routePublicIPPrefix(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.prefixCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"public IP prefixes are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listPublicIPPrefixes(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createPublicIPPrefix(w, r, rp, svc)
	case http.MethodGet:
		h.getPublicIPPrefix(w, r, rp, svc)
	case http.MethodDelete:
		h.deletePublicIPPrefix(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) createPublicIPPrefix(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePublicIPPrefixes,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req publicIPPrefixRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	prefix := netdriver.AzurePublicIPPrefix{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      loc,
		Tags:          req.Tags,
		Zones:         req.Zones,
		PrefixLength:  req.Properties.PrefixLength,
	}

	if req.SKU != nil {
		prefix.SKUName = req.SKU.Name
		prefix.SKUTier = req.SKU.Tier
	}

	stored := svc.PutAzurePublicIPPrefix(r.Context(), prefix)

	writeAcceptedAsync(w, r, rp.Subscription, "pipprefix-create-"+rp.ResourceName,
		h.publicIPPrefixResponse(r.Context(), stored, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getPublicIPPrefix(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePublicIPPrefixes,
) {
	prefix, ok := svc.GetAzurePublicIPPrefix(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"public IP prefix "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.publicIPPrefixResponse(r.Context(), prefix, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deletePublicIPPrefix(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePublicIPPrefixes,
) {
	// A missing prefix deletes idempotently; either way the poller completes on
	// the async operationStatuses Succeeded, matching armnetwork's BeginDelete.
	svc.DeleteAzurePublicIPPrefix(r.Context(), rp.ResourceGroup, rp.ResourceName)
	writeAcceptedAsync(w, r, rp.Subscription, "pipprefix-delete-"+rp.ResourceName, nil)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listPublicIPPrefixes(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzurePublicIPPrefixes,
) {
	// An empty ResourceGroup in the path means a subscription-wide list.
	prefixes := svc.ListAzurePublicIPPrefixes(r.Context(), rp.ResourceGroup)

	out := publicIPPrefixListResponse{Value: make([]publicIPPrefixResponse, 0, len(prefixes))}

	for i := range prefixes {
		scope := rp
		scope.ResourceGroup = prefixes[i].ResourceGroup
		scope.ResourceName = prefixes[i].Name
		out.Value = append(out.Value, h.publicIPPrefixResponse(r.Context(), prefixes[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) publicIPPrefixResponse(
	ctx context.Context, prefix netdriver.AzurePublicIPPrefix, rp azurearm.ResourcePath,
) publicIPPrefixResponse {
	loc := prefix.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePublicIPPrefix, rp.ResourceName)

	out := publicIPPrefixResponse{
		ID:       id,
		Name:     rp.ResourceName,
		Type:     providerName + "/" + typePublicIPPrefix,
		Location: loc,
		Tags:     prefix.Tags,
		Zones:    prefix.Zones,
		Etag:     etagOf(id),
		Properties: publicIPPrefixRespProps{
			ProvisioningState: provisioningSucceeded,
			PrefixLength:      prefix.PrefixLength,
			IPPrefix:          prefix.IPPrefix,
			PublicIPAddresses: h.prefixPublicIPRefs(ctx, id),
		},
	}

	if prefix.SKUName != "" || prefix.SKUTier != "" {
		out.SKU = &publicIPPrefixSKU{Name: prefix.SKUName, Tier: prefix.SKUTier}
	}

	return out
}

// prefixPublicIPRefs scans public IPs for the ones carved from this prefix (their
// armPublicIPPrefixTag matches the prefix ARM id), building each public IP's own
// ARM resource id. This is the read-only publicIPAddresses[] back-reference; a
// public IP created with a publicIPPrefix ref only stores the ref (child-IP
// allocation from the prefix range is deferred).
func (h *Handler) prefixPublicIPRefs(ctx context.Context, prefixARMID string) []armIDRef {
	eips, err := h.net.DescribeAddresses(ctx, nil)
	if err != nil {
		return nil
	}

	var out []armIDRef

	for i := range eips {
		if tagOr(eips[i].Tags, armPublicIPPrefixTag, "") != prefixARMID {
			continue
		}

		pipName := tagOr(eips[i].Tags, armPublicIPTag, "")
		if pipName == "" {
			continue
		}

		pipRG := tagOr(eips[i].Tags, armPublicIPRGTag, "")
		id := azurearm.BuildResourceID(prefixSubscription(prefixARMID), pipRG, providerName, typePublicIP, pipName)
		out = append(out, armIDRef{ID: id})
	}

	return out
}

// prefixSubscription extracts the subscription id from a prefix ARM id so the
// public IP back-references share the same subscription. The id shape is
// /subscriptions/{sub}/resourceGroups/... — a malformed id yields "".
func prefixSubscription(armID string) string {
	rp, ok := azurearm.ParsePath(armID)
	if !ok {
		return ""
	}

	return rp.Subscription
}

// purgePublicIPPrefixes deletes every public IP prefix in the resource group,
// part of the PurgeResourceGroup cascade so prefixes don't orphan on RG delete.
func (h *Handler) purgePublicIPPrefixes(ctx context.Context, resourceGroup string) {
	svc, ok := h.prefixCap()
	if !ok {
		return
	}

	prefixes := svc.ListAzurePublicIPPrefixes(ctx, resourceGroup)
	for i := range prefixes {
		svc.DeleteAzurePublicIPPrefix(ctx, prefixes[i].ResourceGroup, prefixes[i].Name)
	}
}
