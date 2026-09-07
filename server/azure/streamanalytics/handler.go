// Package streamanalytics serves the Azure Stream Analytics ARM API
// (Microsoft.StreamAnalytics/streamingjobs plus the nested transformations /
// inputs / outputs / functions child resources). Real armstreamanalytics
// StreamingJobsClient / InputsClient / OutputsClient / FunctionsClient /
// TransformationsClient requests hit this handler the same way they hit
// management.azure.com.
//
// Real Azure runs job CreateOrReplace, start and stop as long-running
// operations; the emulator completes them synchronously (sync-200/201) with
// provisioningState=Succeeded. The job start/stop/scale actions drive the
// jobState state machine (Created/Stopped/Failed -> Running -> Stopped), and an
// illegal transition is rejected with the 409 real ARM returns.
package streamanalytics

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/streamanalytics"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName = "Microsoft.StreamAnalytics"
	jobType      = "streamingjobs"
	jobArmType   = providerName + "/" + jobType

	actionStart = "start"
	actionStop  = "stop"
	actionScale = "scale"

	actionTest             = "test"
	actionRetrieveDefaults = "retrievedefaultdefinition"
)

// Store is the minimal Stream Analytics backend the handler needs.
// *streamanalytics.Mock satisfies it.
type Store interface {
	CreateOrUpdateJob(
		ctx context.Context, sub, rg, name, location string, in *streamanalytics.JobInput,
	) (streamanalytics.StreamingJob, bool, error)
	GetJob(ctx context.Context, sub, rg, name string) (streamanalytics.StreamingJob, error)
	DeleteJob(ctx context.Context, sub, rg, name string) (bool, error)
	ListJobsByResourceGroup(ctx context.Context, sub, rg string) ([]streamanalytics.StreamingJob, error)
	ListJobsBySubscription(ctx context.Context, sub string) ([]streamanalytics.StreamingJob, error)
	JobChildren(ctx context.Context, sub, rg, job string) ([]streamanalytics.Child, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error

	StartJob(ctx context.Context, sub, rg, name, outputStartMode, outputStartTime string) (streamanalytics.StreamingJob, error)
	StopJob(ctx context.Context, sub, rg, name string) (streamanalytics.StreamingJob, error)
	ScaleJob(ctx context.Context, sub, rg, name string, streamingUnits *int) (streamanalytics.StreamingJob, error)

	CreateOrUpdateChild(
		ctx context.Context, sub, rg, job, kind, name string, properties json.RawMessage,
	) (streamanalytics.Child, bool, error)
	GetChild(ctx context.Context, sub, rg, job, kind, name string) (streamanalytics.Child, error)
	DeleteChild(ctx context.Context, sub, rg, job, kind, name string) (bool, error)
	ListChildren(ctx context.Context, sub, rg, job, kind string) ([]streamanalytics.Child, error)
	TestChild(ctx context.Context, sub, rg, job, kind, name string) (string, error)
}

// Handler serves Microsoft.StreamAnalytics/streamingjobs (and nested child
// resources) ARM requests.
type Handler struct {
	store Store
}

// New returns a Stream Analytics handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a Stream Analytics ARM URL. The provider and
// type are matched case-insensitively.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return strings.EqualFold(rp.Provider, providerName) &&
		strings.EqualFold(rp.ResourceType, jobType)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if rp.ResourceName == "" {
		h.listJobs(w, r, &rp)
		return
	}

	if rp.SubResource == "" {
		h.serveJob(w, r, &rp)
		return
	}

	if isJobAction(rp.SubResource) {
		h.serveJobAction(w, r, &rp)
		return
	}

	if isChildKind(rp.SubResource) {
		h.serveChild(w, r, &rp)
		return
	}

	azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown sub-resource "+rp.SubResource)
}

// PurgeResourceGroup deletes every job and child under sub/rg so a
// resource-group delete cascades into them.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

