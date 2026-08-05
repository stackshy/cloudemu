package vpclattice

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

type wireTargetGroup struct {
	Arn                         string          `json:"arn,omitempty"`
	ID                          string          `json:"id,omitempty"`
	Name                        string          `json:"name,omitempty"`
	Type                        string          `json:"type,omitempty"`
	Status                      string          `json:"status,omitempty"`
	Config                      json.RawMessage `json:"config,omitempty"`
	ServiceArns                 []string        `json:"serviceArns,omitempty"`
	CreatedAt                   string          `json:"createdAt,omitempty"`
	LastUpdatedAt               string          `json:"lastUpdatedAt,omitempty"`
	Port                        int32           `json:"port,omitempty"`
	Protocol                    string          `json:"protocol,omitempty"`
	VpcIdentifier               string          `json:"vpcIdentifier,omitempty"`
	IPAddressType               string          `json:"ipAddressType,omitempty"`
	LambdaEventStructureVersion string          `json:"lambdaEventStructureVersion,omitempty"`
}

func targetGroupToWire(t *driver.TargetGroup) wireTargetGroup {
	w := wireTargetGroup{
		Arn: t.ARN, ID: t.ID, Name: t.Name, Type: t.Type, Status: t.Status,
		ServiceArns: t.ServiceARNs, CreatedAt: t.CreatedAt, LastUpdatedAt: t.LastUpdatedAt,
	}
	if len(t.Config) > 0 {
		w.Config = json.RawMessage(t.Config)
	}

	return w
}

func targetGroupToSummary(t *driver.TargetGroup) wireTargetGroup {
	return wireTargetGroup{
		Arn: t.ARN, ID: t.ID, Name: t.Name, Type: t.Type, Status: t.Status,
		Port: t.Port, Protocol: t.Protocol, VpcIdentifier: t.VpcID,
		IPAddressType: t.IPAddressType, LambdaEventStructureVersion: t.LambdaEventStructureVersion,
		ServiceArns: t.ServiceARNs, CreatedAt: t.CreatedAt, LastUpdatedAt: t.LastUpdatedAt,
	}
}

// serveTargetGroups routes /targetgroups[/{id}[/{action}]].
func (h *Handler) serveTargetGroups(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		routeCollection(w, r, h.createTargetGroup, h.listTargetGroups)

		return
	}

	id := rest[0]

	if len(rest) > 1 {
		h.serveTargetAction(w, r, id, rest[1])

		return
	}

	routeByID(w, r, id, h.getTargetGroup, h.updateTargetGroup, h.deleteTargetGroup)
}

// serveTargetAction routes the POST target subpaths of a target group.
func (h *Handler) serveTargetAction(w http.ResponseWriter, r *http.Request, id, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	switch action {
	case "registertargets":
		h.registerTargets(w, r, id)
	case "deregistertargets":
		h.deregisterTargets(w, r, id)
	case "listtargets":
		h.listTargets(w, r, id)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createTargetGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Config json.RawMessage   `json:"config"`
		Tags   map[string]string `json:"tags"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	tg, err := h.lattice.CreateTargetGroup(r.Context(), &driver.CreateTargetGroupInput{
		Name: req.Name, Type: req.Type, Config: req.Config, Tags: req.Tags,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, targetGroupToWire(tg))
}

func (h *Handler) getTargetGroup(w http.ResponseWriter, r *http.Request, id string) {
	tg, err := h.lattice.GetTargetGroup(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, targetGroupToWire(tg))
}

func (h *Handler) updateTargetGroup(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		HealthCheck json.RawMessage `json:"healthCheck"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	tg, err := h.lattice.UpdateTargetGroup(r.Context(), id, req.HealthCheck)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, targetGroupToWire(tg))
}

func (h *Handler) deleteTargetGroup(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.lattice.DeleteTargetGroup(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listTargetGroups(w http.ResponseWriter, r *http.Request) {
	tgs, err := h.lattice.ListTargetGroups(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	items := make([]wireTargetGroup, 0, len(tgs))
	for i := range tgs {
		items = append(items, targetGroupToSummary(&tgs[i]))
	}

	writeJSON(w, map[string]any{"items": items})
}

// --- targets ---

type wireTarget struct {
	ID         string `json:"id,omitempty"`
	Port       int32  `json:"port,omitempty"`
	Status     string `json:"status,omitempty"`
	ReasonCode string `json:"reasonCode,omitempty"`
}

func decodeTargets(w http.ResponseWriter, r *http.Request) ([]driver.RegisteredTarget, bool) {
	var req struct {
		Targets []wireTarget `json:"targets"`
	}

	if !decodeJSON(w, r, &req) {
		return nil, false
	}

	out := make([]driver.RegisteredTarget, 0, len(req.Targets))
	for _, t := range req.Targets {
		out = append(out, driver.RegisteredTarget{ID: t.ID, Port: t.Port})
	}

	return out, true
}

func targetsToWire(ts []driver.RegisteredTarget) []wireTarget {
	out := make([]wireTarget, 0, len(ts))
	for i := range ts {
		out = append(out, wireTarget{
			ID: ts[i].ID, Port: ts[i].Port, Status: ts[i].Status, ReasonCode: ts[i].ReasonCode,
		})
	}

	return out
}

func (h *Handler) registerTargets(w http.ResponseWriter, r *http.Request, id string) {
	in, ok := decodeTargets(w, r)
	if !ok {
		return
	}

	success, _, err := h.lattice.RegisterTargets(r.Context(), id, in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"successful": targetsToWire(success), "unsuccessful": []any{}})
}

func (h *Handler) deregisterTargets(w http.ResponseWriter, r *http.Request, id string) {
	in, ok := decodeTargets(w, r)
	if !ok {
		return
	}

	success, _, err := h.lattice.DeregisterTargets(r.Context(), id, in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"successful": targetsToWire(success), "unsuccessful": []any{}})
}

func (h *Handler) listTargets(w http.ResponseWriter, r *http.Request, id string) {
	ts, err := h.lattice.ListTargets(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"items": targetsToWire(ts)})
}
