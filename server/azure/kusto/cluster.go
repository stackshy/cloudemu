package kusto

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveCluster(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	switch r.Method {
	case http.MethodPut:
		h.createCluster(w, r, kp)
	case http.MethodPatch:
		h.updateCluster(w, r, kp)
	case http.MethodGet:
		h.getCluster(w, kp)
	case http.MethodDelete:
		h.deleteCluster(w, kp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// createCluster serves the PUT LRO. armkusto's ClustersClient.BeginCreateOrUpdate
// wraps this in a poller; returning a synchronous 201/200 whose body already
// carries provisioningState=Succeeded (and no Azure-AsyncOperation/Location
// header) makes PollUntilDone terminate on the first poll, so the poller — a
// real Kusto create is a long LRO — never hangs.
func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	var req createClusterRequest
	if !decodeBody(w, r, &req) {
		return
	}

	location := req.Location
	if location == "" {
		location = defaultLocation
	}

	now := time.Now().UTC()

	h.mu.Lock()

	c, existed := h.clusters.Get(clusterKey(kp.cluster))
	if !existed {
		c = &clusterState{
			Name:          kp.cluster,
			Subscription:  kp.sub,
			ResourceGroup: kp.rg,
			State:         stateRunning,
			CreatedAt:     now,
			Databases:     map[string]*databaseState{},
		}
	}

	c.Location = location
	c.Tags = maps.Clone(req.Tags)
	c.Zones = slices.Clone(req.Zones)
	c.SKU = normalizeSKU(req.SKU)
	c.Properties = req.Properties
	c.UpdatedAt = now
	h.clusters.Set(clusterKey(kp.cluster), c)

	resource := toClusterResource(c)
	h.mu.Unlock()

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, resource)
}

// updateCluster serves the ARM PATCH (ClustersClient.Update / ClusterUpdate):
// the tags object is replaced wholesale when present (resource-level ARM tag
// PATCH is replace, not deep-merge), sku / zones / location are replaced when
// supplied, the mutable cluster properties are overlaid (shallow patch), and
// every untouched field — including the synthesized URIs and run state — is
// preserved. A PATCH on a missing cluster is a 404.
func (h *Handler) updateCluster(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	var req updateClusterRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.Unlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	if req.Location != "" {
		c.Location = req.Location
	}

	if req.Tags != nil {
		c.Tags = maps.Clone(req.Tags)
	}

	if req.SKU != nil {
		c.SKU = normalizeSKU(req.SKU)
	}

	if req.Zones != nil {
		c.Zones = slices.Clone(req.Zones)
	}

	if req.Properties != nil {
		applyClusterPropsPatch(&c.Properties, req.Properties)
	}

	c.UpdatedAt = time.Now().UTC()
	resource := toClusterResource(c)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getCluster(w http.ResponseWriter, kp kustoPath) {
	h.mu.RLock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.RUnlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	resource := toClusterResource(c)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// deleteCluster serves the DELETE LRO. A synchronous 200 (deleted) or 204
// (already absent) with no async header makes armkusto's BeginDelete poller
// terminate on the first poll. The cascade is implicit: the cluster state owns
// every database it holds.
func (h *Handler) deleteCluster(w http.ResponseWriter, kp kustoPath) {
	h.mu.Lock()

	if _, ok := h.lookupCluster(kp); !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	h.clusters.Delete(clusterKey(kp.cluster))
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

// setClusterState serves the Start/Stop LROs by flipping the cluster's state.
// It returns a synchronous 200 with no async header so the BeginStart/BeginStop
// poller finalizes on the first poll.
func (h *Handler) setClusterState(w http.ResponseWriter, kp kustoPath, state string) {
	h.mu.Lock()

	c, ok := h.lookupCluster(kp)
	if !ok {
		h.mu.Unlock()
		writeClusterNotFound(w, kp.cluster)

		return
	}

	c.State = state
	c.UpdatedAt = time.Now().UTC()
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, kp kustoPath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	resources := make([]any, 0)

	for _, c := range h.clusters.SortedValues() {
		if !strings.EqualFold(c.Subscription, kp.sub) {
			continue
		}

		if kp.rg != "" && !strings.EqualFold(c.ResourceGroup, kp.rg) {
			continue
		}

		resources = append(resources, toClusterResource(c))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

func normalizeSKU(in *kustoSKU) kustoSKU {
	if in == nil || in.Name == "" {
		return kustoSKU{Name: "Standard_D11_v2", Tier: "Standard"}
	}

	out := *in
	if out.Tier == "" {
		out.Tier = "Standard"
	}

	return out
}

// applyClusterPropsPatch overlays the client-mutable properties of a
// ClusterUpdate onto the existing cluster properties in place. Server-computed
// fields (state, URIs, provisioningState) are left untouched — toClusterResource
// recomputes them.
func applyClusterPropsPatch(dst, patch *clusterProperties) {
	if patch.EngineType != "" {
		dst.EngineType = patch.EngineType
	}

	if patch.PublicNetworkAccess != "" {
		dst.PublicNetworkAccess = patch.PublicNetworkAccess
	}

	if patch.EnableStreamingIngest != nil {
		dst.EnableStreamingIngest = patch.EnableStreamingIngest
	}

	if patch.EnableDiskEncryption != nil {
		dst.EnableDiskEncryption = patch.EnableDiskEncryption
	}

	if patch.EnableDoubleEncryption != nil {
		dst.EnableDoubleEncryption = patch.EnableDoubleEncryption
	}

	if patch.EnablePurge != nil {
		dst.EnablePurge = patch.EnablePurge
	}

	if patch.EnableAutoStop != nil {
		dst.EnableAutoStop = patch.EnableAutoStop
	}
}

func toClusterResource(c *clusterState) clusterResource {
	created := c.CreatedAt
	updated := c.UpdatedAt
	sku := c.SKU

	props := c.Properties
	props.ProvisioningState = provisioningSucceeded
	props.State = c.State
	props.URI = "https://" + c.Name + "." + c.Location + kustoHost
	props.DataIngestionURI = "https://" + ingestPrefix + c.Name + "." + c.Location + kustoHost

	return clusterResource{
		ID:       azurearm.BuildResourceID(c.Subscription, c.ResourceGroup, providerName, segClusters, c.Name),
		Name:     c.Name,
		Type:     clusterResourceType,
		Location: c.Location,
		Tags:     c.Tags,
		SKU:      &sku,
		Zones:    c.Zones,
		SystemData: &systemData{
			CreatedAt:      &created,
			CreatedByType:  "Application",
			LastModifiedAt: &updated,
		},
		Properties: props,
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return false
	}

	return true
}
