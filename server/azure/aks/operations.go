package aks

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/aks"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// ---- Managed Cluster ops ----

func (h *Handler) createOrUpdateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armManagedCluster
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	_, getErr := h.be.GetCluster(r.Context(), rp.ResourceGroup, rp.ResourceName)
	existed := getErr == nil

	cluster, err := h.be.CreateOrUpdateCluster(r.Context(), buildClusterInput(&body, rp))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, _ := h.be.ListAgentPools(r.Context(), rp.ResourceGroup, rp.ResourceName)

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, toARMCluster(cluster, pools, rp.Subscription))
}

func buildClusterInput(body *armManagedCluster, rp *azurearm.ResourcePath) aks.ClusterInput {
	in := aks.ClusterInput{
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Name:          rp.ResourceName,
		Location:      body.Location,
		Tags:          fromPtrTags(body.Tags),
	}

	if body.SKU != nil {
		in.Tier = body.SKU.Tier
	}

	if body.Identity != nil {
		in.IdentityPresent = true
		in.IdentityType = body.Identity.Type
		in.UserAssignedIdentityIDs = identityIDs(body.Identity.UserAssignedIdentities)
	}

	if body.Properties != nil {
		in.KubernetesVersion = body.Properties.KubernetesVersion
		in.DNSPrefix = body.Properties.DNSPrefix
		in.NodeResourceGroup = body.Properties.NodeResourceGroup
		in.EnableRBAC = body.Properties.EnableRBAC
		in.NetworkProfile = networkProfileInput(body.Properties.NetworkProfile)
		in.AgentPools = inlineAgentPoolInputs(body.Properties.AgentPoolProfiles)
	}

	return in
}

// identityIDs returns the ARM resource IDs (map keys) of the submitted
// user-assigned identities, so the backend can synthesize a principal/client
// pair for each. The submitted values are read-only and ignored.
func identityIDs(in map[string]*armUserAssignedValue) []string {
	if len(in) == 0 {
		return nil
	}

	ids := make([]string, 0, len(in))
	for id := range in {
		ids = append(ids, id)
	}

	return ids
}

// networkProfileInput maps the submitted ARM networkProfile onto the driver
// input, or nil when the caller omitted it (so the backend synthesizes the AKS
// defaults). Only the modeled sub-keys are carried; an unmodeled sub-key the
// caller set still round-trips through the property overlay.
func networkProfileInput(np *armNetworkProfile) *aks.NetworkProfile {
	if np == nil {
		return nil
	}

	return &aks.NetworkProfile{
		NetworkPlugin:   np.NetworkPlugin,
		NetworkPolicy:   np.NetworkPolicy,
		ServiceCidr:     np.ServiceCidr,
		DNSServiceIP:    np.DNSServiceIP,
		PodCidr:         np.PodCidr,
		LoadBalancerSKU: np.LoadBalancerSKU,
		OutboundType:    np.OutboundType,
	}
}

