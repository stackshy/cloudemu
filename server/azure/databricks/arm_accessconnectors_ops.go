package databricks

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// serveAccessConnectors routes accessConnectors collection and resource paths.
func (h *Handler) serveAccessConnectors(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)

			return
		}

		h.listAccessConnectors(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateAccessConnector(w, r, rp)
	case http.MethodGet:
		h.getAccessConnector(w, r, rp)
	case http.MethodPatch:
		h.patchAccessConnector(w, r, rp)
	case http.MethodDelete:
		h.deleteAccessConnector(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createOrUpdateAccessConnector(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armAccessConnector
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := dbxdriver.AccessConnectorConfig{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      body.Location,
		Tags:          body.Tags,
		Identity:      fromARMIdentity(body.Identity),
	}

	ac, err := h.dbx.CreateOrUpdateAccessConnector(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAccessConnector(ac))
}

func (h *Handler) getAccessConnector(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	ac, err := h.dbx.GetAccessConnector(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAccessConnector(ac))
}

func (h *Handler) patchAccessConnector(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body accessConnectorUpdate
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	ac, err := h.dbx.UpdateAccessConnector(r.Context(), rp.ResourceGroup, rp.ResourceName, body.Tags, fromARMIdentity(body.Identity))
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAccessConnector(ac))
}

func (h *Handler) deleteAccessConnector(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.dbx.DeleteAccessConnector(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listAccessConnectors(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var (
		connectors []dbxdriver.AccessConnector
		err        error
	)

	if rp.ResourceGroup != "" {
		connectors, err = h.dbx.ListAccessConnectorsByResourceGroup(r.Context(), rp.ResourceGroup)
	} else {
		connectors, err = h.dbx.ListAccessConnectors(r.Context())
	}

	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	out := make([]armAccessConnector, 0, len(connectors))
	for i := range connectors {
		out = append(out, toARMAccessConnector(&connectors[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, armAccessConnectorList{Value: out})
}
