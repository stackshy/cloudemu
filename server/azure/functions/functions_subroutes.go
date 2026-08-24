package functions

import (
	"encoding/json"
	"io"
	"net/http"

	azfunctions "github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveFunctions routes the deployed-function sub-tree: the functions
// collection, a single function (get/create/delete), and its keys.
//
//nolint:gocritic // rp travels the dispatch chain once per request.
func (*Handler) serveFunctions(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
) {
	switch {
	case rp.SubResourceName == "":
		listFunctions(w, r, rp, store)
	case rp.SubResourceAction == actionListKeys:
		listFunctionKeys(w, r, rp, store)
	case rp.SubResourceAction == "":
		serveFunctionResource(w, r, rp, store)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "unsupported function route")
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func serveFunctionResource(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps,
) {
	switch r.Method {
	case http.MethodGet:
		getFunction(w, r, rp, store)
	case http.MethodPut:
		createFunction(w, r, rp, store)
	case http.MethodDelete:
		deleteFunction(w, r, rp, store)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func listFunctions(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	fns, err := store.ListSiteFunctions(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]functionEnvelope, 0, len(fns))
	for i := range fns {
		out = append(out, toFunctionEnvelope(rp, &fns[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, functionEnvelopeCollection{Value: out})
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func getFunction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	fn, err := store.GetSiteFunction(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toFunctionEnvelope(rp, fn))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func createFunction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBytes)

	var req createFunctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	fn, err := store.CreateSiteFunction(r.Context(), rp.ResourceName, azfunctions.SiteFunction{
		Name:       rp.SubResourceName,
		Config:     req.Properties.Config,
		Language:   req.Properties.Language,
		IsDisabled: req.Properties.IsDisabled,
	})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toFunctionEnvelope(rp, fn))
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func deleteFunction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	if err := store.DeleteSiteFunction(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//nolint:gocritic // rp travels the dispatch chain once per request.
func listFunctionKeys(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store azureFunctionApps) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "listkeys requires POST")
		return
	}

	keys, err := store.FunctionKeys(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, stringDictionary{
		ID:         siteID(rp) + "/functions/" + rp.SubResourceName + "/keys",
		Name:       rp.SubResourceName,
		Type:       functionResourceType,
		Properties: nonNilMap(keys),
	})
}
