package cloudrun

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// serviceResource is the google.cloud.run.v2.Service wire shape (the subset
// CloudEmu models) returned by Get and inlined in create/patch Operation
// responses.
type serviceResource struct {
	Name                  string                `json:"name"`
	Description           string                `json:"description,omitempty"`
	UID                   string                `json:"uid,omitempty"`
	Generation            string                `json:"generation,omitempty"`
	Labels                map[string]string     `json:"labels,omitempty"`
	Annotations           map[string]string     `json:"annotations,omitempty"`
	CreateTime            string                `json:"createTime,omitempty"`
	UpdateTime            string                `json:"updateTime,omitempty"`
	Ingress               string                `json:"ingress,omitempty"`
	LaunchStage           string                `json:"launchStage,omitempty"`
	Template              revisionTemplate      `json:"template"`
	Traffic               []trafficTarget       `json:"traffic,omitempty"`
	ObservedGeneration    string                `json:"observedGeneration,omitempty"`
	TerminalCondition     *condition            `json:"terminalCondition,omitempty"`
	Conditions            []condition           `json:"conditions,omitempty"`
	LatestReadyRevision   string                `json:"latestReadyRevision,omitempty"`
	LatestCreatedRevision string                `json:"latestCreatedRevision,omitempty"`
	TrafficStatuses       []trafficTargetStatus `json:"trafficStatuses,omitempty"`
	URI                   string                `json:"uri,omitempty"`
	Reconciling           bool                  `json:"reconciling,omitempty"`
	Etag                  string                `json:"etag,omitempty"`
}

// revisionTemplate is Service.template — the spec each new revision is cut from.
type revisionTemplate struct {
	Revision             string            `json:"revision,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	Annotations          map[string]string `json:"annotations,omitempty"`
	Scaling              *revisionScaling  `json:"scaling,omitempty"`
	VPCAccess            *vpcAccess        `json:"vpcAccess,omitempty"`
	Timeout              string            `json:"timeout,omitempty"`
	ServiceAccount       string            `json:"serviceAccount,omitempty"`
	Containers           []container       `json:"containers,omitempty"`
	ExecutionEnvironment string            `json:"executionEnvironment,omitempty"`
}

type revisionScaling struct {
	MinInstanceCount int `json:"minInstanceCount,omitempty"`
	MaxInstanceCount int `json:"maxInstanceCount,omitempty"`
}

type trafficTarget struct {
	Type     string `json:"type,omitempty"`
	Revision string `json:"revision,omitempty"`
	Percent  int    `json:"percent,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

type trafficTargetStatus struct {
	Type     string `json:"type,omitempty"`
	Revision string `json:"revision,omitempty"`
	Percent  int    `json:"percent,omitempty"`
	Tag      string `json:"tag,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// revisionResource is the google.cloud.run.v2.Revision wire shape returned by
// revisions.get / revisions.list.
type revisionResource struct {
	Name                 string           `json:"name"`
	UID                  string           `json:"uid,omitempty"`
	Generation           string           `json:"generation,omitempty"`
	Service              string           `json:"service,omitempty"`
	CreateTime           string           `json:"createTime,omitempty"`
	UpdateTime           string           `json:"updateTime,omitempty"`
	LaunchStage          string           `json:"launchStage,omitempty"`
	Scaling              *revisionScaling `json:"scaling,omitempty"`
	VPCAccess            *vpcAccess       `json:"vpcAccess,omitempty"`
	Timeout              string           `json:"timeout,omitempty"`
	ServiceAccount       string           `json:"serviceAccount,omitempty"`
	Containers           []container      `json:"containers,omitempty"`
	ExecutionEnvironment string           `json:"executionEnvironment,omitempty"`
	Conditions           []condition      `json:"conditions,omitempty"`
	Etag                 string           `json:"etag,omitempty"`
}

type listServicesResponse struct {
	Services      []serviceResource `json:"services"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

type listRevisionsResponse struct {
	Revisions     []revisionResource `json:"revisions"`
	NextPageToken string             `json:"nextPageToken,omitempty"`
}

// serveServices routes the services surface.
func (h *Handler) serveServices(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch {
	case p.action != "":
		h.serveIam(w, r, p, p.serviceName(p.name), h.serviceExists)
	case p.sub == revisionsSeg && p.subName != "":
		h.serveRevisionItem(w, r, p)
	case p.sub == revisionsSeg:
		h.listRevisions(w, r, p)
	case p.name != "":
		h.serveServiceItem(w, r, p)
	default:
		h.serveServiceCollection(w, r, p)
	}
}

func (h *Handler) serveServiceCollection(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodPost:
		h.createService(w, r, p)
	case http.MethodGet:
		h.listServices(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveServiceItem(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodGet:
		h.getService(w, r, p)
	case http.MethodPatch:
		h.updateService(w, r, p)
	case http.MethodDelete:
		h.deleteService(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) serveRevisionItem(w http.ResponseWriter, r *http.Request, p *crPath) {
	switch r.Method {
	case http.MethodGet:
		h.getRevision(w, r, p)
	case http.MethodDelete:
		h.deleteRevision(w, r, p)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request, p *crPath) {
	var body serviceResource
	if !decodeJSON(w, r, &body) {
		return
	}

	name := r.URL.Query().Get("serviceId")
	if name == "" {
		name = lastSegment(body.Name)
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "serviceId is required")
		return
	}

	svc, err := h.cr.CreateService(r.Context(), serviceConfigFromWire(name, p.location, &body))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "create-"+name),
		Done:     true,
		Response: asResponse(toServiceResource(svc, p), serviceTypeURL),
	})
}

func (h *Handler) updateService(w http.ResponseWriter, r *http.Request, p *crPath) {
	var body serviceResource
	if !decodeJSON(w, r, &body) {
		return
	}

	cfg := serviceConfigFromWire(p.name, p.location, &body)
	cfg.UpdateMask = maskPaths(r.URL.Query().Get("updateMask"))

	svc, err := h.cr.UpdateService(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{
		Name:     opName(p, "update-"+p.name),
		Done:     true,
		Response: asResponse(toServiceResource(svc, p), serviceTypeURL),
	})
}

func (h *Handler) getService(w http.ResponseWriter, r *http.Request, p *crPath) {
	svc, err := h.cr.GetService(r.Context(), p.name)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toServiceResource(svc, p))
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request, p *crPath) {
	svcs, err := h.cr.ListServices(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	items, next := pageConvert(r, svcs, func(s *driver.Service) serviceResource {
		return toServiceResource(s, p)
	})

	writeJSON(w, http.StatusOK, listServicesResponse{Services: items, NextPageToken: next})
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request, p *crPath) {
	if err := h.cr.DeleteService(r.Context(), p.name); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{Name: opName(p, "delete-"+p.name), Done: true})
}

func (h *Handler) listRevisions(w http.ResponseWriter, r *http.Request, p *crPath) {
	listPaged(w, r,
		func() ([]driver.Revision, error) { return h.cr.ListRevisions(r.Context(), p.name) },
		func(rev *driver.Revision) revisionResource { return toRevisionResource(rev, p) },
		func(items []revisionResource, next string) listRevisionsResponse {
			return listRevisionsResponse{Revisions: items, NextPageToken: next}
		})
}

func (h *Handler) getRevision(w http.ResponseWriter, r *http.Request, p *crPath) {
	rev, err := h.cr.GetRevision(r.Context(), p.subName)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toRevisionResource(rev, p))
}

