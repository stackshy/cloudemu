// Package logic serves the Azure Logic Apps (Consumption) workflows ARM API
// (Microsoft.Logic/workflows). Real armlogic WorkflowsClient requests hit this
// handler the same way they hit management.azure.com.
//
// Every operation is synchronous (sync-200/201): CreateOrUpdate, Update, Delete,
// enable and disable complete in-line, so there is no long-running-operation
// plumbing to wire. Of the trigger sub-resources only
// POST .../triggers/{trigger}/listCallbackUrl is served; the other trigger,
// run and version URLs are not claimed by Matches (see
// docs/coverage/nongoals/logic.md).
package logic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	providerName           = "Microsoft.Logic"
	resourceType           = "workflows"
	armType                = providerName + "/" + resourceType
	actionEnable           = "enable"
	actionDisable          = "disable"
	subTriggers            = "triggers"
	actionListCallbackURL  = "listCallbackUrl"
	topParam               = "$top"
	skipTokenParam         = "$skiptoken"
	codeInvalidRequestBody = "InvalidRequestContent"
	codeInvalidQueryParam  = "InvalidQueryParameterValue"
)

// Store is the minimal Logic Apps backend the handler needs. *logic.Mock
// satisfies it.
type Store interface {
	CreateOrUpdate(ctx context.Context, sub, rg, name string, in *logic.Input) (logic.Workflow, bool, error)
	Update(ctx context.Context, sub, rg, name string, patch logic.Patch) (logic.Workflow, error)
	Get(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Enable(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Disable(ctx context.Context, sub, rg, name string) (logic.Workflow, error)
	Delete(ctx context.Context, sub, rg, name string) (bool, error)
	ListByResourceGroup(ctx context.Context, sub, rg string) ([]logic.Workflow, error)
	ListBySubscription(ctx context.Context, sub string) ([]logic.Workflow, error)
	ListCallbackURL(ctx context.Context, sub, rg, name, trigger string) (logic.CallbackURL, error)
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
// single workflow, its enable / disable action, or a trigger's listCallbackUrl
// action. The provider and type are matched case-insensitively because SDK URL
// templates and hand-written tooling differ in casing.
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
	case strings.ToLower(subTriggers):
		return isListCallbackURL(&rp)
	default:
		return false
	}
}

// isListCallbackURL reports whether rp is .../triggers/{trigger}/listCallbackUrl.
func isListCallbackURL(rp *azurearm.ResourcePath) bool {
	return strings.EqualFold(rp.SubResource, subTriggers) && rp.SubResourceName != "" &&
		strings.EqualFold(rp.SubResourceAction, actionListCallbackURL)
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

	if isListCallbackURL(&rp) {
		h.listCallbackURL(w, r, &rp)
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
		in.IntegrationServiceEnvironment = p.IntegrationServiceEnvironment
		in.Sku = p.Sku
	}

	wf, created, err := h.store.CreateOrUpdate(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, &in)
	if err != nil {
		writeErr(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	azurearm.WriteJSON(w, status, toResponse(&wf))
}

// update applies an ARM PATCH by decoding the body and handing it to the
// store, which merges it under its own lock. An empty body (armlogic's
// WorkflowsClient.Update sends none) is an empty patch: it moves only version
// and changedTime. A PATCH on a missing workflow is a 404.
func (h *Handler) update(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	req, ok := decodeOptionalJSON(w, r)
	if !ok {
		return
	}

	wf, err := h.store.Update(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName, toPatch(req))
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&wf))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	wf, err := h.store.Get(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toResponse(&wf))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existed, err := h.store.Delete(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		writeErr(w, err)
		return
	}

	// ARM DELETE is idempotent. A missing resource returns 204 No Content. A
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
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// listCallbackURL serves POST .../triggers/{trigger}/listCallbackUrl in the
// armlogic WorkflowTriggerCallbackURL shape.
func (h *Handler) listCallbackURL(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	cb, err := h.store.ListCallbackURL(r.Context(), rp.Subscription, rp.ResourceGroup, rp.ResourceName,
		rp.SubResourceName)
	if err != nil {
		writeErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toCallbackResponse(&cb))
}

// list serves the resource-group and subscription listings. $top bounds the
// page; when more workflows remain the response carries a nextLink that
// repeats the request with an opaque $skiptoken. Without $top every workflow is
// returned in one page. $filter is not honored (a documented non-goal).
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	top, ok := parseTop(w, r)
	if !ok {
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
		writeErr(w, err)
		return
	}

	if top == 0 {
		top = max(len(items), 1)
	}

	// The store returns a stable order (lowercased ARM id), which the offset
	// token requires.
	page, err := pagination.Paginate(items, r.URL.Query().Get(skipTokenParam), top)
	if err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, codeInvalidQueryParam,
			"The value of query parameter '"+skipTokenParam+"' is invalid.")
		return
	}

	out := listResponse{Value: make([]workflowResponse, 0, len(page.Items))}
	for i := range page.Items {
		out.Value = append(out.Value, toResponse(&page.Items[i]))
	}

	if page.HasMore {
		out.NextLink = nextPageLink(r, page.NextPageToken)
	}

	azurearm.WriteJSON(w, http.StatusOK, out)
}

// parseTop reads $top. Absent means "no limit" (0); anything but a positive
// integer is a 400.
func parseTop(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get(topParam)
	if raw == "" {
		return 0, true
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		azurearm.WriteError(w, http.StatusBadRequest, codeInvalidQueryParam,
			"The value '"+raw+"' of query parameter '"+topParam+"' is invalid.")
		return 0, false
	}

	return n, true
}

// nextPageLink builds the absolute URL that continues a listing at token,
// preserving the request path and query (api-version and $top included).
// armlogic pagers GET this URL verbatim, so it must carry scheme and host: a
// server request URL has neither.
func nextPageLink(r *http.Request, token string) string {
	next := *r.URL
	next.Host = r.Host

	next.Scheme = "http"
	if r.TLS != nil {
		next.Scheme = "https"
	}

	q := next.Query()
	q.Set(skipTokenParam, token)
	next.RawQuery = q.Encode()

	return next.String()
}

// writeErr maps a store error onto the ARM error envelope. A definition that is
// not a JSON object is Azure's InvalidRequestContent; everything else takes the
// shared cerrors mapping.
func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, logic.ErrInvalidDefinition) {
		azurearm.WriteError(w, http.StatusBadRequest, codeInvalidRequestBody, cerrors.Message(err))
		return
	}

	azurearm.WriteCErr(w, err)
}

// decodeOptionalJSON reads a request body that may be empty. It returns a nil
// request for an empty (or whitespace-only) body, and writes a 400 and returns
// false on malformed JSON.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request) (*workflowRequest, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, azurearm.MaxBodyBytes))
	if err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, codeInvalidRequestBody, err.Error())
		return nil, false
	}

	if len(bytes.TrimSpace(body)) == 0 {
		return nil, true
	}

	var req workflowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, codeInvalidRequestBody, err.Error())
		return nil, false
	}

	return &req, true
}
