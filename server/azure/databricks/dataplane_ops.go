package databricks

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	dbxdriver "github.com/stackshy/cloudemu/databricks/driver"
)

// --- wire types ---

type autoscaleJSON struct {
	MinWorkers int32 `json:"min_workers"`
	MaxWorkers int32 `json:"max_workers"`
}

type instancePoolJSON struct {
	InstancePoolID   string `json:"instance_pool_id,omitempty"`
	InstancePoolName string `json:"instance_pool_name"`
	NodeTypeID       string `json:"node_type_id"`
	State            string `json:"state,omitempty"`
	MinIdleInstances int32  `json:"min_idle_instances,omitempty"`
	MaxCapacity      int32  `json:"max_capacity,omitempty"`
}

type instancePoolID struct {
	InstancePoolID string `json:"instance_pool_id"`
}

type listPoolsResponse struct {
	InstancePools []instancePoolJSON `json:"instance_pools"`
}

type clusterJSON struct {
	ClusterID    string         `json:"cluster_id,omitempty"`
	ClusterName  string         `json:"cluster_name,omitempty"`
	SparkVersion string         `json:"spark_version"`
	NodeTypeID   string         `json:"node_type_id"`
	State        string         `json:"state,omitempty"`
	NumWorkers   int32          `json:"num_workers,omitempty"`
	Autoscale    *autoscaleJSON `json:"autoscale,omitempty"`
}

type clusterID struct {
	ClusterID string `json:"cluster_id"`
}

type listClustersResponse struct {
	Clusters []clusterJSON `json:"clusters"`
}

type createIDResponse struct {
	ClusterID      string `json:"cluster_id,omitempty"`
	InstancePoolID string `json:"instance_pool_id,omitempty"`
}

type jobResponse struct {
	JobID           int64           `json:"job_id"`
	CreatorUserName string          `json:"creator_user_name,omitempty"`
	CreatedTime     int64           `json:"created_time,omitempty"`
	Settings        json.RawMessage `json:"settings,omitempty"`
}

type createJobResponse struct {
	JobID int64 `json:"job_id"`
}

type listJobsResponse struct {
	Jobs    []jobResponse `json:"jobs"`
	HasMore bool          `json:"has_more"`
}

type jobIDRequest struct {
	JobID int64 `json:"job_id"`
}

type jobUpdateRequest struct {
	JobID       int64           `json:"job_id"`
	NewSettings json.RawMessage `json:"new_settings"`
}

type runNowResponse struct {
	RunID       int64 `json:"run_id"`
	NumberInJob int64 `json:"number_in_job"`
}

type permissionLevelJSON struct {
	PermissionLevel string `json:"permission_level"`
}

type accessControlRequest struct {
	UserName             string `json:"user_name,omitempty"`
	GroupName            string `json:"group_name,omitempty"`
	ServicePrincipalName string `json:"service_principal_name,omitempty"`
	PermissionLevel      string `json:"permission_level"`
}

type accessControlResponse struct {
	UserName             string                `json:"user_name,omitempty"`
	GroupName            string                `json:"group_name,omitempty"`
	ServicePrincipalName string                `json:"service_principal_name,omitempty"`
	AllPermissions       []permissionLevelJSON `json:"all_permissions"`
}

type permissionsRequest struct {
	AccessControlList []accessControlRequest `json:"access_control_list"`
}

type permissionsResponse struct {
	ObjectID          string                  `json:"object_id"`
	ObjectType        string                  `json:"object_type"`
	AccessControlList []accessControlResponse `json:"access_control_list"`
}

// --- instance pools ---

func (h *DataPlaneHandler) servePools(w http.ResponseWriter, r *http.Request, action string) {
	dispatch(w, r, action, map[string]dpRoute{
		actCreate: {http.MethodPost, h.createPool},
		actGet:    {http.MethodGet, h.getPool},
		actList:   {http.MethodGet, h.listPools},
		actEdit:   {http.MethodPost, h.editPool},
		actDelete: {http.MethodPost, h.deletePool},
	})
}

