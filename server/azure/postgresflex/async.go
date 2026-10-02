package postgresflex

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	resourceTypeLocations  = "locations"
	subAzureAsyncOperation = "azureAsyncOperation"
	subOperationResults    = "operationResults"
	asyncAPIVersion        = "2025-08-01"
)

// writeAccepted answers a mutating Flexible Server call the way real ARM does:
// 202 Accepted with an Azure-AsyncOperation header. The current
// Microsoft.DBforPostgreSQL API (2025-08-01, the version azurerm pins) lists
// 202 as the only success code for PUT, PATCH, DELETE and the start, stop and
// restart actions on servers and their databases, firewall rules and
// configurations, so a 200 or 201 is rejected by go-azure-sdk. The backend is
// synchronous, so the status endpoint always reports Succeeded and the SDK
// poller then reads the resource with a GET. body is optional.
func writeAccepted(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, body any) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	opID := strings.ToLower(r.Method + "-" + rp.ResourceName + "-" + rp.SubResource + "-" + rp.SubResourceName)
	statusURL := scheme + "://" + r.Host + "/subscriptions/" + rp.Subscription +
		"/providers/" + providerName + "/" + resourceTypeLocations + "/eastus/" +
		subAzureAsyncOperation + "/" + opID + "?api-version=" + asyncAPIVersion

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Retry-After", "0")

	if body == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	azurearm.WriteJSON(w, http.StatusAccepted, body)
}

// isAsyncStatusPath reports whether rp is the per-location operation status
// endpoint writeAccepted points the poller at.
func isAsyncStatusPath(rp *azurearm.ResourcePath) bool {
	return rp.ResourceType == resourceTypeLocations &&
		(rp.SubResource == subAzureAsyncOperation || rp.SubResource == subOperationResults)
}

// serveAsyncStatus answers an operation status poll. Every operation already
// finished before its 202 was written, so each poll reports Succeeded.
func serveAsyncStatus(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{
		"name":   rp.SubResourceName,
		"status": "Succeeded",
	})
}