func (h *Handler) deleteRevision(w http.ResponseWriter, r *http.Request, p *crPath) {
	if err := h.cr.DeleteRevision(r.Context(), p.subName); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, operation{Name: opName(p, "delete-rev-"+p.subName), Done: true})
}

// serviceConfigFromWire maps a wire Service body onto a driver.ServiceConfig.
func serviceConfigFromWire(name, location string, body *serviceResource) driver.ServiceConfig {
	t := body.Template

	return driver.ServiceConfig{
		Name:                 name,
		Location:             location,
		Description:          body.Description,
		Ingress:              body.Ingress,
		LaunchStage:          body.LaunchStage,
		Containers:           toDriverContainers(t.Containers),
		ServiceAccount:       t.ServiceAccount,
		Timeout:              t.Timeout,
		ExecutionEnvironment: t.ExecutionEnvironment,
		VPCAccess:            toDriverVPC(t.VPCAccess),
		Scaling:              toDriverScaling(t.Scaling),
		Traffic:              toDriverTraffic(body.Traffic),
		Labels:               body.Labels,
		Annotations:          body.Annotations,
		TemplateLabels:       t.Labels,
		TemplateAnnotations:  t.Annotations,
	}
}

// maskPaths splits a FieldMask query value (comma-separated field paths) into a
// slice, returning nil for an absent or empty mask (a maskless full replace).
func maskPaths(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return nil
	}

	var out []string

	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}

	return out
}

func toDriverScaling(in *revisionScaling) *driver.ServiceScaling {
	if in == nil {
		return nil
	}

	return &driver.ServiceScaling{MinInstanceCount: in.MinInstanceCount, MaxInstanceCount: in.MaxInstanceCount}
}

