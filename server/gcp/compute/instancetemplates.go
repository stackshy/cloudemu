package compute

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	gcecompute "github.com/stackshy/cloudemu/v2/providers/gcp/compute"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

const resourceTemplates = "instanceTemplates"

// templateBackend is the GCP-local capability storing global instance
// templates, reached by type assertion like migBackend.
type templateBackend interface {
	CreateInstanceTemplateGCP(t gcecompute.InstanceTemplate) error
	GetInstanceTemplateGCP(project, name string) (gcecompute.InstanceTemplate, bool)
	ListInstanceTemplatesGCP(project string) []gcecompute.InstanceTemplate
	DeleteInstanceTemplateGCP(project, name string) error
}

// templateResponse holds the computed compute#instanceTemplate fields; the rest
// of the stored insert body (properties, description) is echoed under them.
type templateResponse struct {
	Kind              string `json:"kind"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	CreationTimestamp string `json:"creationTimestamp,omitempty"`
	SelfLink          string `json:"selfLink"`

	spec json.RawMessage
}

// MarshalJSON writes the computed fields over the stored insert body.
//
//nolint:gocritic // value receiver so both values and pointers marshal
func (t templateResponse) MarshalJSON() ([]byte, error) {
	type plain templateResponse

	return mergeSpecJSON(t.spec, plain(t))
}

type templateListResponse struct {
	Kind     string             `json:"kind"`
	ID       string             `json:"id"`
	Items    []templateResponse `json:"items"`
	SelfLink string             `json:"selfLink"`
}

// serveInstanceTemplatesRoute dispatches global instanceTemplates
// insert/get/list/delete. Regional templates are not modeled.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstanceTemplatesRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	backend, ok := h.compute.(templateBackend)
	if !ok || rp.Scope != gcprest.ScopeGlobal {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	route := r.Method + " item"
	if rp.ResourceName == "" {
		route = r.Method + " collection"
	} else if rp.Action != "" {
		route = r.Method + " action"
	}

	switch route {
	case http.MethodPost + " collection":
		h.insertTemplate(w, r, rp, backend)
	case http.MethodGet + " collection":
		listTemplates(w, r, rp, backend)
	case http.MethodGet + " item":
		getTemplate(w, r, rp, backend)
	case http.MethodDelete + " item":
		h.deleteTemplate(w, r, rp, backend)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func getTemplate(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend templateBackend) {
	t, found := findTemplate(backend, rp.Project, rp.ResourceName)
	if !found {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'projects/"+rp.Project+"/global/instanceTemplates/"+rp.ResourceName+"' was not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toTemplateResponse(&t, rp.Project, hostFromRequest(r)))
}

//nolint:gocritic // rp is a request-scoped value
func listTemplates(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend templateBackend) {
	host := hostFromRequest(r)
	items := backend.ListInstanceTemplatesGCP(rp.Project)
	out := make([]templateResponse, 0, len(items))

	for i := range items {
		if gcprest.NameMatches(r.URL.Query().Get("filter"), items[i].Name) {
			out = append(out, toTemplateResponse(&items[i], rp.Project, host))
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, templateListResponse{
		Kind:     "compute#instanceTemplateList",
		ID:       "projects/" + rp.Project + "/global/instanceTemplates",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", resourceTemplates, ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertTemplate(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend templateBackend) {
	var spec json.RawMessage
	if !gcprest.DecodeJSON(w, r, &spec) {
		return
	}

	var req struct {
		Name       string          `json:"name"`
		Properties json.RawMessage `json:"properties"`
	}

	if err := json.Unmarshal(spec, &req); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	if req.Name == "" || len(req.Properties) == 0 {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance template name and properties are required")
		return
	}

	if err := backend.CreateInstanceTemplateGCP(gcecompute.InstanceTemplate{Project: rp.Project, Name: req.Name, Spec: spec}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// targetId carries the template's numeric id: Terraform keys its read on it
	// (self_link_unique), and a get by id resolves like a get by name.
	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "", resourceTemplates, req.Name, "insert")
	op.TargetID = templateID(req.Name)
	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend templateBackend) {
	if err := backend.DeleteInstanceTemplateGCP(rp.Project, rp.ResourceName); err != nil {
		if cerrors.IsFailedPrecondition(err) {
			gcprest.WriteError(w, http.StatusBadRequest, "resourceInUseByAnotherResource", cerrors.Message(err))
			return
		}

		gcprest.WriteCErr(w, err)

		return
	}

	h.dropPolicy(rp)

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, gcprest.ScopeGlobal, "", resourceTemplates, rp.ResourceName, "delete")
	gcprest.WriteJSON(w, http.StatusOK, op)
}

func toTemplateResponse(t *gcecompute.InstanceTemplate, project, host string) templateResponse {
	return templateResponse{
		Kind:              "compute#instanceTemplate",
		ID:                templateID(t.Name),
		Name:              t.Name,
		CreationTimestamp: t.CreatedAt,
		SelfLink:          gcprest.SelfLink(host, project, gcprest.ScopeGlobal, "", resourceTemplates, t.Name),
		spec:              t.Spec,
	}
}

func templateID(name string) string { return numericID("instanceTemplates/" + name) }

// findTemplate resolves a template by name or by numeric id, as GCP accepts
// either in the path.
func findTemplate(backend templateBackend, project, nameOrID string) (gcecompute.InstanceTemplate, bool) {
	if t, ok := backend.GetInstanceTemplateGCP(project, nameOrID); ok {
		return t, true
	}

	for _, t := range backend.ListInstanceTemplatesGCP(project) {
		if templateID(t.Name) == nameOrID {
			return t, true
		}
	}

	return gcecompute.InstanceTemplate{}, false
}
