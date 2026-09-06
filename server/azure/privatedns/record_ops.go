package privatedns

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	pddriver "github.com/stackshy/cloudemu/v2/services/privatedns/driver"
)

// createOrUpdateRecord handles PUT .../privateDnsZones/{zone}/{type}/{name}. The
// whole record set arrives in one body and fully REPLACES the stored state.
// RecordSets.CreateOrUpdate returns the provisioned body — 201 on create, 200 on
// update.
func (h *Handler) createOrUpdateRecord(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body recordJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	rs := buildRecord(&body)

	stored, created, err := h.pdns.CreateOrUpdateRecordSet(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResource, rp.SubResourceName, rs)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toRecordJSON(rp, stored))
}

func (h *Handler) getRecord(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.GetRecordSet(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResource, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toRecordJSON(rp, stored))
}

// deleteRecord removes the record set.
func (h *Handler) deleteRecord(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if derr := h.pdns.DeleteRecordSet(
		r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResource, rp.SubResourceName); derr != nil {
		azurearm.WriteCErr(w, derr)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listRecordsByType(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.ListRecordSetsByType(r.Context(), rp.ResourceGroup, rp.ResourceName, rp.SubResource)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeRecordList(w, rp, stored)
}

func (h *Handler) listRecordsAll(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	stored, err := h.pdns.ListRecordSets(r.Context(), rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeRecordList(w, rp, stored)
}

// writeRecordList renders a record-set list envelope, stamping each record's
// per-type URL segment and name.
func (*Handler) writeRecordList(w http.ResponseWriter, rp *azurearm.ResourcePath, stored []pddriver.RecordSet) {
	out := recordListResult{Value: make([]recordJSON, 0, len(stored))}

	for i := range stored {
		scope := *rp
		scope.SubResource = stored[i].RecordType
		scope.SubResourceName = stored[i].Name
		out.Value = append(out.Value, toRecordJSON(&scope, &stored[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// buildRecord maps the ARM request body to the native model: ttl is extracted as
// an int64 so it serializes as an integer, and every other property except the
// computed read-only ones (fqdn, isAutoRegistered, provisioningState) rides
// through verbatim in RecordData.
func buildRecord(body *recordJSON) pddriver.RecordSet {
	return pddriver.RecordSet{
		TTL:        extractTTL(body.Properties),
		RecordData: stripKeys(body.Properties, ttlKey, fqdnKey, isAutoRegisteredKey, provisioningKey),
	}
}

// toRecordJSON reconstructs the ARM record-set body, stamping the id/etag and
// injecting the modeled ttl plus the computed fqdn (trailing dot) and
// isAutoRegistered=false. The record-type arrays ride through from RecordData.
func toRecordJSON(rp *azurearm.ResourcePath, rs *pddriver.RecordSet) recordJSON {
	recordType := strings.ToUpper(rp.SubResource)
	zoneID := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typePrivateZones, rp.ResourceName)
	id := zoneID + "/" + recordType + "/" + rp.SubResourceName

	const injected = 3

	props := make(map[string]any, len(rs.RecordData)+injected)
	for k, v := range rs.RecordData {
		props[k] = v
	}

	if rs.TTL != nil {
		props[ttlKey] = *rs.TTL
	}

	props[fqdnKey] = recordFQDN(rp.SubResourceName, rp.ResourceName)
	props[isAutoRegisteredKey] = false

	return recordJSON{
		ID:         id,
		Name:       rp.SubResourceName,
		Type:       recordResourceType + recordType,
		Etag:       azurearm.ETag(id),
		Properties: props,
	}
}
