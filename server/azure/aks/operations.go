package aks

import (
	"net/http"

	"github.com/stackshy/cloudemu/providers/azure/aks"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// ---- Managed Cluster ops ----

func (h *Handler) createOrUpdateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body armManagedCluster
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cluster, err := h.be.CreateOrUpdateCluster(r.Context(), buildClusterInput(&body, rp))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	pools, _ := h.be.ListAgentPools(r.Context(), rp.ResourceGroup, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(cluster, pools, rp.Subscription))
}

func buildClusterInput(body *armManagedCluster, rp *azurearm.ResourcePath) aks.ClusterInput {
	in := aks.ClusterInput{
		Subscription:  rp.Subscription,
		ResourceGroup: rp.ResourceGroup,
		Name:          rp.ResourceName,
		Location:      body.Location,
		Tags:          fromPtrTags(body.Tags),
	}

	if body.Properties != nil {
		in.KubernetesVersion = body.Properties.KubernetesVersion
		in.DNSPrefix = body.Properties.DNSPrefix
		in.NodeResourceGroup = body.Properties.NodeResourceGroup

		for i := range body.Properties.AgentPoolProfiles {
			p := &body.Properties.AgentPoolProfiles[i]
			in.AgentPools = append(in.AgentPools, aks.AgentPoolInput{
				Name:            p.Name,
				Count:           p.Count,
				VMSize:          p.VMSize,
				OSDiskSizeGB:    p.OSDiskSizeGB,
				OSType:          p.OSType,
				Mode:            p.Mode,
				OrchestratorVer: p.OrchestratorVer,
				NodeLabels:      fromPtrTags(p.NodeLabels),
				NodeTaints:      p.NodeTaints,
			})
		}
	}

	return in
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
	if err := h.be.DeleteCluster(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// SDK accepts 202/204 on DELETE; 204 keeps the LRO poller terminal.
	w.WriteHeader(http.StatusNoContent)
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
		in.NodeLabels = fromPtrTags(body.Properties.NodeLabels)
		in.NodeTaints = body.Properties.NodeTaints
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
	if err := h.be.DeleteAgentPool(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// SDK requires 202/204 on agent-pool DELETE.
	w.WriteHeader(http.StatusNoContent)
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

	kubeconfig := h.be.StubKubeconfig(rp.ResourceGroup, rp.ResourceName)

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
