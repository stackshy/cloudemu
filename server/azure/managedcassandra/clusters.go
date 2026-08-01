package managedcassandra

import (
	"context"
	"fmt"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

func (*Handler) clusterID(rp *azurearm.ResourcePath, name string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name)
}

func (h *Handler) createOrUpdateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body clusterResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := mcdriver.CreateClusterConfig{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      body.Location,
		Tags:          body.Tags,
	}

	if p := body.Properties; p != nil {
		cfg.CassandraVersion = p.CassandraVersion
		cfg.ClusterNameOverride = p.ClusterNameOverride
		cfg.DelegatedManagementSubnetID = p.DelegatedManagementSubnetID
		cfg.AuthenticationMethod = p.AuthenticationMethod
		cfg.InitialCassandraAdminPassword = p.InitialCassandraAdminPassword
		cfg.HoursBetweenBackups = p.HoursBetweenBackups
		cfg.RepairEnabled = p.RepairEnabled != nil && *p.RepairEnabled
		cfg.CassandraAuditLoggingEnabled = p.CassandraAuditLoggingEnabled != nil && *p.CassandraAuditLoggingEnabled
		cfg.ClientCertificates = pems(p.ClientCertificates)
		cfg.ExternalGossipCertificates = pems(p.ExternalGossipCertificates)
		cfg.ExternalSeedNodes = ipAddrs(p.ExternalSeedNodes)
	}

	c, err := h.db.CreateOrUpdateCluster(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(c, h.clusterID(rp, c.Name)))
}

func (h *Handler) getCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	c, err := h.db.GetCluster(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(c, h.clusterID(rp, c.Name)))
}

func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body clusterResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	patch := mcdriver.ClusterPatch{Tags: body.Tags}
	if p := body.Properties; p != nil {
		patch.RepairEnabled = p.RepairEnabled
		patch.AuthenticationMethod = strPtrIfSet(p.AuthenticationMethod)
		patch.ExternalSeedNodes = ipAddrs(p.ExternalSeedNodes)
		patch.ClientCertificates = pems(p.ClientCertificates)

		if p.HoursBetweenBackups > 0 {
			hbb := p.HoursBetweenBackups
			patch.HoursBetweenBackups = &hbb
		}
	}

	c, err := h.db.UpdateCluster(r.Context(), rp.ResourceGroup, rp.ResourceName, patch)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(c, h.clusterID(rp, c.Name)))
}

func strPtrIfSet(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteCluster(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	var (
		clusters []mcdriver.Cluster
		err      error
	)

	if rp.ResourceGroup == "" {
		clusters, err = h.db.ListClustersBySubscription(r.Context())
	} else {
		clusters, err = h.db.ListClustersByResourceGroup(r.Context(), rp.ResourceGroup)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := armList[clusterResource]{Value: make([]clusterResource, 0, len(clusters))}

	for i := range clusters {
		rg := clusters[i].ResourceGroup
		id := azurearm.BuildResourceID(rp.Subscription, rg, providerName, resourceType, clusters[i].Name)
		out.Value = append(out.Value, toARMCluster(&clusters[i], id))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (*Handler) postClusterAction(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	action func(context.Context, string, string) (*mcdriver.Cluster, error),
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if _, err := action(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// deallocate/start are long-running actions with no result body; reply
	// 202 + Azure-AsyncOperation so the SDK poller reads terminal status from
	// the operationStatuses URL.
	opID := rp.ResourceName + "-" + rp.SubResource
	w.Header().Set("Azure-AsyncOperation", asyncStatusURL(r, rp.Subscription, opID))
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// asyncStatusURL builds an operationStatuses URL for a synthetic operation id.
func asyncStatusURL(r *http.Request, sub, opID string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host +
		"/subscriptions/" + sub +
		"/providers/" + providerName + "/" + resourceLocations + "/global/" + subOperationStatuses + "/" + opID +
		"?api-version=2024-11-15"
}

// operationStatus reports a completed LRO. When the id maps to a stored
// InvokeCommand result, the result body is returned so the Location poller can
// unmarshal it; otherwise a terminal Azure-AsyncOperation status is returned.
func (h *Handler) operationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	rp, _ := azurearm.ParsePath(r.URL.Path)

	if v, ok := h.invokeResults.LoadAndDelete(rp.SubResourceName); ok {
		azurearm.WriteJSON(w, http.StatusOK, commandOutput{CommandOutput: v.(string)})
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{"status": "Succeeded"})
}

func (h *Handler) invokeCommand(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var body commandPostBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	out, err := h.db.InvokeCommand(r.Context(), rp.ResourceGroup, rp.ResourceName, body.Command, body.Host)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// InvokeCommand is a long-running action returning a result body. Stash the
	// output under an operation id and reply 202 + Location so the SDK's LRO
	// poller fetches it from the operationStatuses URL.
	opID := fmt.Sprintf("%s-invoke-%d", rp.ResourceName, h.opCounter.Add(1))
	h.invokeResults.Store(opID, out)

	w.Header().Set("Location", asyncStatusURL(r, rp.Subscription, opID))
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) clusterStatus(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	status, err := h.db.ClusterStatus(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := clusterStatus{ReaperStatus: &reaperStatus{Healthy: boolPtr(status.ReaperStatus)}}

	byDC := map[string]int{}

	for i := range status.Nodes {
		n := &status.Nodes[i]

		idx, ok := byDC[n.DataCenter]
		if !ok {
			idx = len(out.DataCenters)
			byDC[n.DataCenter] = idx

			out.DataCenters = append(out.DataCenters, statusDataCenter{Name: n.DataCenter})
		}

		out.DataCenters[idx].Nodes = append(out.DataCenters[idx].Nodes, statusNode{
			Address: n.Address, State: n.State, Rack: n.Rack, Load: n.Load,
		})
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}