// serveJob routes the top-level job CRUD surface.
func (h *Handler) serveJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createJob(w, r, rp)
	case http.MethodPatch:
		h.updateJob(w, r, rp)
	case http.MethodGet:
		h.getJob(w, r, rp)
	case http.MethodDelete:
		h.deleteJob(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// serveJobAction routes the POST job actions (start/stop/scale).
func (h *Handler) serveJobAction(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	switch strings.ToLower(rp.SubResource) {
	case actionStart:
		h.startJob(w, r, rp)
	case actionStop:
		h.stopJob(w, r, rp)
	case actionScale:
		h.scaleJob(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceType", "unknown action "+rp.SubResource)
	}
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req jobRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := jobInputFromRequest(&req)

	j, created, err := h.store.CreateOrUpdateJob(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.applyEmbeddedChildren(r.Context(), rp, req.Properties); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	h.writeJob(w, r, rp, &j, status)
}

// updateJob applies an ARM PATCH: only the supplied scalar fields are overlaid
// onto the stored job; the immutable location, computed fields and child
// resources are preserved (children cannot be modified via a job PATCH). A PATCH
// on a missing job is a 404.
func (h *Handler) updateJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.GetJob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var req jobRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := jobInputFromRequest(&req)

	j, _, err := h.store.CreateOrUpdateJob(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, existing.Location, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeJob(w, r, rp, &j, http.StatusOK)
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	j, err := h.store.GetJob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeJob(w, r, rp, &j, http.StatusOK)
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.DeleteJob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeDeleteStatus(w, existed)
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []streamanalytics.StreamingJob
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListJobsByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListJobsBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := jobListResponse{Value: make([]jobResponse, 0, len(items))}

	for i := range items {
		children, cerr := h.store.JobChildren(r.Context(), rp.Subscription, items[i].ResourceGroup, items[i].Name)
		if cerr != nil {
			azurearm.WriteCErr(w, cerr)
			return
		}

		out.Value = append(out.Value, toJobResponse(&items[i], children))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) startJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req startRequest
	if r.ContentLength != 0 && !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	j, err := h.store.StartJob(
		r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.OutputStartMode, req.OutputStartTime)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeJob(w, r, rp, &j, http.StatusOK)
}

func (h *Handler) stopJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	j, err := h.store.StopJob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeJob(w, r, rp, &j, http.StatusOK)
}

func (h *Handler) scaleJob(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var req scaleRequest
	if r.ContentLength != 0 && !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	j, err := h.store.ScaleJob(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, req.StreamingUnits)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.writeJob(w, r, rp, &j, http.StatusOK)
}

// writeJob loads the job's children and writes the embedded job response.
func (h *Handler) writeJob(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, j *streamanalytics.StreamingJob, status int,
) {
	children, err := h.store.JobChildren(r.Context(), rp.Subscription, rp.ResourceGroup, j.Name)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, status, toJobResponse(j, children))
}

// applyEmbeddedChildren upserts the transformation/inputs/outputs/functions a
// job PUT body carried, so a create-complete request materializes its children.
func (h *Handler) applyEmbeddedChildren(
	ctx context.Context, rp *azurearm.ResourcePath, props *jobPropertiesRequest,
) error {
	if props == nil {
		return nil
	}

	if props.Transformation != nil {
		if err := h.upsertChild(ctx, rp, streamanalytics.KindTransformations, props.Transformation); err != nil {
			return err
		}
	}

	for _, group := range []struct {
		kind  string
		items []childBody
	}{
		{streamanalytics.KindInputs, props.Inputs},
		{streamanalytics.KindOutputs, props.Outputs},
		{streamanalytics.KindFunctions, props.Functions},
	} {
		for i := range group.items {
			if err := h.upsertChild(ctx, rp, group.kind, &group.items[i]); err != nil {
				return err
			}
		}
	}

	return nil
}

// upsertChild persists one embedded child under the job named by rp.
func (h *Handler) upsertChild(
	ctx context.Context, rp *azurearm.ResourcePath, kind string, body *childBody,
) error {
	if body.Name == "" {
		return cerrors.Newf(cerrors.InvalidArgument, "embedded %s requires a name", kind)
	}

	_, _, err := h.store.CreateOrUpdateChild(
		ctx, rp.Subscription, rp.ResourceGroup, rp.ResourceName, kind, body.Name, body.Properties)

	return err
}

// isJobAction reports whether seg is a job-level POST action verb.
func isJobAction(seg string) bool {
	switch strings.ToLower(seg) {
	case actionStart, actionStop, actionScale:
		return true
	default:
		return false
	}
}

// isChildKind reports whether seg names one of the four child collections.
func isChildKind(seg string) bool {
	switch strings.ToLower(seg) {
	case streamanalytics.KindTransformations, streamanalytics.KindInputs,
		streamanalytics.KindOutputs, streamanalytics.KindFunctions:
		return true
	default:
		return false
	}
}

// writeDeleteStatus writes the idempotent ARM DELETE result: 200 when the
// resource existed, 204 when it did not.
func writeDeleteStatus(w http.ResponseWriter, existed bool) {
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeChildErr maps a child create error, translating a missing parent job
// (NotFound) into the ARM ParentResourceNotFound 404 real Azure returns.
func writeChildErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) {
		azurearm.WriteParentNotFound(w, err)
		return
	}

	azurearm.WriteCErr(w, err)
}