// inlineAgentPoolInputs maps the inline agentPoolProfiles submitted in a cluster
// PUT onto driver inputs. It shares the advanced-field mapping with the
// standalone agentPools path so both wire paths round-trip identically.
func inlineAgentPoolInputs(profiles []armAgentPoolProfile) []aks.AgentPoolInput {
	if len(profiles) == 0 {
		return nil
	}

	out := make([]aks.AgentPoolInput, 0, len(profiles))

	for i := range profiles {
		p := &profiles[i]
		in := aks.AgentPoolInput{
			Name:             p.Name,
			Count:            p.Count,
			VMSize:           p.VMSize,
			OSDiskSizeGB:     p.OSDiskSizeGB,
			OSType:           p.OSType,
			Mode:             p.Mode,
			OrchestratorVer:  p.OrchestratorVer,
			ScaleSetPriority: p.ScaleSetPriority,
			NodeLabels:       fromPtrTags(p.NodeLabels),
			NodeTaints:       p.NodeTaints,
			MaxPods:          p.MaxPods,
			OSDiskType:       p.OSDiskType,
			Type:             p.Type,
		}
		p.armAgentPoolAdvanced.applyTo(&in)
		out = append(out, in)
	}

	return out
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	cluster, err := h.be.GetCluster(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, _ := h.be.ListAgentPools(r.Context(), rp.ResourceGroup, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(cluster, pools, rp.Subscription))
}

func (h *Handler) updateClusterTags(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armTagsObject
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cluster, err := h.be.UpdateClusterTags(r.Context(), rp.ResourceGroup, rp.ResourceName, fromPtrTags(body.Tags))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, _ := h.be.ListAgentPools(r.Context(), rp.ResourceGroup, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(cluster, pools, rp.Subscription))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	writeIdempotentDelete(w, h.be.DeleteCluster(r.Context(), rp.ResourceGroup, rp.ResourceName))
}

// writeIdempotentDelete renders an ARM DELETE result. ARM DELETE is idempotent:
// a missing resource is a successful no-op, so a NotFound error is treated the
// same as a successful delete. 204 keeps the SDK LRO poller terminal (the AKS
// swagger documents 202/204 for DELETE).
func writeIdempotentDelete(w http.ResponseWriter, err error) {
	if err != nil && !cerrors.IsNotFound(err) {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// postClusterAction is the shared body for start/stop. Both are long-running
// actions whose SDK response type carries no fields, so a 202 +
// Azure-AsyncOperation header pointing at a synthetic status endpoint is
// enough for the poller to terminate once it observes Succeeded — no final
// GET or resource body is required.
func postClusterAction(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	action func(ctx context.Context, rg, name string) (*aks.ManagedCluster, error),
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if _, err := action(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	opID := rp.ResourceName + "-" + rp.SubResource
	w.Header().Set("Azure-AsyncOperation", asyncStatusURL(r, rp.Subscription, opID))
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// asyncStatusURL builds a self-referential operationStatuses URL for a
// synthetic operation id, so the request's own scheme/host works on both the
// plain-HTTP and TLS listeners.
func asyncStatusURL(r *http.Request, subscription, opID string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host +
		"/subscriptions/" + subscription +
		"/providers/" + providerName + "/" + resourceTypeLocations + "/eastus/" + subOperationStatuses + "/" + opID +
		"?api-version=2025-02-01"
}

// operationStatus answers the LRO poll start/stop point at. The backend is
// synchronous, so by the time the SDK polls, the action has already
// completed — every poll reports Succeeded.
func (*Handler) operationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{"status": "Succeeded"})
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var (
		clusters []aks.ManagedCluster
		err      error
	)

	if rp.ResourceGroup == "" {
		clusters, err = h.be.ListClusters(r.Context())
	} else {
		clusters, err = h.be.ListClustersByResourceGroup(r.Context(), rp.ResourceGroup)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armManagedCluster, 0, len(clusters))

	for i := range clusters {
		pools, _ := h.be.ListAgentPools(r.Context(), clusters[i].ResourceGroup, clusters[i].Name)
		out = append(out, toARMCluster(&clusters[i], pools, rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armManagedCluster]{Value: out})
}

// ---- Agent Pool ops ----

func (h *Handler) createOrUpdateAgentPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armAgentPool
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	in := aks.AgentPoolInput{Name: rp.SubResourceName}
	if body.Properties != nil {
		in.Count = body.Properties.Count
		in.VMSize = body.Properties.VMSize
		in.OSDiskSizeGB = body.Properties.OSDiskSizeGB
		in.OSType = body.Properties.OSType
		in.Mode = body.Properties.Mode
		in.OrchestratorVer = body.Properties.OrchestratorVer
		in.ScaleSetPriority = body.Properties.ScaleSetPriority
		in.NodeLabels = fromPtrTags(body.Properties.NodeLabels)
		in.NodeTaints = body.Properties.NodeTaints
		in.MaxPods = body.Properties.MaxPods
		in.OSDiskType = body.Properties.OSDiskType
		in.Type = body.Properties.Type
		body.Properties.armAgentPoolAdvanced.applyTo(&in)
	}

	pool, err := h.be.CreateOrUpdateAgentPool(r.Context(), rp.ResourceGroup, rp.ResourceName, in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAgentPool(pool, rp.Subscription))
}

func (h *Handler) getAgentPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	pool, err := h.be.GetAgentPool(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMAgentPool(pool, rp.Subscription))
}

func (h *Handler) deleteAgentPool(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	writeIdempotentDelete(w, h.be.DeleteAgentPool(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName))
}

//nolint:dupl // sub-resource lists are intentionally typed; sharing via generics adds noise.
func (h *Handler) listAgentPools(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	pools, err := h.be.ListAgentPools(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armAgentPool, 0, len(pools))
	for i := range pools {
		out = append(out, toARMAgentPool(&pools[i], rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armAgentPool]{Value: out})
}

// ---- Maintenance Configuration ops ----

func (h *Handler) createOrUpdateMaintenanceConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armMaintenanceConfig
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	mc, err := h.be.CreateOrUpdateMaintenanceConfig(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, body.Properties,
	)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMMaintenance(mc, rp.Subscription))
}

func (h *Handler) getMaintenanceConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	mc, err := h.be.GetMaintenanceConfig(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMMaintenance(mc, rp.Subscription))
}

func (h *Handler) deleteMaintenanceConfig(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.be.DeleteMaintenanceConfig(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName,
	); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

//nolint:dupl // sub-resource lists are intentionally typed; sharing via generics adds noise.
func (h *Handler) listMaintenanceConfigs(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	configs, err := h.be.ListMaintenanceConfigs(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armMaintenanceConfig, 0, len(configs))
	for i := range configs {
		out = append(out, toARMMaintenance(&configs[i], rp.Subscription))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armMaintenanceConfig]{Value: out})
}

// ---- Credential listing + cert rotation ----

// listClusterCredentials returns a stub kubeconfig that points at the
// data-plane-not-implemented sentinel host. Wave 2 will replace this with a
// real cloudemu-served Kubernetes API endpoint.
func (h *Handler) listClusterCredentials(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.be.GetCluster(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	kubeconfig := h.be.Kubeconfig(rp.ResourceGroup, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, armCredentialResults{
		Kubeconfigs: []armCredentialResult{
			{Name: rp.SubResource, Value: kubeconfig},
		},
	})
}

func (h *Handler) rotateClusterCertificates(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.be.RotateClusterCertificates(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// SDK requires 202/204 on rotateClusterCertificates.
	w.WriteHeader(http.StatusNoContent)
}
