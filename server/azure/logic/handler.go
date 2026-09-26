// Package logic serves the Azure Logic Apps (Consumption) workflows ARM API
// (Microsoft.Logic/workflows). Real armlogic WorkflowsClient requests hit this
// handler the same way they hit management.azure.com.
//
// Every operation is synchronous (sync-200/201): CreateOrUpdate, Update, Delete,
// enable and disable complete in-line, so there is no long-running-operation
// plumbing to wire. Workflow runs, triggers, versions and callback URLs are data
// plane and not served; those sub-resource URLs are not claimed by Matches.
package logic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName  = "Microsoft.Logic"
	resourceType  = "workflows"
	armType       = providerName + "/" + resourceType
	actionEnable  = "enable"
	actionDisable = "disable"
)

// Store is the minimal Logic Apps backend the handler needs. *logic.Mock
// satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string, in *logic.Input) (logic.Workflow, bool, error)
	Get(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Enable(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Disable(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]logic.Workflow, error)
	ListBySubscription(ctx context.Context, sub string) ([]logic.Workflow, error)
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
}

// Handler serves Microsoft.Logic/workflows ARM requests.
type Handler struct {
	store Store
}

// New returns a Logic Apps workflows handler backed by store.
func New(store Store) *Handler {
	return &Handler{store: store}
}

// Matches reports whether r targets a workflows ARM URL: the collection, a
// single workflow, or its enable / disable action. The provider and type are
// matched case-insensitively because SDK URL templates and hand-written tooling
// differ in casing.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	if !strings.EqualFold(rp.Provider, providerName) || !strings.EqualFold(rp.ResourceType, resourceType) {
		return false
	}

	switch strings.ToLower(rp.SubResource) {
	case "":
		return true
	case actionEnable, actionDisable:
		return rp.SubResourceName == ""
	default:
		return false
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	// A collection URL (no resource name) is a list: by resource group when the
	// path carried one, otherwise by subscription.
	if rp.ResourceName == "" {
		h.list(w, r, &rp)
		return
	}

	if rp.SubResource != "" {
		h.action(w, r, &rp)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdate(w, r, &rp)
	case http.MethodPatch:
		h.update(w, r, &rp)
	case http.MethodGet:
		h.get(w, r, &rp)
	case http.MethodDelete:
		h.delete(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// PurgeResourceGroup deletes every workflow under sub/rg so a resource-group
// delete cascades into its workflows (resourcegroups.ResourceGroupPurger).
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	return h.store.PurgeResourceGroup(ctx, subscription, resourceGroup)
}

func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req workflowRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	in := logic.Input{
		Location: req.Location,
		Tags:     req.Tags,
		Identity: toDriverIdentity(req.Identity),
	}

	if p := req.Properties; p != nil {
		in.State = p.State
		in.Definition = p.Definition
		in.Parameters = p.Parameters
		in.AccessControl = p.AccessControl
		in.IntegrationAccount = p.IntegrationAccount
	}

	wf, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&wf))
}

// update applies an ARM PATCH: only the supplied fields are overlaid onto the
// stored workflow; the computed fields and unmentioned properties are
// preserved. An empty body (armlogic's WorkflowsClient.Update sends none) is a
// no-op that echoes the stored workflow. A PATCH on a missing workflow is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	req, ok := decodeOptionalJSON(w, r)
	if !ok {
		return
	}

	if req == nil {
		azurearm.WriteJSON(w, http.StatusOK, toResponse(&existing))
		return
	}

	wf, _, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		mergePatch(&existing, req))
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&wf))
}

// mergePatch overlays the fields present in req onto the stored workflow,
// producing the Input for a create-or-update that preserves everything the PATCH
// did not mention. Location is immutable and always taken from the stored value.
func mergePatch(existing *logic.Workflow, req *workflowRequest) *logic.Input {
	in := &logic.Input{
		Location:           existing.Location,
		Tags:               existing.Tags,
		Identity:           identityInputFrom(existing.Identity),
		State:              existing.State,
		Definition:         existing.Definition,
		Parameters:         existing.Parameters,
		AccessControl:      existing.AccessControl,
		IntegrationAccount: existing.IntegrationAccount,
	}

	if req.Tags != nil {
		in.Tags = req.Tags
	}

	if req.Identity != nil {
		in.Identity = toDriverIdentity(req.Identity)
	}

	if p := req.Properties; p != nil {
		in.State = firstNonEmpty(p.State, in.State)
		in.Definition = overlay(p.Definition, in.Definition)
		in.Parameters = overlay(p.Parameters, in.Parameters)
		in.AccessControl = overlay(p.AccessControl, in.AccessControl)
		in.IntegrationAccount = overlay(p.IntegrationAccount, in.IntegrationAccount)
	}

	return in
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	wf, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&wf))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	// ARM DELETE is idempotent: a missing resource returns 204 No Content, a
	// deleted one returns 200 OK. The armlogic client accepts both.
	if existed {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// action serves POST .../workflows/{name}/enable and .../disable, which switch
// properties.state and answer 200 with an empty body.
func (h *Handler) action(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	toggle := h.store.Enable
	if strings.EqualFold(rp.SubResource, actionDisable) {
		toggle = h.store.Disable
	}

	if _, err := toggle(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var (
		items []logic.Workflow
		err   error
	)

	if rp.ResourceGroup != "" {
		items, err = h.store.ListByResourceGroup(r.Context(), rp.Subscription, rp.ResourceGroup)
	} else {
		items, err = h.store.ListBySubscription(r.Context(), rp.Subscription)
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := listResponse{Value: make([]workflowResponse, 0, len(items))}
	for i := range items {
		out.Value = append(out.Value, toResponse(&items[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// decodeOptionalJSON reads a request body that may be empty. It returns a nil
// request for an empty (or whitespace-only) body, and writes a 400 and returns
// false on malformed JSON.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request) (*workflowRequest, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, azurearm.MaxBodyBytes))
	if err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return nil, false
	}

	if len(bytes.TrimSpace(body)) == 0 {
		return nil, true
	}

	var req workflowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return nil, false
	}

	return &req, true
}

// overlay returns patch when it was supplied, otherwise the stored value.
func overlay(patch, stored json.RawMessage) json.RawMessage {
	if len(patch) == 0 {
		return stored
	}

	return patch
}

// firstNonEmpty returns a when it is set, otherwise b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
