package servicedirectory

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"
)

const (
	// maxBodyBytes caps a decoded request body.
	maxBodyBytes = 8 << 20

	// fieldMetadata and fieldAnnotations are the two wire names for the
	// service/endpoint string map: `metadata` on v1beta1, `annotations` on v1.
	fieldMetadata    = "metadata"
	fieldAnnotations = "annotations"
)

// resource-name builders (server side) — must match the driver's stable names.
func nsResourceName(project, location, ns string) string {
	return "projects/" + project + "/locations/" + location + "/" + namespacesSeg + "/" + ns
}

func svcResourceName(project, location, ns, svc string) string {
	return nsResourceName(project, location, ns) + "/" + servicesSeg + "/" + svc
}

func epResourceName(project, location, ns, svc, ep string) string {
	return svcResourceName(project, location, ns, svc) + "/" + endpointsSeg + "/" + ep
}

// namespaceBody is the wire shape of a namespace create/patch request.
type namespaceBody struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

// serviceBody is the wire shape of a service create/patch request. The
// service's string map is named `annotations` on /v1 and `metadata` on
// /v1beta1; both keys are decoded and mapField prefers whichever the client
// sent.
type serviceBody struct {
	Name        string            `json:"name"`
	Annotations map[string]string `json:"annotations"`
	Metadata    map[string]string `json:"metadata"`
}

// mapField returns the string map the client sent, preferring the v1beta1
// `metadata` key over the v1 `annotations` key.
func (b *serviceBody) mapField() map[string]string {
	if b.Metadata != nil {
		return b.Metadata
	}

	return b.Annotations
}

// endpointBody is the wire shape of an endpoint create/patch request. As with a
// service, the string map arrives under `annotations` (v1) or `metadata`
// (v1beta1).
type endpointBody struct {
	Name        string            `json:"name"`
	Address     string            `json:"address"`
	Port        int               `json:"port"`
	Annotations map[string]string `json:"annotations"`
	Metadata    map[string]string `json:"metadata"`
	Network     string            `json:"network"`
}

// mapField returns the string map the client sent, preferring the v1beta1
// `metadata` key over the v1 `annotations` key.
func (b *endpointBody) mapField() map[string]string {
	if b.Metadata != nil {
		return b.Metadata
	}

	return b.Annotations
}

// mapKey returns the wire field name for the service/endpoint string map in the
// given API version: `metadata` for v1beta1, `annotations` for v1.
func mapKey(version string) string {
	if version == apiV1Beta1 {
		return fieldMetadata
	}

	return fieldAnnotations
}

// normalizeMask rewrites the v1beta1 `metadata` updateMask path to the internal
// `annotations` field name so a version-agnostic driver honors it. Other paths
// pass through unchanged.
func normalizeMask(mask []string) []string {
	for i, p := range mask {
		switch {
		case p == fieldMetadata:
			mask[i] = fieldAnnotations
		case strings.HasPrefix(p, fieldMetadata+"."):
			mask[i] = fieldAnnotations + strings.TrimPrefix(p, fieldMetadata)
		}
	}

	return mask
}

// decodeBody reads and unmarshals the request body into v, tolerating an empty
// body. It writes a 400 and returns false on a malformed body.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		return true
	}

	if err := json.Unmarshal(raw, v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
		return false
	}

	return true
}

func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, rt *route) {
	var body namespaceBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("namespaceId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	ns, err := h.db.CreateNamespace(r.Context(), &sddriver.NamespaceConfig{
		Project: rt.project, Location: rt.location, ID: id, Labels: body.Labels,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, namespaceJSON(ns))
}

func (h *Handler) getNamespace(w http.ResponseWriter, r *http.Request, rt *route) {
	ns, err := h.db.GetNamespace(r.Context(), rt.project, rt.location, rt.ns)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, namespaceJSON(ns))
}

func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListNamespaces(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, namespaceJSON(&all[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"namespaces": items})
}

