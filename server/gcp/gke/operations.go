package gke

import (
	"net/http"

	"github.com/stackshy/cloudemu/providers/gcp/gke"
)

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body createClusterReq
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Cluster == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "cluster body required")
		return
	}

	in := gke.CreateClusterInput{
		Name:              body.Cluster.Name,
		Location:          p.location,
		Description:       body.Cluster.Description,
		Network:           body.Cluster.Network,
		Subnetwork:        body.Cluster.Subnetwork,
		InitialNodeCount:  body.Cluster.InitialNodeCount,
		LoggingService:    body.Cluster.LoggingService,
		MonitoringService: body.Cluster.MonitoringService,
		ResourceLabels:    body.Cluster.ResourceLabels,
	}

	for i := range body.Cluster.NodePools {
		in.NodePools = append(in.NodePools, nodePoolSpecFromWire(&body.Cluster.NodePools[i]))
	}

	_, op, err := h.gke.CreateCluster(r.Context(), &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func nodePoolSpecFromWire(np *gkeNodePool) gke.NodePoolSpec {
	spec := gke.NodePoolSpec{
		Name:             np.Name,
		InitialNodeCount: np.InitialNodeCount,
		Version:          np.Version,
	}

	if np.Config != nil {
		spec.MachineType = np.Config.MachineType
		spec.DiskSizeGB = np.Config.DiskSizeGb
	}

	if np.Autoscaling != nil {
		spec.AutoscalingOn = np.Autoscaling.Enabled
		spec.AutoscalingMin = np.Autoscaling.MinNodeCount
		spec.AutoscalingMax = np.Autoscaling.MaxNodeCount
	}

	return spec
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, p *gkePath) {
	c, err := h.gke.GetCluster(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	pools, _ := h.gke.ListNodePools(r.Context(), p.location, p.name)

	writeJSON(w, http.StatusOK, toClusterResource(c, p.project, pools))
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, p *gkePath) {
	clusters, err := h.gke.ListClusters(r.Context(), p.location)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listClustersResp{Clusters: make([]gkeCluster, 0, len(clusters))}

	for i := range clusters {
		pools, _ := h.gke.ListNodePools(r.Context(), clusters[i].Location, clusters[i].Name)
		out.Clusters = append(out.Clusters, toClusterResource(&clusters[i], p.project, pools))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body updateClusterReq
	if !decodeJSON(w, r, &body) {
		return
	}

	in := gke.UpdateClusterInput{}

	if body.Update != nil {
		in.NodeVersion = body.Update.DesiredNodeVersion
		in.MasterVersion = body.Update.DesiredMasterVersion
		in.LoggingService = body.Update.DesiredLoggingService
		in.MonitoringService = body.Update.DesiredMonitoringService
		in.ResourceLabels = body.Update.DesiredResourceLabels
	}

	op, err := h.gke.UpdateCluster(r.Context(), p.location, p.name, in)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, p *gkePath) {
	op, err := h.gke.DeleteCluster(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

//nolint:gocyclo // single switch over GKE cluster :setX actions; flat dispatch is clearest.
func (h *Handler) clusterAction(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	switch p.action {
	case "setLogging":
		h.setLogging(w, r, p)
	case "setMonitoring":
		h.setMonitoring(w, r, p)
	case "setMasterAuth":
		h.setMasterAuth(w, r, p)
	case "setLegacyAbac":
		h.setLegacyAbac(w, r, p)
	case "setNetworkPolicy":
		h.setNetworkPolicy(w, r, p)
	case "setMaintenancePolicy":
		h.setMaintenancePolicy(w, r, p)
	case "setResourceLabels":
		h.setResourceLabels(w, r, p)
	case "startIpRotation":
		h.startIPRotation(w, r, p)
	case "completeIpRotation":
		h.completeIPRotation(w, r, p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported action: "+p.action)
	}
}

func (h *Handler) setLogging(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setLoggingReq
	if !decodeJSON(w, r, &body) {
		return
	}

	op, err := h.gke.SetClusterLogging(r.Context(), p.location, p.name, body.LoggingService)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setMonitoring(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setMonitoringReq
	if !decodeJSON(w, r, &body) {
		return
	}

	op, err := h.gke.SetClusterMonitoring(r.Context(), p.location, p.name, body.MonitoringService)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setMasterAuth(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setMasterAuthReq
	if !decodeJSON(w, r, &body) {
		return
	}

	username := ""
	if body.Update != nil {
		username = body.Update.Username
	}

	op, err := h.gke.SetMasterAuth(r.Context(), p.location, p.name, username)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setLegacyAbac(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setLegacyAbacReq
	if !decodeJSON(w, r, &body) {
		return
	}

	op, err := h.gke.SetLegacyAbac(r.Context(), p.location, p.name, body.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setNetworkPolicy(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setNetworkPolicyReq
	if !decodeJSON(w, r, &body) {
		return
	}

	enabled := false
	if body.NetworkPolicy != nil {
		enabled = body.NetworkPolicy.Enabled
	}

	op, err := h.gke.SetNetworkPolicy(r.Context(), p.location, p.name, enabled)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setMaintenancePolicy(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setMaintenancePolicyReq
	if !decodeJSON(w, r, &body) {
		return
	}

	window := ""
	if body.MaintenancePolicy != nil &&
		body.MaintenancePolicy.Window != nil &&
		body.MaintenancePolicy.Window.DailyMaintenanceWindow != nil {
		window = body.MaintenancePolicy.Window.DailyMaintenanceWindow.StartTime
	}

	op, err := h.gke.SetMaintenancePolicy(r.Context(), p.location, p.name, window)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setResourceLabels(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setLabelsReq
	if !decodeJSON(w, r, &body) {
		return
	}

	op, err := h.gke.SetResourceLabels(r.Context(), p.location, p.name, body.ResourceLabels)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) startIPRotation(w http.ResponseWriter, r *http.Request, p *gkePath) {
	op, err := h.gke.StartIPRotation(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) completeIPRotation(w http.ResponseWriter, r *http.Request, p *gkePath) {
	op, err := h.gke.CompleteIPRotation(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

// node-pool routes

func (h *Handler) createNodePool(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body createNodePoolReq
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.NodePool == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "nodePool body required")
		return
	}

	spec := nodePoolSpecFromWire(body.NodePool)

	_, op, err := h.gke.CreateNodePool(r.Context(), p.location, p.name, &spec)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) getNodePool(w http.ResponseWriter, r *http.Request, p *gkePath) {
	np, err := h.gke.GetNodePool(r.Context(), p.location, p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toNodePoolResource(np, p.project))
}

func (h *Handler) listNodePools(w http.ResponseWriter, r *http.Request, p *gkePath) {
	pools, err := h.gke.ListNodePools(r.Context(), p.location, p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listNodePoolsResp{NodePools: make([]gkeNodePool, 0, len(pools))}
	for i := range pools {
		out.NodePools = append(out.NodePools, toNodePoolResource(&pools[i], p.project))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) updateNodePool(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body updateNodePoolReq
	if !decodeJSON(w, r, &body) {
		return
	}

	in := gke.UpdateNodePoolInput{
		NodeVersion: body.NodeVersion,
		MachineType: body.MachineType,
		DiskSizeGB:  body.DiskSizeGb,
	}

	op, err := h.gke.UpdateNodePool(r.Context(), p.location, p.name, p.subName, in)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) deleteNodePool(w http.ResponseWriter, r *http.Request, p *gkePath) {
	op, err := h.gke.DeleteNodePool(r.Context(), p.location, p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) nodePoolAction(w http.ResponseWriter, r *http.Request, p *gkePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	switch p.action {
	case "setSize":
		h.setNodePoolSize(w, r, p)
	case "setAutoscaling":
		h.setNodePoolAutoscaling(w, r, p)
	case "setManagement":
		h.setNodePoolManagement(w, r, p)
	case "rollback":
		h.rollbackNodePool(w, r, p)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unsupported node-pool action: "+p.action)
	}
}

func (h *Handler) setNodePoolSize(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setNodePoolSizeReq
	if !decodeJSON(w, r, &body) {
		return
	}

	op, err := h.gke.SetNodePoolSize(r.Context(), p.location, p.name, p.subName, body.NodeCount)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setNodePoolAutoscaling(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setNodePoolAutoscalingReq
	if !decodeJSON(w, r, &body) {
		return
	}

	on, minN, maxN := false, int64(0), int64(0)
	if body.Autoscaling != nil {
		on = body.Autoscaling.Enabled
		minN = body.Autoscaling.MinNodeCount
		maxN = body.Autoscaling.MaxNodeCount
	}

	op, err := h.gke.SetNodePoolAutoscaling(r.Context(), p.location, p.name, p.subName, on, minN, maxN)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) setNodePoolManagement(w http.ResponseWriter, r *http.Request, p *gkePath) {
	var body setNodePoolManagementReq
	if !decodeJSON(w, r, &body) {
		return
	}

	autoUpgrade, autoRepair := false, false
	if body.Management != nil {
		autoUpgrade = body.Management.AutoUpgrade
		autoRepair = body.Management.AutoRepair
	}

	op, err := h.gke.SetNodePoolManagement(r.Context(), p.location, p.name, p.subName, autoUpgrade, autoRepair)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}

func (h *Handler) rollbackNodePool(w http.ResponseWriter, r *http.Request, p *gkePath) {
	op, err := h.gke.RollbackNodePool(r.Context(), p.location, p.name, p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toOperationResource(op, p.project))
}
