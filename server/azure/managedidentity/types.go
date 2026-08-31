package managedidentity

import "github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"

// identityRequest is the ARM PUT/PATCH body. Only location and tags are
// writable; the properties block is read-only and ignored on input.
type identityRequest struct {
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags,omitempty"`
}

// identityResponse is the ARM representation of a user-assigned identity.
type identityResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties identityProps     `json:"properties"`
}

// identityProps carries the three service-minted, read-only ids.
type identityProps struct {
	ClientID    string `json:"clientId"`
	PrincipalID string `json:"principalId"`
	TenantID    string `json:"tenantId"`
}

// listResponse is the ARM list envelope for ListByResourceGroup /
// ListBySubscription. nextLink is omitted — the emulator returns a single page.
type listResponse struct {
	Value []identityResponse `json:"value"`
}

// toResponse projects a stored identity onto its ARM wire representation.
func toResponse(id *managedidentity.Identity) identityResponse {
	return identityResponse{
		ID:       id.ARMID(),
		Name:     id.Name,
		Type:     armType,
		Location: id.Location,
		Tags:     id.Tags,
		Properties: identityProps{
			ClientID:    id.ClientID,
			PrincipalID: id.PrincipalID,
			TenantID:    id.TenantID,
		},
	}
}