func (h *Handler) patchNamespace(w http.ResponseWriter, r *http.Request, rt *route) {
	var body namespaceBody
	if !decodeBody(w, r, &body) {
		return
	}

	ns, err := h.db.PatchNamespace(r.Context(), &sddriver.NamespaceConfig{
		Project: rt.project, Location: rt.location, ID: rt.ns, Labels: body.Labels,
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, namespaceJSON(ns))
}

func (h *Handler) deleteNamespace(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteNamespace(r.Context(), rt.project, rt.location, rt.ns); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) createService(w http.ResponseWriter, r *http.Request, rt *route) {
	var body serviceBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("serviceId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	svc, err := h.db.CreateService(r.Context(), &sddriver.ServiceConfig{
		Project: rt.project, Location: rt.location, Namespace: rt.ns, ID: id, Annotations: body.mapField(),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, serviceJSON(svc, rt.version))
}

func (h *Handler) getService(w http.ResponseWriter, r *http.Request, rt *route) {
	svc, err := h.db.GetService(r.Context(), rt.project, rt.location, rt.ns, rt.svc)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, serviceJSON(svc, rt.version))
}

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListServices(r.Context(), rt.project, rt.location, rt.ns)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, serviceJSON(&all[i], rt.version))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (h *Handler) patchService(w http.ResponseWriter, r *http.Request, rt *route) {
	var body serviceBody
	if !decodeBody(w, r, &body) {
		return
	}

	svc, err := h.db.PatchService(r.Context(), &sddriver.ServiceConfig{
		Project: rt.project, Location: rt.location, Namespace: rt.ns, ID: rt.svc, Annotations: body.mapField(),
	}, normalizeMask(parseMask(r.URL.Query().Get("updateMask"))))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, serviceJSON(svc, rt.version))
}

func (h *Handler) deleteService(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteService(r.Context(), rt.project, rt.location, rt.ns, rt.svc); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request, rt *route) {
	var body endpointBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("endpointId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	ep, err := h.db.CreateEndpoint(r.Context(), &sddriver.EndpointConfig{
		Project: rt.project, Location: rt.location, Namespace: rt.ns, Service: rt.svc, ID: id,
		Address: body.Address, Port: body.Port, Annotations: body.mapField(), Network: body.Network,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, endpointJSON(ep, rt.version))
}

func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, rt *route) {
	ep, err := h.db.GetEndpoint(r.Context(), rt.project, rt.location, rt.ns, rt.svc, rt.ep)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, endpointJSON(ep, rt.version))
}

func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListEndpoints(r.Context(), rt.project, rt.location, rt.ns, rt.svc)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, endpointJSON(&all[i], rt.version))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"endpoints": items})
}

func (h *Handler) patchEndpoint(w http.ResponseWriter, r *http.Request, rt *route) {
	var body endpointBody
	if !decodeBody(w, r, &body) {
		return
	}

	ep, err := h.db.PatchEndpoint(r.Context(), &sddriver.EndpointConfig{
		Project: rt.project, Location: rt.location, Namespace: rt.ns, Service: rt.svc, ID: rt.ep,
		Address: body.Address, Port: body.Port, Annotations: body.mapField(), Network: body.Network,
	}, normalizeMask(parseMask(r.URL.Query().Get("updateMask"))))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, endpointJSON(ep, rt.version))
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteEndpoint(r.Context(), rt.project, rt.location, rt.ns, rt.svc, rt.ep); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// namespaceJSON renders a namespace as servicedirectory/v1 wire JSON. The empty
// labels map is omitted so a round-tripped resource compares cleanly.
func namespaceJSON(ns *sddriver.Namespace) map[string]any {
	m := map[string]any{
		"name": nsResourceName(ns.Project, ns.Location, ns.ID),
		"uid":  ns.UID,
	}
	if len(ns.Labels) > 0 {
		m["labels"] = ns.Labels
	}

	return m
}

// serviceJSON renders a service as Service Directory wire JSON for the given API
// version. The string map is emitted under the version's field name —
// `metadata` on v1beta1, `annotations` on v1 — so a v1beta1 Terraform refresh
// and a v1 SDK read each see the key they expect and neither drifts.
func serviceJSON(svc *sddriver.Service, version string) map[string]any {
	m := map[string]any{
		"name": svcResourceName(svc.Project, svc.Location, svc.Namespace, svc.ID),
		"uid":  svc.UID,
	}
	if len(svc.Annotations) > 0 {
		m[mapKey(version)] = svc.Annotations
	}

	return m
}

// endpointJSON renders an endpoint as Service Directory wire JSON for the given
// API version. Zero-value address/port/network fields are omitted, matching the
// real API, so an unset field never drifts against a Terraform config that also
// leaves it unset. The string map is emitted under the version's field name
// (`metadata` on v1beta1, `annotations` on v1).
func endpointJSON(ep *sddriver.Endpoint, version string) map[string]any {
	m := map[string]any{
		"name": epResourceName(ep.Project, ep.Location, ep.Namespace, ep.Service, ep.ID),
		"uid":  ep.UID,
	}

	if ep.Address != "" {
		m["address"] = ep.Address
	}

	if ep.Port != 0 {
		m["port"] = ep.Port
	}

	if ep.Network != "" {
		m["network"] = ep.Network
	}

	if len(ep.Annotations) > 0 {
		m[mapKey(version)] = ep.Annotations
	}

	return m
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}
