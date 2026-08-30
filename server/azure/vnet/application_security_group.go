package vnet

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// typeASG is the Microsoft.Network resource type for application security groups.
const typeASG = "applicationSecurityGroups"

// ARM JSON shapes for Microsoft.Network/applicationSecurityGroups. An ASG is a
// tag-like resource: its properties object is empty apart from the read-only
// provisioningState, so there is no request-properties struct.

type asgRequest struct {
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags,omitempty"`
}

type asgResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Etag       string            `json:"etag,omitempty"`
	Properties asgResponseProps  `json:"properties"`
}

type asgResponseProps struct {
	ProvisioningState string `json:"provisioningState"`
}

type asgListResponse struct {
	Value []asgResponse `json:"value"`
}

// asgCap returns the application-security-group surface if the networking driver
// implements it (the Azure provider does; AWS/GCP do not).
func (h *Handler) asgCap() (netdriver.AzureApplicationSecurityGroups, bool) {
	svc, ok := h.net.(netdriver.AzureApplicationSecurityGroups)

	return svc, ok
}

// routeASG dispatches Microsoft.Network/applicationSecurityGroups requests.
// Unlike NAT gateways, ASGs have no BeginX-poller data plane: real armnetwork's
// ApplicationSecurityGroupsClient pollers complete on a synchronous terminal
// 200, so create/get/delete all answer sync-200 (no 202 async plumbing).
//
//nolint:gocritic,dupl // rp is request-scoped; capability-gated dispatch mirrored by routePublicIPPrefix over a distinct type
func (h *Handler) routeASG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	svc, ok := h.asgCap()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"application security groups are not supported by this networking driver")

		return
	}

	if rp.ResourceName == "" {
		h.listASGs(w, r, rp, svc)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createASG(w, r, rp, svc)
	case http.MethodGet:
		h.getASG(w, r, rp, svc)
	case http.MethodDelete:
		h.deleteASG(w, r, rp, svc)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) createASG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureApplicationSecurityGroups,
) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req asgRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	loc := req.Location
	if loc == "" {
		loc = defaultLoc
	}

	stored := svc.PutAzureApplicationSecurityGroup(r.Context(), netdriver.AzureApplicationSecurityGroup{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      loc,
		Tags:          req.Tags,
	})

	azurearm.WriteJSON(w, http.StatusOK, asgResponseFrom(stored, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getASG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureApplicationSecurityGroups,
) {
	asg, ok := svc.GetAzureApplicationSecurityGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound",
			"application security group "+rp.ResourceName+" not found")

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, asgResponseFrom(asg, rp))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) deleteASG(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureApplicationSecurityGroups,
) {
	// Sync-200 (empty body): armnetwork's BeginDelete poller completes on a
	// terminal 200 with no async headers, so no operationStatuses responder is
	// needed. A missing ASG deletes idempotently (204-equivalent 200), matching
	// ARM's delete-of-absent behavior.
	svc.DeleteAzureApplicationSecurityGroup(r.Context(), rp.ResourceGroup, rp.ResourceName)
	w.WriteHeader(http.StatusOK)
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) listASGs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	svc netdriver.AzureApplicationSecurityGroups,
) {
	// An empty ResourceGroup in the path means a subscription-wide list.
	asgs := svc.ListAzureApplicationSecurityGroups(r.Context(), rp.ResourceGroup)

	out := asgListResponse{Value: make([]asgResponse, 0, len(asgs))}

	for i := range asgs {
		scope := rp
		scope.ResourceGroup = asgs[i].ResourceGroup
		scope.ResourceName = asgs[i].Name
		out.Value = append(out.Value, asgResponseFrom(asgs[i], scope))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

//nolint:gocritic // rp is a request-scoped value
func asgResponseFrom(asg netdriver.AzureApplicationSecurityGroup, rp azurearm.ResourcePath) asgResponse {
	loc := asg.Location
	if loc == "" {
		loc = defaultLoc
	}

	id := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeASG, rp.ResourceName)

	return asgResponse{
		ID:         id,
		Name:       rp.ResourceName,
		Type:       providerName + "/" + typeASG,
		Location:   loc,
		Tags:       asg.Tags,
		Etag:       etagOf(id),
		Properties: asgResponseProps{ProvisioningState: provisioningSucceeded},
	}
}

// purgeASGs deletes every application security group in the resource group,
// part of the PurgeResourceGroup cascade so ASGs don't orphan on RG delete. It
// cannot fail (an in-memory delete is total), so it returns nothing.
func (h *Handler) purgeASGs(ctx context.Context, resourceGroup string) {
	svc, ok := h.asgCap()
	if !ok {
		return
	}

	for _, asg := range svc.ListAzureApplicationSecurityGroups(ctx, resourceGroup) {
		svc.DeleteAzureApplicationSecurityGroup(ctx, asg.ResourceGroup, asg.Name)
	}
}
