// Package datafactory implements the Azure Data Factory
// (Microsoft.DataFactory/factories, api-version 2018-06-01) ARM REST API as a
// server.Handler. The real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datafactory/armdatafactory
// FactoriesClient, and the Terraform azurerm provider's azurerm_data_factory
// resource, configured with a custom endpoint, hit this handler the same way
// they hit management.azure.com, driving the shared datafactory driver.
//
// Microsoft.DataFactory is a distinct ARM provider namespace, so registration
// order relative to the other Azure handlers is unconstrained; it must register
// before the permissive BlobStorage fallback so a factory request is not
// swallowed as a blob call.
//
// Coverage:
//
//	PUT    .../factories/{name}   — Factories.CreateOrUpdate (sync 201/200)
//	GET    .../factories/{name}   — Factories.Get
//	PATCH  .../factories/{name}   — Factories.Update (tags + identity replace)
//	DELETE .../factories/{name}   — Factories.Delete (sync 200)
//	GET    .../factories          — Factories.ListByResourceGroup
//	GET    .../providers/Microsoft.DataFactory/factories — Factories.List
//
// identity is TOP-LEVEL and modeled explicitly (the server-wide echo only reaches
// the nested properties object); publicNetworkAccess is an explicit enum (the
// echo swallows an explicit zero); provisioningState/createTime/version are
// computed and stamped server-side. Every other property is preserved verbatim so
// deferred child surfaces (linked services, datasets, pipelines, triggers, ...)
// stay round-trip-safe.
package datafactory

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafactory/driver"
)

// Handler serves Microsoft.DataFactory/factories ARM requests against a Data
// Factory driver.
type Handler struct {
	df dfdriver.Factories
}

// New returns a Data Factory handler backed by df.
func New(df dfdriver.Factories) *Handler {
	return &Handler{df: df}
}

// Matches claims ARM URLs targeting Microsoft.DataFactory/factories. The provider
// namespace is disjoint from every other Azure handler, so registration order is
// unconstrained; registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && rp.ResourceType == factoriesType
}

// ServeHTTP routes on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listFactories(w, r, &rp)

		return
	}

	// A factory child resource (linked services, datasets, pipelines, triggers,
	// ...) is a separate resource type the core control plane does not model yet.
	// Reject cleanly rather than misparsing it as a factory request.
	if rp.SubResource != "" {
		writeSubResourceDeferred(w)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateFactory(w, r, &rp)
	case http.MethodGet:
		h.getFactory(w, r, &rp)
	case http.MethodPatch:
		h.updateFactory(w, r, &rp)
	case http.MethodDelete:
		h.deleteFactory(w, r, &rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// PurgeResourceGroup deletes every factory stored under the given resource group,
// backing the resource-group cascade delete. Best-effort: a single failure is
// returned but does not stop the remaining teardown. The subscription is unused
// (the emulator is single-estate).
func (h *Handler) PurgeResourceGroup(ctx context.Context, _, resourceGroup string) error {
	factories, err := h.df.ListFactoriesByResourceGroup(ctx, resourceGroup)
	if err != nil {
		return err
	}

	var firstErr error

	for i := range factories {
		if derr := h.df.DeleteFactory(ctx, factories[i].ResourceGroup, factories[i].Name); derr != nil && firstErr == nil {
			firstErr = derr
		}
	}

	return firstErr
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}

// writeSubResourceDeferred rejects an addressable factory child resource (linked
// services, datasets, pipelines, triggers, ...) the core control plane does not
// model yet.
func writeSubResourceDeferred(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "NotFound",
		"data factory sub-resource is not supported")
}
