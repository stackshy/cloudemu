package databricks

import (
	"net/http"
	"strconv"

	dbxdriver "github.com/stackshy/cloudemu/databricks/driver"
)

// --- wire types: runs ---

type runStateJSON struct {
	LifeCycleState string `json:"life_cycle_state,omitempty"`
	ResultState    string `json:"result_state,omitempty"`
	StateMessage   string `json:"state_message,omitempty"`
}

type runJSON struct {
	RunID     int64         `json:"run_id"`
	JobID     int64         `json:"job_id,omitempty"`
	RunName   string        `json:"run_name,omitempty"`
	State     *runStateJSON `json:"state,omitempty"`
	StartTime int64         `json:"start_time,omitempty"`
	EndTime   int64         `json:"end_time,omitempty"`
}

type listRunsResponse struct {
	Runs    []runJSON `json:"runs"`
	HasMore bool      `json:"has_more"`
}

type runIDRequest struct {
	RunID int64 `json:"run_id"`
}

type notebookOutputJSON struct {
	Result string `json:"result,omitempty"`
}

type runOutputJSON struct {
	Metadata       runJSON             `json:"metadata"`
	NotebookOutput *notebookOutputJSON `json:"notebook_output,omitempty"`
}

// --- wire types: cluster policies ---

type policyJSON struct {
	PolicyID           string `json:"policy_id,omitempty"`
	Name               string `json:"name"`
	Definition         string `json:"definition,omitempty"`
	Description        string `json:"description,omitempty"`
	MaxClustersPerUser int64  `json:"max_clusters_per_user,omitempty"`
	CreatorUserName    string `json:"creator_user_name,omitempty"`
	CreatedAtTimestamp int64  `json:"created_at_timestamp,omitempty"`
}

type createPolicyResponse struct {
	PolicyID string `json:"policy_id"`
}

type policyIDRequest struct {
	PolicyID string `json:"policy_id"`
}

type listPoliciesResponse struct {
	Policies []policyJSON `json:"policies"`
}

// --- wire types: libraries ---

type pypiJSON struct {
	Package string `json:"package"`
}

type mavenJSON struct {
	Coordinates string `json:"coordinates"`
}

type cranJSON struct {
	Package string `json:"package"`
}

type libraryJSON struct {
	Jar   string     `json:"jar,omitempty"`
	Egg   string     `json:"egg,omitempty"`
	Whl   string     `json:"whl,omitempty"`
	Pypi  *pypiJSON  `json:"pypi,omitempty"`
	Maven *mavenJSON `json:"maven,omitempty"`
	Cran  *cranJSON  `json:"cran,omitempty"`
}

type librariesRequest struct {
	ClusterID string        `json:"cluster_id"`
	Libraries []libraryJSON `json:"libraries"`
}

type libraryFullStatusJSON struct {
	Library libraryJSON `json:"library"`
	Status  string      `json:"status"`
}

type clusterLibraryStatusesJSON struct {
	ClusterID       string                  `json:"cluster_id"`
	LibraryStatuses []libraryFullStatusJSON `json:"library_statuses"`
}

type allStatusesResponse struct {
	Statuses []clusterLibraryStatusesJSON `json:"statuses"`
}

// --- runs routing + ops ---

func (h *DataPlaneHandler) serveRuns(w http.ResponseWriter, r *http.Request, action string) {
	dispatch(w, r, action, map[string]dpRoute{
		actGet:       {http.MethodGet, h.getRun},
		actList:      {http.MethodGet, h.listRuns},
		actCancel:    {http.MethodPost, h.cancelRun},
		actGetOutput: {http.MethodGet, h.getRunOutput},
	})
}

func (h *DataPlaneHandler) getRun(w http.ResponseWriter, r *http.Request) {
	id, ok := int64Query(w, r, "run_id")
	if !ok {
		return
	}

	run, err := h.dp.GetRun(r.Context(), id)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toRunJSON(run))
}

func (h *DataPlaneHandler) listRuns(w http.ResponseWriter, r *http.Request) {
	var jobID int64
	if v := r.URL.Query().Get("job_id"); v != "" {
		jobID, _ = strconv.ParseInt(v, 10, 64)
	}

	runs, err := h.dp.ListRuns(r.Context(), jobID)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]runJSON, 0, len(runs))
	for i := range runs {
		out = append(out, toRunJSON(&runs[i]))
	}

	dpJSON(w, listRunsResponse{Runs: out})
}

func (h *DataPlaneHandler) cancelRun(w http.ResponseWriter, r *http.Request) {
	var in runIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.CancelRun(r.Context(), in.RunID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) getRunOutput(w http.ResponseWriter, r *http.Request) {
	id, ok := int64Query(w, r, "run_id")
	if !ok {
		return
	}

	out, err := h.dp.GetRunOutput(r.Context(), id)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, runOutputJSON{
		Metadata:       toRunJSON(&out.Run),
		NotebookOutput: &notebookOutputJSON{Result: out.NotebookResult},
	})
}

// --- cluster policies routing + ops ---

func (h *DataPlaneHandler) servePolicies(w http.ResponseWriter, r *http.Request, action string) {
	dispatch(w, r, action, map[string]dpRoute{
		actCreate: {http.MethodPost, h.createPolicy},
		actGet:    {http.MethodGet, h.getPolicy},
		actEdit:   {http.MethodPost, h.editPolicy},
		actDelete: {http.MethodPost, h.deletePolicy},
		actList:   {http.MethodGet, h.listPolicies},
	})
}

func (h *DataPlaneHandler) createPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyJSON
	if !dpDecode(w, r, &in) {
		return
	}

	policy, err := h.dp.CreateClusterPolicy(r.Context(), policyConfig(&in))
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, createPolicyResponse{PolicyID: policy.PolicyID})
}

