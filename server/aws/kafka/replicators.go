package kafka

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// routeReplicators dispatches /replication/v1/replicators and its sub-paths.
func (h *Handler) routeReplicators(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createReplicator(w, r)
		case http.MethodGet:
			h.listReplicators(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	arn := rest[0]

	if len(rest) == 2 && rest[1] == "replication-info" {
		h.updateReplicationInfo(w, r, arn)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.describeReplicator(w, r, arn)
	case http.MethodDelete:
		h.deleteReplicator(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createReplicator(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	out, err := h.k.CreateReplicator(r.Context(), body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"replicatorArn":   out.ReplicatorARN,
		"replicatorName":  out.ReplicatorName,
		"replicatorState": out.State,
	})
}

func (h *Handler) describeReplicator(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DescribeReplicator(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, replicatorToWire(out))
}

func (h *Handler) listReplicators(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("replicatorNameFilter")

	list, next, err := h.k.ListReplicators(r.Context(), filter, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	reps := make([]map[string]any, 0, len(list))
	for i := range list {
		reps = append(reps, replicatorToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"replicators": reps}, next))
}

func (h *Handler) deleteReplicator(w http.ResponseWriter, r *http.Request, arn string) {
	arnOut, state, err := h.k.DeleteReplicator(r.Context(), arn, r.URL.Query().Get("currentVersion"))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"replicatorArn": arnOut, "replicatorState": state})
}

func (h *Handler) updateReplicationInfo(w http.ResponseWriter, r *http.Request, arn string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	out, err := h.k.UpdateReplicationInfo(r.Context(), arn, body)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"replicatorArn":   out.ReplicatorARN,
		"replicatorState": out.State,
	})
}

// replicatorToWire renders a replicator as the Describe/Summary wire shape,
// surfacing the version and cluster/replication blocks stored in raw options.
func replicatorToWire(rep *driver.Replicator) map[string]any {
	out := map[string]any{
		"replicatorArn":         rep.ReplicatorARN,
		"replicatorName":        rep.ReplicatorName,
		"replicatorState":       rep.State,
		"replicatorResourceArn": rep.ReplicatorARN,
		"creationTime":          timeRFC3339(rep.CreationTime),
	}

	if rep.Tags != nil {
		out["tags"] = rep.Tags
	}

	overlayReplicatorRaw(out, rep.RawOptions)

	return out
}

// overlayReplicatorRaw copies the modeled raw-option blocks into the response
// under their typed field names.
func overlayReplicatorRaw(out map[string]any, raw map[string]json.RawMessage) {
	overlayRaw(out, raw, "currentVersion", "currentVersion")
	overlayRaw(out, raw, "kafkaClusters", "kafkaClusters")
	overlayRaw(out, raw, "replicationInfoList", "replicationInfoList")
	overlayRaw(out, raw, "serviceExecutionRoleArn", "serviceExecutionRoleArn")
	overlayRaw(out, raw, "replicatorDescription", "replicatorDescription")
}
