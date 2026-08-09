package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// entityPathLen is the segment count of entities/{type}/{ref}/{action}.
const entityPathLen = 4

// serveReputation routes /reputation/entities and its sub-paths.
func (h *Handler) serveReputation(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 || rest[0] != "entities" {
		notFound(w, r.URL.Path)

		return
	}

	sub := rest[1:]

	switch len(sub) {
	case 0:
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		h.listReputationEntities(w, r)
	case twoSegments:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		h.getReputationEntity(w, r, sub[0], sub[1])
	case entityPathLen - 1:
		h.updateReputationEntity(w, r, sub[0], sub[1], sub[2])
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) listReputationEntities(w http.ResponseWriter, r *http.Request) {
	entities, err := h.ses.ListReputationEntities(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]reputationEntityJSON, 0, len(entities))
	for i := range entities {
		out = append(out, reputationEntityToJSON(&entities[i]))
	}

	writeJSON(w, listReputationEntitiesResponse{ReputationEntities: out})
}

func (h *Handler) getReputationEntity(w http.ResponseWriter, r *http.Request, entityType, ref string) {
	e, err := h.ses.GetReputationEntity(r.Context(), entityType, ref)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getReputationEntityResponse{ReputationEntity: reputationEntityToJSON(e)})
}

func (h *Handler) updateReputationEntity(w http.ResponseWriter, r *http.Request, entityType, ref, action string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	switch action {
	case "customer-managed-status":
		var req updateRepStatusRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		writeOK(w, h.ses.UpdateReputationEntityCustomerManagedStatus(r.Context(), entityType, ref, req.SendingStatus))
	case "policy":
		var req updateRepPolicyRequest
		if !decodeJSON(w, r, &req) {
			return
		}

		writeOK(w, h.ses.UpdateReputationEntityPolicy(r.Context(), entityType, ref, req.ReputationEntityPolicy))
	default:
		notFound(w, r.URL.Path)
	}
}

func reputationEntityToJSON(e *driver.ReputationEntity) reputationEntityJSON {
	return reputationEntityJSON{
		ReputationEntityReference: e.Reference,
		ReputationEntityType:      e.EntityType,
		CustomerManagedStatus:     reputationStatusJSON{Status: e.CustomerManagedStatus},
		AwsSesManagedStatus:       reputationStatusJSON{Status: e.AWSManagedStatus},
	}
}