func (h *DataPlaneHandler) createPool(w http.ResponseWriter, r *http.Request) {
	var in instancePoolJSON
	if !dpDecode(w, r, &in) {
		return
	}

	pool, err := h.dp.CreateInstancePool(r.Context(), dbxdriver.InstancePoolConfig{
		Name: in.InstancePoolName, NodeTypeID: in.NodeTypeID,
		MinIdleInstances: in.MinIdleInstances, MaxCapacity: in.MaxCapacity,
	})
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, createIDResponse{InstancePoolID: pool.ID})
}

func (h *DataPlaneHandler) getPool(w http.ResponseWriter, r *http.Request) {
	pool, err := h.dp.GetInstancePool(r.Context(), r.URL.Query().Get("instance_pool_id"))
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toPoolJSON(pool))
}

func (h *DataPlaneHandler) listPools(w http.ResponseWriter, r *http.Request) {
	pools, err := h.dp.ListInstancePools(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]instancePoolJSON, 0, len(pools))
	for i := range pools {
		out = append(out, toPoolJSON(&pools[i]))
	}

	dpJSON(w, listPoolsResponse{InstancePools: out})
}

func (h *DataPlaneHandler) editPool(w http.ResponseWriter, r *http.Request) {
	var in instancePoolJSON
	if !dpDecode(w, r, &in) {
		return
	}

	err := h.dp.EditInstancePool(r.Context(), in.InstancePoolID, dbxdriver.InstancePoolConfig{
		Name: in.InstancePoolName, NodeTypeID: in.NodeTypeID,
		MinIdleInstances: in.MinIdleInstances, MaxCapacity: in.MaxCapacity,
	})
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) deletePool(w http.ResponseWriter, r *http.Request) {
	var in instancePoolID
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.DeleteInstancePool(r.Context(), in.InstancePoolID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

// --- clusters ---

func (h *DataPlaneHandler) serveClusters(w http.ResponseWriter, r *http.Request, action string) {
	lifecycle := func(w http.ResponseWriter, r *http.Request) { h.clusterLifecycle(w, r, action) }

	dispatch(w, r, action, map[string]dpRoute{
		actCreate:          {http.MethodPost, h.createCluster},
		actGet:             {http.MethodGet, h.getCluster},
		actList:            {http.MethodGet, h.listClusters},
		actEdit:            {http.MethodPost, h.editCluster},
		actDelete:          {http.MethodPost, lifecycle},
		actPermanentDelete: {http.MethodPost, lifecycle},
		actStart:           {http.MethodPost, lifecycle},
		actRestart:         {http.MethodPost, lifecycle},
		actResize:          {http.MethodPost, h.resizeCluster},
		actPin:             {http.MethodPost, h.pinCluster},
		actUnpin:           {http.MethodPost, h.unpinCluster},
		actListNodeTypes:   {http.MethodGet, h.listNodeTypes},
		actSparkVersions:   {http.MethodGet, h.sparkVersions},
		actListZones:       {http.MethodGet, h.listZones},
	})
}

func (h *DataPlaneHandler) createCluster(w http.ResponseWriter, r *http.Request) {
	var in clusterJSON
	if !dpDecode(w, r, &in) {
		return
	}

	cluster, err := h.dp.CreateCluster(r.Context(), clusterConfig(&in))
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, createIDResponse{ClusterID: cluster.ID})
}

func (h *DataPlaneHandler) getCluster(w http.ResponseWriter, r *http.Request) {
	cluster, err := h.dp.GetCluster(r.Context(), r.URL.Query().Get("cluster_id"))
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toClusterJSON(cluster))
}

func (h *DataPlaneHandler) listClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := h.dp.ListClusters(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]clusterJSON, 0, len(clusters))
	for i := range clusters {
		out = append(out, toClusterJSON(&clusters[i]))
	}

	dpJSON(w, listClustersResponse{Clusters: out})
}

