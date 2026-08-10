package opensearch

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// serveDomain routes /2021-01-01/opensearch/domain and its sub-paths.
//
//nolint:gocyclo // per-sub-resource dispatch; large by API design.
func (h *Handler) serveDomain(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodPost {
			h.createDomain(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	name := rest[0]

	if len(rest) == 1 {
		h.serveDomainByName(w, r, name)

		return
	}

	switch rest[1] {
	case "config":
		h.serveDomainConfig(w, r, name, rest[2:])
	case "progress":
		h.describeDomainChangeProgress(w, r, name)
	case "health":
		h.describeDomainHealth(w, r, name)
	case "nodes":
		h.describeDomainNodes(w, r, name)
	case "autoTunes":
		h.describeDomainAutoTunes(w, r, name)
	case "dryRun":
		h.describeDryRunProgress(w, r, name)
	case "dataSource":
		h.serveDataSource(w, r, name, rest[2:])
	case "index":
		h.serveIndex(w, r, name, rest[2:])
	case "authorizeVpcEndpointAccess":
		h.authorizeVpcEndpointAccess(w, r, name)
	case "revokeVpcEndpointAccess":
		h.revokeVpcEndpointAccess(w, r, name)
	case "listVpcEndpointAccess":
		h.listVpcEndpointAccess(w, r, name)
	case "vpcEndpoints":
		h.listVpcEndpointsForDomain(w, r, name)
	case "domainMaintenance":
		h.serveDomainMaintenance(w, r, name)
	case "domainMaintenances":
		h.listDomainMaintenances(w, r, name)
	case "scheduledActions":
		h.listScheduledActions(w, r, name)
	case "scheduledAction":
		h.serveScheduledAction(w, r, name, rest[2:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveDomainByName handles GET (describe) and DELETE on /domain/{name}.
func (h *Handler) serveDomainByName(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		h.describeDomain(w, r, name)
	case http.MethodDelete:
		h.deleteDomain(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createDomain(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExValidation, "read body: "+err.Error())

		return
	}

	var req createDomainRequest
	if len(body) > 0 {
		if uerr := json.Unmarshal(body, &req); uerr != nil {
			writeError(w, http.StatusBadRequest, driver.ExValidation, "invalid JSON: "+uerr.Error())

			return
		}
	}

	out, err := h.os.CreateDomain(r.Context(), driver.CreateDomainInput{
		DomainName:      req.DomainName,
		EngineVersion:   req.EngineVersion,
		EngineMode:      req.EngineMode,
		IPAddressType:   req.IPAddressType,
		ClusterConfig:   req.ClusterConfig.toDriver(),
		AccessPolicies:  req.AccessPolicies,
		AdvancedOptions: req.AdvancedOptions,
		RawOptions:      decodeRawOptions(body),
		Tags:            tagsToMap(req.TagList),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"DomainStatus": marshalRaw(domainStatusToWire(out))})
}

func (h *Handler) describeDomain(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDomain(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"DomainStatus": marshalRaw(domainStatusToWire(out))})
}

func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DeleteDomain(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"DomainStatus": marshalRaw(domainStatusToWire(out))})
}

func (h *Handler) describeDomains(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DomainNames []string `json:"DomainNames"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	list, err := h.os.DescribeDomains(r.Context(), req.DomainNames)
	if err != nil {
		writeErr(w, err)

		return
	}

	statuses := make([]json.RawMessage, 0, len(list))
	for i := range list {
		statuses = append(statuses, marshalRaw(domainStatusToWire(&list[i])))
	}

	writeJSON(w, map[string]any{"DomainStatusList": statuses})
}

// serveDomainConfig handles GET (describe) and POST (update) on /domain/{name}/config
// and POST on /domain/{name}/config/cancel.
func (h *Handler) serveDomainConfig(w http.ResponseWriter, r *http.Request, name string, rest []string) {
	if len(rest) == 1 && rest[0] == "cancel" {
		h.cancelDomainConfigChange(w, r, name)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.describeDomainConfig(w, r, name)
	case http.MethodPost:
		h.updateDomainConfig(w, r, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) describeDomainConfig(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDomainConfig(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"DomainConfig": marshalRaw(domainConfigToWire(out))})
}

func (h *Handler) updateDomainConfig(w http.ResponseWriter, r *http.Request, name string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExValidation, "read body: "+err.Error())

		return
	}

	var req updateDomainConfigRequest
	if len(body) > 0 {
		if uerr := json.Unmarshal(body, &req); uerr != nil {
			writeError(w, http.StatusBadRequest, driver.ExValidation, "invalid JSON: "+uerr.Error())

			return
		}
	}

	in := driver.UpdateDomainConfigInput{
		DomainName:      name,
		AccessPolicies:  req.AccessPolicies,
		IPAddressType:   req.IPAddressType,
		AdvancedOptions: req.AdvancedOptions,
		RawOptions:      decodeRawOptions(body),
		DryRun:          req.DryRun,
	}

	if req.ClusterConfig != nil {
		cc := req.ClusterConfig.toDriver()
		in.ClusterConfig = &cc
	}

	out, _, err := h.os.UpdateDomainConfig(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"DomainConfig": marshalRaw(domainConfigToWire(out))})
}

func (h *Handler) cancelDomainConfigChange(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		DryRun bool `json:"DryRun"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	changes, err := h.os.CancelDomainConfigChange(r.Context(), name, req.DryRun)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DryRun": req.DryRun, "CancelledChangeIds": []string{}, "CancelledChangeProperties": changes})
}

func (h *Handler) describeDomainChangeProgress(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDomainChangeProgress(r.Context(), name, r.URL.Query().Get("changeid"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"ChangeProgressStatus": out})
}

func (h *Handler) describeDomainHealth(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDomainHealth(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

func (h *Handler) describeDomainNodes(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDomainNodes(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"DomainNodesStatusList": out})
}

func (h *Handler) describeDomainAutoTunes(w http.ResponseWriter, r *http.Request, name string) {
	out, next, err := h.os.DescribeDomainAutoTunes(r.Context(), name, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"AutoTunes": out}, next))
}

func (h *Handler) describeDryRunProgress(w http.ResponseWriter, r *http.Request, name string) {
	out, err := h.os.DescribeDryRunProgress(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, out)
}

// serveDomainRoot handles the top-level /2021-01-01/domain paths:
// GET /domain (ListDomainNames) and GET /domain/{name}/packages.
func (h *Handler) serveDomainRoot(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		if r.Method == http.MethodGet {
			h.listDomainNames(w, r)

			return
		}

		methodNotAllowed(w)

		return
	}

	if len(rest) == 2 && rest[1] == "packages" && r.Method == http.MethodGet {
		h.listPackagesForDomain(w, r, rest[0])

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) listDomainNames(w http.ResponseWriter, r *http.Request) {
	list, err := h.os.ListDomainNames(r.Context(), r.URL.Query().Get("engineType"))
	if err != nil {
		writeErr(w, err)

		return
	}

	names := make([]map[string]string, 0, len(list))
	for _, d := range list {
		names = append(names, map[string]string{"DomainName": d.DomainName, "EngineType": d.EngineType})
	}

	writeJSON(w, map[string]any{"DomainNames": names})
}
