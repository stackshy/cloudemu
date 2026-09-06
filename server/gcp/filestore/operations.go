package filestore

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// instancePatch holds the masked fields of an update.
type instancePatch struct {
	description    string
	labels         map[string]string
	fileShares     []fileShareModel
	setDescription bool
	setLabels      bool
	setFileShares  bool
}

// createInstance handles POST .../instances?instanceId={i} — Create (LRO).
func (h *Handler) createInstance(w http.ResponseWriter, r *http.Request, rt route) {
	instanceID := r.URL.Query().Get("instanceId")
	if instanceID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instanceId query parameter is required")
		return
	}

	var body instanceRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	m, ok := buildModel(w, &body)
	if !ok {
		return
	}

	name := instanceName(rt.project, rt.location, instanceID)
	if err := h.store.create(name, m); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeDoneOperation(w, rt, m)
}

// getInstance handles GET .../instances/{i} — Get.
func (h *Handler) getInstance(w http.ResponseWriter, rt route) {
	m, err := h.store.get(instanceName(rt.project, rt.location, rt.name))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toJSON(m))
}

// listInstances handles GET .../instances — List, scoped to (project, location).
func (h *Handler) listInstances(w http.ResponseWriter, rt route) {
	models := h.store.list(rt.project, rt.location)

	out := make([]instanceJSON, 0, len(models))
	for _, m := range models {
		out = append(out, toJSON(m))
	}

	gcprest.WriteJSON(w, http.StatusOK, listInstancesResponse{Instances: out})
}

// patchInstance handles PATCH .../instances/{i}?updateMask= — Update (LRO).
func (h *Handler) patchInstance(w http.ResponseWriter, r *http.Request, rt route) {
	var body instanceRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	patch, ok := buildPatch(w, r.URL.Query().Get("updateMask"), &body)
	if !ok {
		return
	}

	m, err := h.store.patch(instanceName(rt.project, rt.location, rt.name), &patch)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeDoneOperation(w, rt, m)
}

// deleteInstance handles DELETE .../instances/{i} — Delete (LRO).
func (h *Handler) deleteInstance(w http.ResponseWriter, rt route) {
	name := instanceName(rt.project, rt.location, rt.name)
	if err := h.store.delete(name); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	h.writeDeleteOperation(w, rt)
}

// writeDoneOperation registers and returns a completed operation carrying the
// instance as its response (Create / Update).
func (h *Handler) writeDoneOperation(w http.ResponseWriter, rt route, m *instanceModel) {
	raw, err := json.Marshal(toJSON(m))
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	name := operationName(rt.project, rt.location, h.store.newOpID())
	// Register the response as RawMessage: the shared LRO handler marshals a
	// plain []byte as a base64 string, which would garble a client that polls
	// the operation (SDK/gcloud wait loops) rather than reading the inline
	// response.
	h.ops.Register(name, json.RawMessage(raw))

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: name, Done: true, Response: raw})
}

// writeDeleteOperation registers and returns a completed operation with an empty
// response (Delete).
func (h *Handler) writeDeleteOperation(w http.ResponseWriter, rt route) {
	empty := json.RawMessage("{}")

	name := operationName(rt.project, rt.location, h.store.newOpID())
	h.ops.Register(name, empty)

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: name, Done: true, Response: empty})
}

// buildModel validates and normalizes a create body into a stored model. On any
// validation failure it writes the error and returns ok=false.
func buildModel(w http.ResponseWriter, req *instanceRequest) (*instanceModel, bool) {
	tier, tierOK, present := req.Tier.normalize(tierNames)
	if !present || !tierOK || tier == "TIER_UNSPECIFIED" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "tier is required and must be a valid Tier")
		return nil, false
	}

	shares, ok := buildFileShares(w, req.FileShares)
	if !ok {
		return nil, false
	}

	networks, ok := buildNetworks(w, req.Networks)
	if !ok {
		return nil, false
	}

	return &instanceModel{
		description: req.Description,
		tier:        tier,
		labels:      req.Labels,
		fileShares:  shares,
		networks:    networks,
		etag:        req.Etag,
		kmsKeyName:  req.KmsKeyName,
	}, true
}

func buildFileShares(w http.ResponseWriter, in []fileShareReq) ([]fileShareModel, bool) {
	out := make([]fileShareModel, 0, len(in))

	for i := range in {
		fs := &in[i]

		opts, ok := buildNfsOptions(w, fs.NfsExportOptions)
		if !ok {
			return nil, false
		}

		out = append(out, fileShareModel{
			name:             fs.Name,
			capacityGb:       int64(fs.CapacityGb),
			sourceBackup:     fs.SourceBackup,
			nfsExportOptions: opts,
		})
	}

	return out, true
}