func (h *DataPlaneHandler) editCluster(w http.ResponseWriter, r *http.Request) {
	var in clusterJSON
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.EditCluster(r.Context(), in.ClusterID, clusterConfig(&in)); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

// clusterLifecycle handles delete/permanent-delete/start/restart, all of which
// take {cluster_id} and return an empty body.
func (h *DataPlaneHandler) clusterLifecycle(w http.ResponseWriter, r *http.Request, action string) {
	var in clusterID
	if !dpDecode(w, r, &in) {
		return
	}

	var err error

	switch action {
	case actDelete:
		err = h.dp.DeleteCluster(r.Context(), in.ClusterID)
	case actPermanentDelete:
		err = h.dp.PermanentDeleteCluster(r.Context(), in.ClusterID)
	case actStart:
		err = h.dp.StartCluster(r.Context(), in.ClusterID)
	case actRestart:
		err = h.dp.RestartCluster(r.Context(), in.ClusterID)
	default:
		dpError(w, http.StatusNotFound, "ENDPOINT_NOT_FOUND", "unknown cluster action: "+action)

		return
	}

	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

// --- jobs ---

func (h *DataPlaneHandler) serveJobs(w http.ResponseWriter, r *http.Request, action string) {
	mutate := func(w http.ResponseWriter, r *http.Request) { h.mutateJob(w, r, action) }

	dispatch(w, r, action, map[string]dpRoute{
		actCreate: {http.MethodPost, h.createJob},
		actGet:    {http.MethodGet, h.getJob},
		actList:   {http.MethodGet, h.listJobs},
		actUpdate: {http.MethodPost, mutate},
		actReset:  {http.MethodPost, mutate},
		actDelete: {http.MethodPost, h.deleteJob},
		actRunNow: {http.MethodPost, h.runJobNow},
	})
}

func (h *DataPlaneHandler) createJob(w http.ResponseWriter, r *http.Request) {
	raw, name, ok := readJobSettings(w, r)
	if !ok {
		return
	}

	id, err := h.dp.CreateJob(r.Context(), dbxdriver.JobConfig{Name: name, SettingsJSON: raw})
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, createJobResponse{JobID: id})
}

func (h *DataPlaneHandler) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("job_id"), 10, 64)
	if err != nil {
		dpError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid job_id")

		return
	}

	job, err := h.dp.GetJob(r.Context(), id)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toJobJSON(job))
}

func (h *DataPlaneHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.dp.ListJobs(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]jobResponse, 0, len(jobs))
	for i := range jobs {
		out = append(out, toJobJSON(&jobs[i]))
	}

	dpJSON(w, listJobsResponse{Jobs: out})
}

func (h *DataPlaneHandler) mutateJob(w http.ResponseWriter, r *http.Request, action string) {
	var in jobUpdateRequest
	if !dpDecode(w, r, &in) {
		return
	}

	cfg := dbxdriver.JobConfig{Name: settingsName(in.NewSettings), SettingsJSON: in.NewSettings}

	var err error
	if action == actReset {
		err = h.dp.ResetJob(r.Context(), in.JobID, cfg)
	} else {
		err = h.dp.UpdateJob(r.Context(), in.JobID, cfg)
	}

	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) deleteJob(w http.ResponseWriter, r *http.Request) {
	var in jobIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.DeleteJob(r.Context(), in.JobID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) runJobNow(w http.ResponseWriter, r *http.Request) {
	var in jobIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	runID, err := h.dp.RunJobNow(r.Context(), in.JobID)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, runNowResponse{RunID: runID, NumberInJob: runID})
}

// --- permissions ---

func (h *DataPlaneHandler) servePermissions(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) < minPermissionsSegs {
		dpError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "permissions path needs object type and id")

		return
	}

	objectType, objectID := parts[3], parts[4]

	switch r.Method {
	case http.MethodGet:
		h.getPermissions(w, r, objectType, objectID)
	case http.MethodPut:
		h.writePermissions(w, r, objectType, objectID, false)
	case http.MethodPatch:
		h.writePermissions(w, r, objectType, objectID, true)
	default:
		dpMethodNotAllowed(w)
	}
}

func (h *DataPlaneHandler) getPermissions(w http.ResponseWriter, r *http.Request, objectType, objectID string) {
	perms, err := h.dp.GetPermissions(r.Context(), objectType, objectID)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toPermissionsJSON(perms))
}

