package kafka

import (
	"encoding/json"
	"net/http"
)

// scramSecretRequest is the Batch(Dis)AssociateScramSecret request body.
type scramSecretRequest struct {
	SecretArnList []string `json:"secretArnList"`
}

// routeScramSecrets dispatches /v1/clusters/{arn}/scram-secrets. POST associates,
// PATCH disassociates, GET lists.
func (h *Handler) routeScramSecrets(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodPost:
		h.batchAssociateScram(w, r, arn)
	case http.MethodPatch:
		h.batchDisassociateScram(w, r, arn)
	case http.MethodGet:
		h.listScramSecrets(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) batchAssociateScram(w http.ResponseWriter, r *http.Request, arn string) {
	var req scramSecretRequest
	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	unprocessed, err := h.k.BatchAssociateScramSecret(r.Context(), arn, req.SecretArnList)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, scramResponse(arn, unprocessed))
}

func (h *Handler) batchDisassociateScram(w http.ResponseWriter, r *http.Request, arn string) {
	var req scramSecretRequest
	if _, ok := decodeBody(w, r, &req); !ok {
		return
	}

	unprocessed, err := h.k.BatchDisassociateScramSecret(r.Context(), arn, req.SecretArnList)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, scramResponse(arn, unprocessed))
}

func (h *Handler) listScramSecrets(w http.ResponseWriter, r *http.Request, arn string) {
	list, next, err := h.k.ListScramSecrets(r.Context(), arn, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, withNext(map[string]any{"secretArnList": list}, next))
}

// scramResponse builds the Batch(Dis)AssociateScramSecret response, carrying
// the cluster ARN and any unprocessed secret entries.
func scramResponse(arn string, unprocessed []json.RawMessage) map[string]any {
	out := map[string]any{"clusterArn": arn}

	if len(unprocessed) > 0 {
		out["unprocessedScramSecrets"] = unprocessed
	}

	return out
}