func toWireScaling(in *driver.ServiceScaling) *revisionScaling {
	if in == nil {
		return nil
	}

	return &revisionScaling{MinInstanceCount: in.MinInstanceCount, MaxInstanceCount: in.MaxInstanceCount}
}

func toDriverTraffic(in []trafficTarget) []driver.TrafficTarget {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.TrafficTarget, 0, len(in))
	for _, t := range in {
		out = append(out, driver.TrafficTarget{Type: t.Type, Revision: t.Revision, Percent: t.Percent, Tag: t.Tag})
	}

	return out
}

func toWireTraffic(in []driver.TrafficTarget) []trafficTarget {
	if len(in) == 0 {
		return nil
	}

	out := make([]trafficTarget, 0, len(in))
	for _, t := range in {
		out = append(out, trafficTarget{Type: t.Type, Revision: t.Revision, Percent: t.Percent, Tag: t.Tag})
	}

	return out
}

func toWireTrafficStatuses(in []driver.TrafficTarget) []trafficTargetStatus {
	if len(in) == 0 {
		return nil
	}

	out := make([]trafficTargetStatus, 0, len(in))
	for _, t := range in {
		out = append(out, trafficTargetStatus{
			Type: t.Type, Revision: t.Revision, Percent: t.Percent, Tag: t.Tag, URI: t.URI,
		})
	}

	return out
}

func toServiceResource(s *driver.Service, p *crPath) serviceResource {
	return serviceResource{
		Name:                  p.serviceName(s.Name),
		Description:           s.Description,
		UID:                   s.UID,
		Generation:            strconv.FormatInt(s.Generation, 10),
		Labels:                s.Labels,
		Annotations:           s.Annotations,
		CreateTime:            formatTime(s.CreateTime),
		UpdateTime:            formatTime(s.UpdateTime),
		Ingress:               s.Ingress,
		LaunchStage:           s.LaunchStage,
		Traffic:               toWireTraffic(s.Traffic),
		ObservedGeneration:    strconv.FormatInt(s.ObservedGeneration, 10),
		TerminalCondition:     toWireCondition(s.TerminalCondition),
		Conditions:            toConditions(s.Conditions),
		LatestReadyRevision:   revName(p, s.Name, s.LatestReadyRevision),
		LatestCreatedRevision: revName(p, s.Name, s.LatestCreatedRevision),
		TrafficStatuses:       toWireTrafficStatuses(s.TrafficStatuses),
		URI:                   s.URI,
		Reconciling:           s.Reconciling,
		Etag:                  s.Etag,
		Template: revisionTemplate{
			Labels:               s.TemplateLabels,
			Annotations:          s.TemplateAnnotations,
			Scaling:              toWireScaling(s.Scaling),
			VPCAccess:            toWireVPC(s.VPCAccess),
			Timeout:              s.Timeout,
			ServiceAccount:       s.ServiceAccount,
			Containers:           toContainers(s.Containers),
			ExecutionEnvironment: s.ExecutionEnvironment,
		},
	}
}

func toRevisionResource(r *driver.Revision, p *crPath) revisionResource {
	return revisionResource{
		Name:                 revName(p, r.Service, r.Name),
		UID:                  r.UID,
		Generation:           strconv.FormatInt(r.Generation, 10),
		Service:              p.serviceName(r.Service),
		CreateTime:           formatTime(r.CreateTime),
		UpdateTime:           formatTime(r.UpdateTime),
		LaunchStage:          r.LaunchStage,
		Scaling:              toWireScaling(r.Scaling),
		VPCAccess:            toWireVPC(r.VPCAccess),
		Timeout:              r.Timeout,
		ServiceAccount:       r.ServiceAccount,
		Containers:           toContainers(r.Containers),
		ExecutionEnvironment: r.ExecutionEnvironment,
		Conditions:           toConditions(r.Conditions),
		Etag:                 r.Etag,
	}
}

// revName composes the fully qualified revision resource name, tolerating an
// empty revision id (returns empty so the field is omitted).
func revName(p *crPath, service, revision string) string {
	if revision == "" {
		return ""
	}

	return p.serviceName(service) + "/revisions/" + revision
}

func (h *Handler) serviceExists(r *http.Request, name string) error {
	_, err := h.cr.GetService(r.Context(), name)

	return err
}

func (h *Handler) jobExists(r *http.Request, name string) error {
	_, err := h.cr.GetJob(r.Context(), name)

	return err
}