func (h *DataPlaneHandler) writePermissions(w http.ResponseWriter, r *http.Request, objectType, objectID string, merge bool) {
	var in permissionsRequest
	if !dpDecode(w, r, &in) {
		return
	}

	acl := toDriverACL(in.AccessControlList)

	var (
		perms *dbxdriver.ObjectPermissions
		err   error
	)

	if merge {
		perms, err = h.dp.UpdatePermissions(r.Context(), objectType, objectID, acl)
	} else {
		perms, err = h.dp.SetPermissions(r.Context(), objectType, objectID, acl)
	}

	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toPermissionsJSON(perms))
}

// --- converters ---

func toPoolJSON(p *dbxdriver.InstancePool) instancePoolJSON {
	return instancePoolJSON{
		InstancePoolID: p.ID, InstancePoolName: p.Name, NodeTypeID: p.NodeTypeID,
		State: p.State, MinIdleInstances: p.MinIdleInstances, MaxCapacity: p.MaxCapacity,
	}
}

func clusterConfig(in *clusterJSON) dbxdriver.ClusterConfig {
	cfg := dbxdriver.ClusterConfig{
		Name: in.ClusterName, SparkVersion: in.SparkVersion,
		NodeTypeID: in.NodeTypeID, NumWorkers: in.NumWorkers,
	}
	if in.Autoscale != nil {
		cfg.AutoscaleMin = in.Autoscale.MinWorkers
		cfg.AutoscaleMax = in.Autoscale.MaxWorkers
	}

	return cfg
}

func toClusterJSON(c *dbxdriver.Cluster) clusterJSON {
	out := clusterJSON{
		ClusterID: c.ID, ClusterName: c.Name, SparkVersion: c.SparkVersion,
		NodeTypeID: c.NodeTypeID, State: c.State, NumWorkers: c.NumWorkers,
	}
	if c.AutoscaleMax > 0 {
		out.Autoscale = &autoscaleJSON{MinWorkers: c.AutoscaleMin, MaxWorkers: c.AutoscaleMax}
	}

	return out
}

func toJobJSON(j *dbxdriver.Job) jobResponse {
	return jobResponse{
		JobID: j.ID, CreatorUserName: j.CreatorUserName, CreatedTime: j.CreatedTime,
		Settings: json.RawMessage(j.SettingsJSON),
	}
}

func toPermissionsJSON(p *dbxdriver.ObjectPermissions) permissionsResponse {
	out := permissionsResponse{ObjectID: p.ObjectID, ObjectType: p.ObjectType}
	for _, a := range p.AccessControlList {
		out.AccessControlList = append(out.AccessControlList, accessControlResponse{
			UserName:             a.UserName,
			GroupName:            a.GroupName,
			ServicePrincipalName: a.ServicePrincipalName,
			AllPermissions:       []permissionLevelJSON{{PermissionLevel: a.PermissionLevel}},
		})
	}

	return out
}

func toDriverACL(in []accessControlRequest) []dbxdriver.AccessControl {
	out := make([]dbxdriver.AccessControl, 0, len(in))
	for _, a := range in {
		out = append(out, dbxdriver.AccessControl{
			UserName:             a.UserName,
			GroupName:            a.GroupName,
			ServicePrincipalName: a.ServicePrincipalName,
			PermissionLevel:      a.PermissionLevel,
		})
	}

	return out
}

// readJobSettings reads the raw jobs/create body and extracts the job name. The
// full body is stored as the job settings and echoed back on Get.
func readJobSettings(w http.ResponseWriter, r *http.Request) (raw []byte, name string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, dpMaxBodyBytes)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		dpError(w, http.StatusBadRequest, "MALFORMED_REQUEST", "read body: "+err.Error())

		return nil, "", false
	}

	return raw, settingsName(raw), true
}

// settingsName best-effort extracts the "name" field from a settings object.
func settingsName(raw []byte) string {
	var probe struct {
		Name string `json:"name"`
	}

	_ = json.Unmarshal(raw, &probe)

	return probe.Name
}
