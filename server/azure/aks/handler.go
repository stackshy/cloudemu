// Package aks implements the Azure Kubernetes Service (Microsoft.ContainerService)
// ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com.
//
// Wave 1 coverage (control plane only):
//
//	PUT    .../providers/Microsoft.ContainerService/managedClusters/{name}                                         — Create or update cluster
//	GET    .../providers/Microsoft.ContainerService/managedClusters/{name}                                         — Get cluster
//	PATCH  .../providers/Microsoft.ContainerService/managedClusters/{name}                                         — Update tags
//	DELETE .../providers/Microsoft.ContainerService/managedClusters/{name}                                         — Delete cluster (cascade)
//	GET    .../providers/Microsoft.ContainerService/managedClusters                                                — List in resource group
//	GET    /subscriptions/{s}/providers/Microsoft.ContainerService/managedClusters                                 — List in subscription
//	PUT    .../managedClusters/{name}/agentPools/{pool}                                                            — Create or update pool
//	GET    .../managedClusters/{name}/agentPools/{pool}                                                            — Get pool
//	DELETE .../managedClusters/{name}/agentPools/{pool}                                                            — Delete pool
//	GET    .../managedClusters/{name}/agentPools                                                                   — List pools
//	PUT    .../managedClusters/{name}/maintenanceConfigurations/{cfg}                                              — Upsert maintenance cfg
//	GET    .../managedClusters/{name}/maintenanceConfigurations/{cfg}                                              — Get maintenance cfg
//	DELETE .../managedClusters/{name}/maintenanceConfigurations/{cfg}                                              — Delete maintenance cfg
//	GET    .../managedClusters/{name}/maintenanceConfigurations                                                    — List maintenance cfgs
//	POST   .../managedClusters/{name}/listClusterAdminCredential                                                   — Stub kubeconfig
//	POST   .../managedClusters/{name}/listClusterUserCredential                                                    — Stub kubeconfig
//	POST   .../managedClusters/{name}/listClusterMonitoringUserCredential                                          — Stub kubeconfig
//	POST   .../managedClusters/{name}/rotateClusterCertificates                                                    — Cert rotation no-op
//
// The Kubernetes data plane (Deployments / Services / Pods) is intentionally
// NOT served — that lands in Wave 2. The kubeconfig blobs we return point
// at https://AKS-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local so a caller that
// tries to talk Kubernetes immediately fails with a clear sentinel.
//
// Mutating ops return 200 OK with the resource body inline so the SDK's LRO
// poller terminates on the first response.
package aks

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/providers/azure/aks"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

const providerName = "Microsoft.ContainerService"

// Backend is the minimal AKS surface the handler needs. *aks.Mock satisfies
// it; tests can swap a fake by satisfying the same methods.
type Backend interface {
	CreateOrUpdateCluster(ctx context.Context, input aks.ClusterInput) (*aks.ManagedCluster, error)
	GetCluster(ctx context.Context, rg, name string) (*aks.ManagedCluster, error)
	UpdateClusterTags(ctx context.Context, rg, name string, tags map[string]string) (*aks.ManagedCluster, error)
	DeleteCluster(ctx context.Context, rg, name string) error
	ListClustersByResourceGroup(ctx context.Context, rg string) ([]aks.ManagedCluster, error)
	ListClusters(ctx context.Context) ([]aks.ManagedCluster, error)
	RotateClusterCertificates(ctx context.Context, rg, name string) error

	CreateOrUpdateAgentPool(ctx context.Context, rg, cluster string, in aks.AgentPoolInput) (*aks.AgentPool, error)
	GetAgentPool(ctx context.Context, rg, cluster, pool string) (*aks.AgentPool, error)
	DeleteAgentPool(ctx context.Context, rg, cluster, pool string) error
	ListAgentPools(ctx context.Context, rg, cluster string) ([]aks.AgentPool, error)

	CreateOrUpdateMaintenanceConfig(
		ctx context.Context, rg, cluster, name string, props map[string]any,
	) (*aks.MaintenanceConfig, error)
	GetMaintenanceConfig(ctx context.Context, rg, cluster, name string) (*aks.MaintenanceConfig, error)
	DeleteMaintenanceConfig(ctx context.Context, rg, cluster, name string) error
	ListMaintenanceConfigs(ctx context.Context, rg, cluster string) ([]aks.MaintenanceConfig, error)

	StubKubeconfig(rg, name string) []byte
}

// Handler serves Microsoft.ContainerService ARM requests against an AKS Backend.
type Handler struct {
	be Backend
}

// New returns an AKS handler backed by be.
func New(be Backend) *Handler {
	return &Handler{be: be}
}

// Matches returns true for ARM Microsoft.ContainerService managedClusters paths.
// AKS uses a mix of "managedClusters" and "managedclusters" in URL templates
// across SDK operations, so the match is case-insensitive on the resource type.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if !strings.EqualFold(rp.Provider, providerName) {
		return false
	}

	return strings.EqualFold(rp.ResourceType, resourceTypeManagedClusters)
}

// ServeHTTP routes the request based on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// Subscription-scoped or RG-scoped collection: no resourceName.
	if rp.ResourceName == "" {
		h.serveClusterCollection(w, r, &rp)
		return
	}

	// Sub-resource routing.
	switch {
	case strings.EqualFold(rp.SubResource, "agentPools"):
		h.serveAgentPoolRoute(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "maintenanceConfigurations"):
		h.serveMaintenanceRoute(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "listClusterAdminCredential"),
		strings.EqualFold(rp.SubResource, "listClusterUserCredential"),
		strings.EqualFold(rp.SubResource, "listClusterMonitoringUserCredential"):
		h.serveListCredentials(w, r, &rp)
	case strings.EqualFold(rp.SubResource, "rotateClusterCertificates"):
		h.serveRotateCertificates(w, r, &rp)
	case rp.SubResource == "":
		h.serveCluster(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"AKS sub-resource not implemented: "+rp.SubResource)
	}
}

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateCluster(w, r, rp)
	case http.MethodGet:
		h.getCluster(w, r, rp)
	case http.MethodPatch:
		h.updateClusterTags(w, r, rp)
	case http.MethodDelete:
		h.deleteCluster(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveClusterCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listClusters(w, r, rp)
}

//nolint:dupl // sub-resource route shapes are intentionally typed; sharing via generics adds noise.
func (h *Handler) serveAgentPoolRoute(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listAgentPools(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateAgentPool(w, r, rp)
	case http.MethodGet:
		h.getAgentPool(w, r, rp)
	case http.MethodDelete:
		h.deleteAgentPool(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

//nolint:dupl // sub-resource route shapes are intentionally typed; sharing via generics adds noise.
func (h *Handler) serveMaintenanceRoute(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listMaintenanceConfigs(w, r, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateMaintenanceConfig(w, r, rp)
	case http.MethodGet:
		h.getMaintenanceConfig(w, r, rp)
	case http.MethodDelete:
		h.deleteMaintenanceConfig(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveListCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.listClusterCredentials(w, r, rp)
}

func (h *Handler) serveRotateCertificates(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.rotateClusterCertificates(w, r, rp)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
