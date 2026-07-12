package route53

import (
	"encoding/xml"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// listMaxItems is the fixed page size echoed back to the SDK; the mock never
// paginates, so IsTruncated is always false.
const listMaxItems = 100

func (h *Handler) createHostedZone(w http.ResponseWriter, r *http.Request) {
	var req createHostedZoneRequest
	if !decodeXML(w, r, &req) {
		return
	}

	cfg := dnsdriver.ZoneConfig{Name: req.Name}
	if req.HostedZoneConfig != nil {
		cfg.Private = req.HostedZoneConfig.PrivateZone
	}

	info, err := h.dns.CreateZone(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Echo the caller's CallerReference back faithfully (the driver doesn't
	// persist it, so this is only recoverable on the create response).
	hz := toHostedZoneXML(info)
	if req.CallerReference != "" {
		hz.CallerReference = req.CallerReference
	}

	wire.WriteXML(w, http.StatusCreated, createHostedZoneResponse{
		Xmlns:      xmlns,
		HostedZone: hz,
		ChangeInfo: newChangeInfo(),
	})
}

func (h *Handler) getHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	info, err := h.dns.GetZone(r.Context(), trimZonePrefix(id))
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, getHostedZoneResponse{
		Xmlns:      xmlns,
		HostedZone: toHostedZoneXML(info),
	})
}

func (h *Handler) listHostedZones(w http.ResponseWriter, r *http.Request) {
	infos, err := h.dns.ListZones(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	zones := make([]hostedZoneXML, 0, len(infos))
	for i := range infos {
		zones = append(zones, toHostedZoneXML(&infos[i]))
	}

	wire.WriteXML(w, http.StatusOK, listHostedZonesResponse{
		Xmlns:       xmlns,
		HostedZones: zones,
		IsTruncated: false,
		MaxItems:    listMaxItems,
	})
}

func (h *Handler) deleteHostedZone(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.dns.DeleteZone(r.Context(), trimZonePrefix(id)); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, changeResourceRecordSetsResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// changeResourceRecordSets applies a CREATE/UPSERT/DELETE batch against the
// zone. Changes are applied in order; the whole batch shares one INSYNC
// ChangeInfo, matching Route 53's atomic-batch semantics closely enough for
// the SDK's change poller.
func (h *Handler) changeResourceRecordSets(w http.ResponseWriter, r *http.Request, id string) {
	var req changeResourceRecordSetsRequest
	if !decodeXML(w, r, &req) {
		return
	}

	zoneID := trimZonePrefix(id)

	for i := range req.ChangeBatch.Changes {
		if err := h.applyChange(r, zoneID, &req.ChangeBatch.Changes[i]); err != nil {
			writeChangeErr(w, err)
			return
		}
	}

	wire.WriteXML(w, http.StatusOK, changeResourceRecordSetsResponse{
		Xmlns:      xmlns,
		ChangeInfo: newChangeInfo(),
	})
}

// applyChange executes a single record change against the driver.
func (h *Handler) applyChange(r *http.Request, zoneID string, ch *changeItem) error {
	rr := &ch.ResourceRecordSet
	cfg := recordConfig(zoneID, rr)

	switch ch.Action {
	case actionDelete:
		return h.dns.DeleteRecord(r.Context(), zoneID, rr.Name, rr.Type)
	case actionCreate:
		_, err := h.dns.CreateRecord(r.Context(), cfg)
		return err
	case actionUpsert:
		return h.upsertRecord(r, cfg)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported change action %q", ch.Action)
	}
}

// upsertRecord updates the record if it already exists, otherwise creates it —
// Route 53's UPSERT semantics. Only a genuine not-found routes to create; any
// other GetRecord error (e.g. an injected/transient failure) is propagated so
// an existing record isn't misrouted into a create → spurious conflict.
func (h *Handler) upsertRecord(r *http.Request, cfg dnsdriver.RecordConfig) error {
	_, err := h.dns.GetRecord(r.Context(), cfg.ZoneID, cfg.Name, cfg.Type)
	switch {
	case err == nil:
		_, uerr := h.dns.UpdateRecord(r.Context(), cfg)
		return uerr
	case cerrors.IsNotFound(err):
		_, cerr := h.dns.CreateRecord(r.Context(), cfg)
		return cerr
	default:
		return err
	}
}

func (h *Handler) listResourceRecordSets(w http.ResponseWriter, r *http.Request, id string) {
	records, err := h.dns.ListRecords(r.Context(), trimZonePrefix(id))
	if err != nil {
		writeErr(w, err)
		return
	}

	sets := make([]resourceRecordSetXML, 0, len(records))
	for i := range records {
		sets = append(sets, toRecordSetXML(&records[i]))
	}

	wire.WriteXML(w, http.StatusOK, listResourceRecordSetsResponse{
		Xmlns:              xmlns,
		ResourceRecordSets: sets,
		IsTruncated:        false,
		MaxItems:           listMaxItems,
	})
}

// recordConfig builds a driver RecordConfig from a parsed record set element.
func recordConfig(zoneID string, rr *resourceRecordSetXML) dnsdriver.RecordConfig {
	values := make([]string, 0, len(rr.ResourceRecords))
	for _, v := range rr.ResourceRecords {
		values = append(values, v.Value)
	}

	cfg := dnsdriver.RecordConfig{
		ZoneID: zoneID,
		Name:   rr.Name,
		Type:   rr.Type,
		Values: values,
		SetID:  rr.SetIdentifier,
	}

	if rr.TTL != nil {
		cfg.TTL = int(*rr.TTL)
	}

	if rr.Weight != nil {
		w := int(*rr.Weight)
		cfg.Weight = &w
	}

	return cfg
}

// newChangeInfo returns a synthetic INSYNC ChangeInfo for a mutating op.
func newChangeInfo() changeInfoXML {
	return changeInfoXML{
		Id:          changeID,
		Status:      changeStatusInsync,
		SubmittedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// decodeXML reads an XML request body into v, writing an InvalidInput error and
// returning false on a decode failure.
func decodeXML(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := xml.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidInput", "invalid XML: "+err.Error())
		return false
	}

	return true
}

// writeError writes a Route 53 XML error response with the given status.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	wire.WriteXML(w, status, errorResponse{
		Xmlns: xmlns,
		Error: errorXML{Code: code, Message: msg},
	})
}

// writeErr maps a canonical cloudemu error to a Route 53 XML error response.
// It is for zone-level operations (Get/Delete/CreateHostedZone), where a
// missing or duplicate resource is the zone itself.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NoSuchHostedZone", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "HostedZoneAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidChangeBatch", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

// writeChangeErr maps a driver error from a ChangeResourceRecordSets batch. The
// zone is known to exist here, so a missing/duplicate *record* is a bad change
// batch — real Route 53 returns InvalidChangeBatch (400), not the zone-level
// NoSuchHostedZone/HostedZoneAlreadyExists codes.
func writeChangeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err), cerrors.IsAlreadyExists(err), cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "InvalidChangeBatch", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "InvalidInput", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
