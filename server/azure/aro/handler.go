// Package aro implements the Azure Red Hat OpenShift
// (Microsoft.RedHatOpenShift/openShiftClusters) ARM REST API as a
// server.Handler. Real armredhatopenshift clients configured with a custom
// endpoint hit this handler the same way they hit management.azure.com.
//
// Coverage (control plane):
//
//	PUT    .../providers/Microsoft.RedHatOpenShift/openShiftClusters/{name}                  — Create or update cluster
//	GET    .../providers/Microsoft.RedHatOpenShift/openShiftClusters/{name}                  — Get cluster
//	DELETE .../providers/Microsoft.RedHatOpenShift/openShiftClusters/{name}                  — Delete cluster
//	GET    .../providers/Microsoft.RedHatOpenShift/openShiftClusters                         — List in resource group
//	GET    /subscriptions/{s}/providers/Microsoft.RedHatOpenShift/openShiftClusters          — List in subscription
//	POST   .../openShiftClusters/{name}/listAdminCredentials                                 — Admin kubeconfig
//	POST   .../openShiftClusters/{name}/listCredentials                                      — kubeadmin username/password
//
// When a shared kubernetes.APIServer is wired into the ARO provider, the
// kubeconfig returned by listAdminCredentials points at a real, OpenShift-
// flavored in-memory data plane, so `oc` operates end-to-end.
package aro

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/aro"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const providerName = "Microsoft.RedHatOpenShift"

// Backend is the ARO surface the handler needs. *aro.Mock satisfies it.
type Backend interface {
	CreateOrUpdateCluster(ctx context.Context, input aro.ClusterInput) (*aro.OpenShiftCluster, error)
	GetCluster(ctx context.Context, subscription, rg, name string) (*aro.OpenShiftCluster, error)
	ListClustersByResourceGroup(ctx context.Context, subscription, rg string) []aro.OpenShiftCluster
	ListClusters(ctx context.Context, subscription string) []aro.OpenShiftCluster
	DeleteCluster(ctx context.Context, subscription, rg, name string) error
	Kubeconfig(subscription, rg, name string) []byte
}

// Handler serves Microsoft.RedHatOpenShift ARM requests against an ARO Backend.
type Handler struct {
	be Backend
}

// New returns an ARO handler backed by be.
func New(be Backend) *Handler {
	return &Handler{be: be}
}

// Matches returns true for ARM Microsoft.RedHatOpenShift openShiftClusters paths.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, resourceTypeOpenShiftClusters)
}

// ServeHTTP routes the request by path shape and method.
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

		h.listClusters(w, r, &rp)

		return
	}

	switch {
	case strings.EqualFold(rp.SubResource, "listAdminCredentials"):
		h.postOnly(w, r, &rp, h.listAdminCredentials)
	case strings.EqualFold(rp.SubResource, "listCredentials"):
		h.postOnly(w, r, &rp, h.listCredentials)
	case rp.SubResource == "":
		h.serveCluster(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"ARO sub-resource not implemented: "+rp.SubResource)
	}
}

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateCluster(w, r, rp)
	case http.MethodGet:
		h.getCluster(w, r, rp)
	case http.MethodDelete:
		h.deleteCluster(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// postOnly enforces POST for the credential-listing sub-resources.
func (*Handler) postOnly(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	fn func(http.ResponseWriter, *http.Request, *azurearm.ResourcePath),
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)

		return
	}

	fn(w, r, rp)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
