// Package azureai implements the Azure AI ARM REST APIs as server.Handlers.
// This file serves Microsoft.CognitiveServices (AI Foundry / AI Studio / the AI
// Services resource / Azure OpenAI). Real
// github.com/Azure/azure-sdk-for-go/.../armcognitiveservices clients configured
// with a custom endpoint hit this handler the same way they hit
// management.azure.com.
//
// Coverage (Microsoft.CognitiveServices):
//
//	accounts                              CRUD, list by RG / subscription
//	accounts/{a}:listKeys|regenerateKey   POST  key management
//	accounts/{a}/models|skus|usages       GET   account catalogs/quota
//	accounts/{a}/deployments              CRUD, list  (model deployments)
//	accounts/{a}/projects                 CRUD, list  (AI Foundry projects)
//	accounts/{a}/raiPolicies              CRUD, list  (content filters)
//	accounts/{a}/commitmentPlans          CRUD, list
//	accounts/{a}/privateEndpointConnections CRUD, list
//
// ARM PUT returns the resource inline with a terminal provisioningState so the
// SDK LRO poller terminates on the first response.
package azureai

import (
	"net/http"

	csdriver "github.com/stackshy/cloudemu/azureai/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const csProvider = "Microsoft.CognitiveServices"

// Account sub-resource collection names.
const (
	collDeployments     = "deployments"
	collProjects        = "projects"
	collRaiPolicies     = "raiPolicies"
	collCommitmentPlans = "commitmentPlans"
	collPEC             = "privateEndpointConnections"
)

// CognitiveServicesHandler serves Microsoft.CognitiveServices ARM requests.
type CognitiveServicesHandler struct {
	svc csdriver.CognitiveServices
}

// NewCognitiveServices returns a handler backed by svc.
func NewCognitiveServices(svc csdriver.CognitiveServices) *CognitiveServicesHandler {
	return &CognitiveServicesHandler{svc: svc}
}

// Matches returns true for ARM Microsoft.CognitiveServices/accounts paths.
func (*CognitiveServicesHandler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == csProvider && rp.ResourceType == "accounts"
}

// childCollections are the account sub-resource collections (as opposed to
// account-level action verbs).
//
//nolint:gochecknoglobals // immutable routing set
var childCollections = map[string]bool{
	collDeployments: true, collProjects: true, collRaiPolicies: true,
	collCommitmentPlans: true, collPEC: true,
}

// ServeHTTP routes the request by path shape and method.
func (h *CognitiveServicesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")

		return
	}

	switch {
	case rp.ResourceName == "":
		h.serveAccountCollection(w, r, &rp)
	case rp.SubResource == "":
		h.serveAccount(w, r, &rp)
	case childCollections[rp.SubResource]:
		h.serveChild(w, r, &rp)
	default:
		h.serveAccountAction(w, r, &rp)
	}
}

func (h *CognitiveServicesHandler) serveAccountCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)

		return
	}

	h.listAccounts(w, r, rp)
}

func (h *CognitiveServicesHandler) serveAccount(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateAccount(w, r, rp)
	case http.MethodGet:
		h.getAccount(w, r, rp)
	case http.MethodPatch:
		h.patchAccount(w, r, rp)
	case http.MethodDelete:
		h.deleteAccount(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
