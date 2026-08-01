package cosmospostgresql

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

const apiVersion = "2023-03-02-preview"

func (*Handler) clusterID(rp *azurearm.ResourcePath, name string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name)
}

// childID builds the resource ID of a cluster sub-resource
// (.../serverGroupsv2/{cluster}/{sub}/{child}).
func (h *Handler) childID(rp *azurearm.ResourcePath, sub, child string) string {
	return h.clusterID(rp, rp.ResourceName) + "/" + sub + "/" + child
}

func (h *Handler) createOrUpdateCluster(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body clusterResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := cpgdriver.CreateClusterConfig{
		Name:          rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Location:      body.Location,
		Tags:          body.Tags,
	}

	if p := body.Properties; p != nil {
		cfg.AdministratorLoginPassword = p.AdministratorLoginPassword
		cfg.CitusVersion = p.CitusVersion
		cfg.PostgresqlVersion = p.PostgresqlVersion
		cfg.CoordinatorServerEdition = p.CoordinatorServerEdition
		cfg.CoordinatorVCores = p.CoordinatorVCores
		cfg.CoordinatorStorageQuotaInMb = p.CoordinatorStorageQuotaInMb
		cfg.CoordinatorEnablePublicIPAccess = derefBool(p.CoordinatorEnablePublicIPAccess)
		cfg.EnableShardsOnCoordinator = derefBool(p.EnableShardsOnCoordinator)
		cfg.NodeServerEdition = p.NodeServerEdition
		cfg.NodeCount = p.NodeCount
		cfg.NodeVCores = p.NodeVCores
		cfg.NodeStorageQuotaInMb = p.NodeStorageQuotaInMb
		cfg.NodeEnablePublicIPAccess = derefBool(p.NodeEnablePublicIPAccess)
		cfg.EnableHa = derefBool(p.EnableHa)
		cfg.PreferredPrimaryZone = p.PreferredPrimaryZone
		cfg.SourceResourceID = p.SourceResourceID
		cfg.SourceLocation = p.SourceLocation
		cfg.MaintenanceWindow = fromWireMW(p.MaintenanceWindow)
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

	patch := cpgdriver.ClusterPatch{Tags: body.Tags}

	if p := body.Properties; p != nil {
		patch.CitusVersion = strPtrIfSet(p.CitusVersion)
		patch.PostgresqlVersion = strPtrIfSet(p.PostgresqlVersion)
		patch.CoordinatorServerEdition = strPtrIfSet(p.CoordinatorServerEdition)
		patch.CoordinatorVCores = intPtrIfSet(p.CoordinatorVCores)
		patch.CoordinatorStorageQuotaInMb = intPtrIfSet(p.CoordinatorStorageQuotaInMb)
		patch.NodeServerEdition = strPtrIfSet(p.NodeServerEdition)
		patch.NodeVCores = intPtrIfSet(p.NodeVCores)
		patch.NodeStorageQuotaInMb = intPtrIfSet(p.NodeStorageQuotaInMb)
		patch.PreferredPrimaryZone = strPtrIfSet(p.PreferredPrimaryZone)
		patch.EnableHa = p.EnableHa
		patch.MaintenanceWindow = fromWireMW(p.MaintenanceWindow)

		if p.NodeCount != 0 {
			nc := p.NodeCount
			patch.NodeCount = &nc
		}
	}

	c, err := h.db.UpdateCluster(r.Context(), rp.ResourceGroup, rp.ResourceName, patch)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMCluster(c, h.clusterID(rp, c.Name)))
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
		clusters []cpgdriver.Cluster
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
		id := azurearm.BuildResourceID(rp.Subscription, clusters[i].ResourceGroup, providerName, resourceType, clusters[i].Name)
		out.Value = append(out.Value, toARMCluster(&clusters[i], id))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// postClusterAction handles restart/start/stop/promote. These are long-running
// actions with no result body; reply 202 + Location so the SDK's Location
// poller reads a terminal status from the operationStatuses URL.
func (*Handler) postClusterAction(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath,
	action func(context.Context, string, string) error,
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	if err := action(r.Context(), rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	opID := rp.ResourceName + "-" + rp.SubResource
	w.Header().Set("Location", asyncStatusURL(r, rp.Subscription, opID))
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
		"?api-version=" + apiVersion
}

// operationStatus reports a completed LRO for the poll the SDK issues.
func (*Handler) operationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]string{"status": "Succeeded"})
}

func (h *Handler) checkNameAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var body nameAvailabilityRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	na, err := h.db.CheckNameAvailability(r.Context(), body.Name, body.Type)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, nameAvailabilityResult{
		Name: na.Name, Type: na.Type, NameAvailable: boolPtr(na.NameAvailable), Message: na.Message,
	})
}

func derefBool(p *bool) bool { return p != nil && *p }

func strPtrIfSet(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func intPtrIfSet(v int) *int {
	if v == 0 {
		return nil
	}

	return &v
}

func fromWireMW(mw *maintenanceWindow) *cpgdriver.MaintenanceWindow {
	if mw == nil {
		return nil
	}

	return &cpgdriver.MaintenanceWindow{
		CustomWindow: mw.CustomWindow, DayOfWeek: mw.DayOfWeek,
		StartHour: mw.StartHour, StartMinute: mw.StartMinute,
	}
}
