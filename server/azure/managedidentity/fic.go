package managedidentity

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/managedidentity"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	subFICs    = "federatedIdentityCredentials"
	ficType    = providerName + "/" + resourceType + "/" + subFICs
	ficMaxPath = 3 // userAssignedIdentities/{i}/federatedIdentityCredentials/{f}
)

type ficProperties struct {
	Issuer    string   `json:"issuer"`
	Subject   string   `json:"subject"`
	Audiences []string `json:"audiences"`
}

type ficJSON struct {
	ID         string        `json:"id,omitempty"`
	Name       string        `json:"name,omitempty"`
	Type       string        `json:"type,omitempty"`
	Properties ficProperties `json:"properties"`
}

// isFICPath reports whether rp addresses the identity's federated credentials.
func isFICPath(rp *azurearm.ResourcePath) bool {
	return strings.EqualFold(rp.SubResource, subFICs) && !rp.IsExtension() && rp.Depth <= ficMaxPath
}

func toFICJSON(f *managedidentity.FederatedCredential) ficJSON {
	parent := managedidentity.Identity{Subscription: f.Subscription, ResourceGroup: f.ResourceGroup, Name: f.Identity}

	return ficJSON{
		ID:         parent.ARMID() + "/" + subFICs + "/" + f.Name,
		Name:       f.Name,
		Type:       ficType,
		Properties: ficProperties{Issuer: f.Issuer, Subject: f.Subject, Audiences: f.Audiences},
	}
}

// serveFIC routes userAssignedIdentities/{i}/federatedIdentityCredentials[/{f}].
func (h *Handler) serveFIC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ctx, sub, rg, identity, name := r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, rp.SubResourceName

	if name == "" {
		if r.Method != http.MethodGet {
			azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
			return
		}

		list, err := h.store.ListFICs(ctx, sub, rg, identity)
		if err != nil {
			azurearm.WriteParentNotFound(w, err)
			return
		}

		out := make([]ficJSON, 0, len(list))
		for i := range list {
			out = append(out, toFICJSON(&list[i]))
		}

		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": out})

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putFIC(w, r, rp)
	case http.MethodGet:
		f, err := h.store.GetFIC(ctx, sub, rg, identity, name)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toFICJSON(&f))
	case http.MethodDelete:
		if h.store.DeleteFIC(ctx, sub, rg, identity, name) {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) putFIC(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body ficJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	in := managedidentity.FICInput{
		Issuer: body.Properties.Issuer, Subject: body.Properties.Subject, Audiences: body.Properties.Audiences,
	}

	f, created, err := h.store.CreateOrUpdateFIC(r.Context(), rp.Subscription, rp.ResourceGroup,
		rp.ResourceName, rp.SubResourceName, in)

	switch {
	case cerrors.IsNotFound(err):
		azurearm.WriteParentNotFound(w, err)
	case cerrors.IsInvalidArgument(err):
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", cerrors.Message(err))
	case err != nil:
		azurearm.WriteCErr(w, err)
	case created:
		azurearm.WriteJSON(w, http.StatusCreated, toFICJSON(&f))
	default:
		azurearm.WriteJSON(w, http.StatusOK, toFICJSON(&f))
	}
}
