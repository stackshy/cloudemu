package managedcassandra

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

func (*Handler) dcID(rp *azurearm.ResourcePath, cluster, dc string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, cluster) +
		"/" + subResourceDCs + "/" + dc
}

func (h *Handler) createOrUpdateDataCenter(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body dataCenterResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := mcdriver.CreateDataCenterConfig{
		ClusterName:   rp.ResourceName,
		ResourceGroup: rp.ResourceGroup,
		Name:          rp.SubResourceName,
	}

	if p := body.Properties; p != nil {
		cfg.DataCenterLocation = p.DataCenterLocation
		cfg.DelegatedSubnetID = p.DelegatedSubnetID
		cfg.NodeCount = p.NodeCount
		cfg.DiskCapacity = p.DiskCapacity
		cfg.SKU = p.SKU
		cfg.DiskSKU = p.DiskSKU
		cfg.AvailabilityZone = p.AvailabilityZone != nil && *p.AvailabilityZone
		cfg.Base64EncodedCassandraYamlFragment = p.Base64EncodedCassandraYamlFragment
		cfg.BackupStorageCustomerKeyURI = p.BackupStorageCustomerKeyURI
		cfg.ManagedDiskCustomerKeyURI = p.ManagedDiskCustomerKeyURI
	}

	dc, err := h.db.CreateOrUpdateDataCenter(r.Context(), cfg)
	if err != nil {
		if cerrors.IsNotFound(err) {
			// The only NotFound this create raises is a missing parent cluster;
			// real Azure answers 404 ParentResourceNotFound.
			azurearm.WriteParentNotFound(w, err)
			return
		}

		azurearm.WriteCErr(w, err)

		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDataCenter(dc, h.dcID(rp, rp.ResourceName, dc.Name)))
}

func (h *Handler) getDataCenter(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	dc, err := h.db.GetDataCenter(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDataCenter(dc, h.dcID(rp, rp.ResourceName, dc.Name)))
}

func (h *Handler) updateDataCenter(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body dataCenterResource
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	var patch mcdriver.DataCenterPatch

	if p := body.Properties; p != nil {
		if p.NodeCount > 0 {
			n := p.NodeCount
			patch.NodeCount = &n
		}

		if p.DiskCapacity > 0 {
			d := p.DiskCapacity
			patch.DiskCapacity = &d
		}

		if p.Base64EncodedCassandraYamlFragment != "" {
			frag := p.Base64EncodedCassandraYamlFragment
			patch.Base64EncodedCassandraYamlFragment = &frag
		}
	}

	dc, err := h.db.UpdateDataCenter(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName, patch)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMDataCenter(dc, h.dcID(rp, rp.ResourceName, dc.Name)))
}

func (h *Handler) deleteDataCenter(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.db.DeleteDataCenter(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDataCenters(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	dcs, err := h.db.ListDataCenters(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := armList[dataCenterResource]{Value: make([]dataCenterResource, 0, len(dcs))}
	for i := range dcs {
		out.Value = append(out.Value, toARMDataCenter(&dcs[i], h.dcID(rp, rp.ResourceName, dcs[i].Name)))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}