func (h *DataPlaneHandler) getPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.dp.GetClusterPolicy(r.Context(), r.URL.Query().Get("policy_id"))
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toPolicyJSON(policy))
}

func (h *DataPlaneHandler) editPolicy(w http.ResponseWriter, r *http.Request) {
	var in policyJSON
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.EditClusterPolicy(r.Context(), in.PolicyID, policyConfig(&in)); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) deletePolicy(w http.ResponseWriter, r *http.Request) {
	var in policyIDRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.DeleteClusterPolicy(r.Context(), in.PolicyID); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) listPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.dp.ListClusterPolicies(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]policyJSON, 0, len(policies))
	for i := range policies {
		out = append(out, toPolicyJSON(&policies[i]))
	}

	dpJSON(w, listPoliciesResponse{Policies: out})
}

// --- libraries routing + ops ---

func (h *DataPlaneHandler) serveLibraries(w http.ResponseWriter, r *http.Request, action string) {
	dispatch(w, r, action, map[string]dpRoute{
		actInstall:       {http.MethodPost, h.installLibraries},
		actUninstall:     {http.MethodPost, h.uninstallLibraries},
		actClusterStatus: {http.MethodGet, h.clusterStatus},
		actAllStatuses:   {http.MethodGet, h.allClusterStatuses},
	})
}

func (h *DataPlaneHandler) installLibraries(w http.ResponseWriter, r *http.Request) {
	var in librariesRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.InstallLibraries(r.Context(), in.ClusterID, toDriverLibs(in.Libraries)); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) uninstallLibraries(w http.ResponseWriter, r *http.Request) {
	var in librariesRequest
	if !dpDecode(w, r, &in) {
		return
	}

	if err := h.dp.UninstallLibraries(r.Context(), in.ClusterID, toDriverLibs(in.Libraries)); err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, struct{}{})
}

func (h *DataPlaneHandler) clusterStatus(w http.ResponseWriter, r *http.Request) {
	clusterID := r.URL.Query().Get("cluster_id")

	statuses, err := h.dp.ClusterLibraryStatuses(r.Context(), clusterID)
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	dpJSON(w, toClusterStatusesJSON(clusterID, statuses))
}

func (h *DataPlaneHandler) allClusterStatuses(w http.ResponseWriter, r *http.Request) {
	all, err := h.dp.AllClusterLibraryStatuses(r.Context())
	if err != nil {
		dpWriteErr(w, err)

		return
	}

	out := make([]clusterLibraryStatusesJSON, 0, len(all))
	for i := range all {
		out = append(out, toClusterStatusesJSON(all[i].ClusterID, all[i].Statuses))
	}

	dpJSON(w, allStatusesResponse{Statuses: out})
}

// --- converters ---

func toRunJSON(run *dbxdriver.Run) runJSON {
	return runJSON{
		RunID:   run.RunID,
		JobID:   run.JobID,
		RunName: run.RunName,
		State: &runStateJSON{
			LifeCycleState: run.LifeCycleState,
			ResultState:    run.ResultState,
			StateMessage:   run.StateMessage,
		},
		StartTime: run.StartTime,
		EndTime:   run.EndTime,
	}
}

func policyConfig(in *policyJSON) dbxdriver.ClusterPolicyConfig {
	return dbxdriver.ClusterPolicyConfig{
		Name:               in.Name,
		Definition:         in.Definition,
		Description:        in.Description,
		MaxClustersPerUser: in.MaxClustersPerUser,
	}
}

func toPolicyJSON(p *dbxdriver.ClusterPolicy) policyJSON {
	return policyJSON{
		PolicyID:           p.PolicyID,
		Name:               p.Name,
		Definition:         p.Definition,
		Description:        p.Description,
		MaxClustersPerUser: p.MaxClustersPerUser,
		CreatorUserName:    p.CreatorUserName,
		CreatedAtTimestamp: p.CreatedAt,
	}
}

func toClusterStatusesJSON(clusterID string, statuses []dbxdriver.LibraryStatus) clusterLibraryStatusesJSON {
	out := clusterLibraryStatusesJSON{ClusterID: clusterID}
	for i := range statuses {
		out.LibraryStatuses = append(out.LibraryStatuses, libraryFullStatusJSON{
			Library: toLibraryJSON(&statuses[i].Library),
			Status:  statuses[i].Status,
		})
	}

	return out
}

func toDriverLibs(in []libraryJSON) []dbxdriver.LibrarySpec {
	out := make([]dbxdriver.LibrarySpec, 0, len(in))

	for _, l := range in {
		spec := dbxdriver.LibrarySpec{Jar: l.Jar, Egg: l.Egg, Whl: l.Whl}
		if l.Pypi != nil {
			spec.PypiPackage = l.Pypi.Package
		}

		if l.Maven != nil {
			spec.MavenCoordinates = l.Maven.Coordinates
		}

		if l.Cran != nil {
			spec.Cran = l.Cran.Package
		}

		out = append(out, spec)
	}

	return out
}

func toLibraryJSON(l *dbxdriver.LibrarySpec) libraryJSON {
	out := libraryJSON{Jar: l.Jar, Egg: l.Egg, Whl: l.Whl}
	if l.PypiPackage != "" {
		out.Pypi = &pypiJSON{Package: l.PypiPackage}
	}

	if l.MavenCoordinates != "" {
		out.Maven = &mavenJSON{Coordinates: l.MavenCoordinates}
	}

	if l.Cran != "" {
		out.Cran = &cranJSON{Package: l.Cran}
	}

	return out
}

// int64Query parses a required int64 query parameter.
func int64Query(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil {
		dpError(w, http.StatusBadRequest, "INVALID_PARAMETER_VALUE", "invalid "+name)

		return 0, false
	}

	return v, true
}
