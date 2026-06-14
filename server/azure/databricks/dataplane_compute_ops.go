package databricks

import "net/http"

// --- wire types ---

type nodeTypeJSON struct {
	NodeTypeID  string  `json:"node_type_id"`
	Description string  `json:"description"`
	NumCores    float64 `json:"num_cores"`
	MemoryMB    int32   `json:"memory_mb"`
}

type listNodeTypesResponse struct {
	NodeTypes []nodeTypeJSON `json:"node_types"`
}

type sparkVersionJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type sparkVersionsResponse struct {
	Versions []sparkVersionJSON `json:"versions"`
}

type listZonesResponse struct {
	Zones       []string `json:"zones"`
	DefaultZone string   `json:"default_zone"`
}

type submitRunRequest struct {
	RunName string `json:"run_name"`
}

type submitRunResponse struct {
	RunID int64 `json:"run_id"`
}

type cancelAllRequest struct {
	JobID int64 `json:"job_id"`
}

type repairRunResponse struct {
	RepairID int64 `json:"repair_id"`
}

// --- cluster lifecycle + metadata ops ---

func (h *DataPlaneHandler) resizeCluster(w http.ResponseWriter, r *http.Request) {
	var in clusterJSON
	if !dpDecode(w, r, &in) {
		return
	}

	var minW, maxW int32
	if in.Autoscale != nil {
		minW, maxW = in.Autoscale.MinWorkers, in.Autoscale.MaxWorkers
	}

	if err := h.dp.ResizeCluster(r.Context(), in.ClusterID, in.NumWorkers, minW, maxW); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) pinCluster(w http.ResponseWriter, r *http.Request) {
	h.clusterPinAction(w, r, true)
}

func (h *DataPlaneHandler) unpinCluster(w http.ResponseWriter, r *http.Request) {
	h.clusterPinAction(w, r, false)
}

func (h *DataPlaneHandler) clusterPinAction(w http.ResponseWriter, r *http.Request, pin bool) {
	var in clusterID
	if !dpDecode(w, r, &in) {
		return
	}

	var err error
	if pin {
		err = h.dp.PinCluster(r.Context(), in.ClusterID)
	} else {
		err = h.dp.UnpinCluster(r.Context(), in.ClusterID)
	}

	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) listNodeTypes(w http.ResponseWriter, r *http.Request) {
	types, err := h.dp.ListNodeTypes(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]nodeTypeJSON, 0, len(types))
	for _, t := range types {
		out = append(out, nodeTypeJSON{
			NodeTypeID: t.NodeTypeID, Description: t.Description, NumCores: t.NumCores, MemoryMB: t.MemoryMB,
		})
	}

	dpJSON(w, listNodeTypesResponse{NodeTypes: out})
}

func (h *DataPlaneHandler) sparkVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.dp.ListSparkVersions(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]sparkVersionJSON, 0, len(versions))
	for _, v := range versions {
		out = append(out, sparkVersionJSON{Key: v.Key, Name: v.Name})
	}

	dpJSON(w, sparkVersionsResponse{Versions: out})
}

func (h *DataPlaneHandler) listZones(w http.ResponseWriter, r *http.Request) {
	zones, defaultZone, err := h.dp.ListZones(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, listZonesResponse{Zones: zones, DefaultZone: defaultZone})
}

// --- run ops ---

func (h *DataPlaneHandler) submitRun(w http.ResponseWriter, r *http.Request) {
	var in submitRunRequest
	if !dpDecode(w, r, &in) {
		return
	}

	runID, err := h.dp.SubmitRun(r.Context(), in.RunName)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, submitRunResponse{RunID: runID})
}

func (h *DataPlaneHandler) cancelAllRuns(w http.ResponseWriter, r *http.Request) {
	var in cancelAllRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.CancelAllRuns(r.Context(), in.JobID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) deleteRun(w http.ResponseWriter, r *http.Request) {
	var in runIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.DeleteRun(r.Context(), in.RunID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) repairRun(w http.ResponseWriter, r *http.Request) {
	var in runIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	repairID, err := h.dp.RepairRun(r.Context(), in.RunID)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, repairRunResponse{RepairID: repairID})
}