func buildNfsOptions(w http.ResponseWriter, in []nfsExportReq) ([]nfsExportModel, bool) {
	out := make([]nfsExportModel, 0, len(in))

	for i := range in {
		o := &in[i]

		access, ok := normalizeOr(w, o.AccessMode, accessModeNames, defaultAccessMode, "accessMode")
		if !ok {
			return nil, false
		}

		squash, ok := normalizeOr(w, o.SquashMode, squashModeNames, defaultSquashMode, "squashMode")
		if !ok {
			return nil, false
		}

		out = append(out, nfsExportModel{
			ipRanges:   o.IPRanges,
			network:    o.Network,
			accessMode: access,
			squashMode: squash,
			anonUID:    int64(o.AnonUID),
			anonGID:    int64(o.AnonGID),
		})
	}

	return out, true
}

func buildNetworks(w http.ResponseWriter, in []networkReq) ([]networkModel, bool) {
	out := make([]networkModel, 0, len(in))

	for i := range in {
		n := &in[i]

		modes, ok := normalizeModes(w, n.Modes)
		if !ok {
			return nil, false
		}

		connect, ok := normalizeOr(w, n.ConnectMode, connectModeNames, defaultConnectMode, "connectMode")
		if !ok {
			return nil, false
		}

		out = append(out, networkModel{
			network:         n.Network,
			modes:           modes,
			reservedIPRange: n.ReservedIPRange,
			connectMode:     connect,
		})
	}

	return out, true
}

// normalizeModes resolves the modes[] enum array, defaulting to MODE_IPV4 when
// omitted (as real Filestore requires an IPv4 mode).
func normalizeModes(w http.ResponseWriter, in []rawEnum) ([]string, bool) {
	if len(in) == 0 {
		return []string{defaultAddressMode}, true
	}

	out := make([]string, 0, len(in))

	for _, e := range in {
		name, ok, _ := e.normalize(addressModeNames)
		if !ok {
			gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid network mode")
			return nil, false
		}

		out = append(out, name)
	}

	return out, true
}

// normalizeOr resolves an optional enum, substituting def when the field is
// absent or the unspecified value. A present-but-invalid token is a 400.
func normalizeOr(w http.ResponseWriter, e rawEnum, names map[int32]string, def, field string) (string, bool) {
	name, ok, present := e.normalize(names)
	if present && !ok {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid "+field)
		return "", false
	}

	if !present || strings.HasSuffix(name, "_UNSPECIFIED") {
		return def, true
	}

	return name, true
}

// buildPatch turns a masked patch body into an instancePatch.
func buildPatch(w http.ResponseWriter, mask string, req *instanceRequest) (instancePatch, bool) {
	patch := instancePatch{}

	if maskHas(mask, "description") {
		patch.description, patch.setDescription = req.Description, true
	}

	if maskHas(mask, "labels") {
		patch.labels, patch.setLabels = req.Labels, true
	}

	if maskHas(mask, "fileShares", "file_shares") {
		shares, ok := buildFileShares(w, req.FileShares)
		if !ok {
			return instancePatch{}, false
		}

		patch.fileShares, patch.setFileShares = shares, true
	}

	return patch, true
}

// maskHas reports whether an update mask names any of the given field aliases.
// An empty mask means "update every field present in the body".
func maskHas(mask string, fields ...string) bool {
	if strings.TrimSpace(mask) == "" {
		return true
	}

	for _, f := range strings.Split(mask, ",") {
		f = strings.TrimSpace(f)
		for _, want := range fields {
			if f == want {
				return true
			}
		}
	}

	return false
}

// --- model -> wire ---

func toJSON(m *instanceModel) instanceJSON {
	out := instanceJSON{
		Name:        m.name,
		Description: m.description,
		State:       m.state,
		CreateTime:  rfc3339(m.createTime),
		Tier:        m.tier,
		Labels:      m.labels,
		Etag:        m.etag,
		KmsKeyName:  m.kmsKeyName,
	}

	for i := range m.fileShares {
		out.FileShares = append(out.FileShares, fileShareToJSON(&m.fileShares[i]))
	}

	for i := range m.networks {
		out.Networks = append(out.Networks, networkToJSON(&m.networks[i]))
	}

	return out
}

func fileShareToJSON(fs *fileShareModel) fileShareJSON {
	out := fileShareJSON{
		Name:         fs.name,
		CapacityGb:   jsonInt64(fs.capacityGb),
		SourceBackup: fs.sourceBackup,
	}

	for i := range fs.nfsExportOptions {
		o := &fs.nfsExportOptions[i]
		out.NfsExportOptions = append(out.NfsExportOptions, nfsExportJSON{
			IPRanges:   o.ipRanges,
			Network:    o.network,
			AccessMode: o.accessMode,
			SquashMode: o.squashMode,
			AnonUID:    jsonInt64(o.anonUID),
			AnonGID:    jsonInt64(o.anonGID),
		})
	}

	return out
}

func networkToJSON(n *networkModel) networkJSON {
	return networkJSON{
		Network:         n.network,
		Modes:           n.modes,
		ReservedIPRange: n.reservedIPRange,
		IPAddresses:     n.ipAddresses,
		ConnectMode:     n.connectMode,
	}
}
