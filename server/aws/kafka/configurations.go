package kafka

import (
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// routeConfigurations dispatches /v1/configurations and its sub-paths.
func (h *Handler) routeConfigurations(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createConfiguration(w, r)
		case http.MethodGet:
			h.listConfigurations(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	arn := rest[0]

	if len(rest) == 1 {
		h.serveConfigurationByArn(w, r, arn)

		return
	}

	if rest[1] == "revisions" {
		h.serveConfigurationRevisions(w, r, arn, rest[2:])

		return
	}

	notFoundPath(w, r.URL.Path)
}

// serveConfigurationByArn handles GET/PUT/DELETE on /v1/configurations/{arn}.
func (h *Handler) serveConfigurationByArn(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodGet:
		h.describeConfiguration(w, r, arn)
	case http.MethodPut:
		h.updateConfiguration(w, r, arn)
	case http.MethodDelete:
		h.deleteConfiguration(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

// serveConfigurationRevisions handles the .../revisions and .../revisions/{n} paths.
func (h *Handler) serveConfigurationRevisions(w http.ResponseWriter, r *http.Request, arn string, rest []string) {
	if len(rest) == 0 {
		h.listConfigurationRevisions(w, r, arn)

		return
	}

	rev, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, driver.ExBadRequest, "invalid revision: "+rest[0])

		return
	}

	h.describeConfigurationRevision(w, r, arn, rev)
}

func (h *Handler) createConfiguration(w http.ResponseWriter, r *http.Request) {
	var req createConfigurationRequest
	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	out, err := h.k.CreateConfiguration(r.Context(), driver.CreateConfigurationInput{
		Name:             req.Name,
		Description:      req.Description,
		KafkaVersions:    req.KafkaVersions,
		ServerProperties: req.ServerProperties,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"arn":            out.ARN,
		"name":           out.Name,
		"state":          out.State,
		"creationTime":   timeRFC3339(out.CreationTime),
		"latestRevision": revisionToWire(out.LatestRevision),
	})
}

func (h *Handler) describeConfiguration(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DescribeConfiguration(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, configToWire(out))
}

func (h *Handler) listConfigurations(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.k.ListConfigurations(r.Context(), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	configs := make([]map[string]any, 0, len(list))
	for i := range list {
		configs = append(configs, configToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"configurations": configs}, next))
}

func (h *Handler) updateConfiguration(w http.ResponseWriter, r *http.Request, arn string) {
	var req updateConfigurationRequest
	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	out, err := h.k.UpdateConfiguration(r.Context(), arn, req.Description, req.ServerProperties)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"arn":            out.ARN,
		"latestRevision": revisionToWire(out.LatestRevision),
	})
}

func (h *Handler) deleteConfiguration(w http.ResponseWriter, r *http.Request, arn string) {
	arnOut, state, err := h.k.DeleteConfiguration(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"arn": arnOut, "state": state})
}

func (h *Handler) listConfigurationRevisions(w http.ResponseWriter, r *http.Request, arn string) {
	list, next, err := h.k.ListConfigurationRevisions(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	revs := make([]map[string]any, 0, len(list))
	for i := range list {
		revs = append(revs, revisionToWire(list[i]))
	}

	writeJSON(w, withNext(map[string]any{"revisions": revs}, next))
}

func (h *Handler) describeConfigurationRevision(w http.ResponseWriter, r *http.Request, arn string, rev int64) {
	cfg, revision, err := h.k.DescribeConfigurationRevision(r.Context(), arn, rev)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"arn":              cfg.ARN,
		"description":      revision.Description,
		"revision":         revision.Revision,
		"creationTime":     timeRFC3339(revision.CreationTime),
		"serverProperties": revision.ServerProperties,
	})
}
