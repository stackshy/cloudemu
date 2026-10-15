package bedrockagentruntime

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/bedrockagentruntime/driver"
)

type sessionRequest struct {
	SessionMetadata  map[string]string `json:"sessionMetadata"`
	EncryptionKeyArn string            `json:"encryptionKeyArn"`
	Tags             map[string]string `json:"tags"`
}

type sessionJSON struct {
	SessionID        string            `json:"sessionId"`
	SessionArn       string            `json:"sessionArn"`
	SessionStatus    string            `json:"sessionStatus"`
	CreatedAt        string            `json:"createdAt,omitempty"`
	LastUpdatedAt    string            `json:"lastUpdatedAt,omitempty"`
	SessionMetadata  map[string]string `json:"sessionMetadata,omitempty"`
	EncryptionKeyArn string            `json:"encryptionKeyArn,omitempty"`
}

func toSessionJSON(s *driver.Session) sessionJSON {
	return sessionJSON{
		SessionID: s.ID, SessionArn: s.ARN, SessionStatus: s.Status, CreatedAt: s.CreatedAt,
		LastUpdatedAt: s.LastUpdatedAt, SessionMetadata: s.Metadata, EncryptionKeyArn: s.EncryptionKeyARN,
	}
}

// sessionOp runs fn with the Sessions capability.
func sessionOp(h *Handler, w http.ResponseWriter, op string, fn func(driver.Sessions)) {
	withCapability(h, w, op, fn)
}

func serveCreateSession(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	sessionOp(h, w, "CreateSession", func(c driver.Sessions) {
		var in sessionRequest
		if !decodeJSONAllowEmpty(w, r, &in) {
			return
		}

		s, err := c.CreateSession(r.Context(), driver.SessionConfig{
			Metadata: in.SessionMetadata, EncryptionKeyARN: in.EncryptionKeyArn, Tags: in.Tags,
		})
		if err != nil {
			writeErr(w, err)

			return
		}

		out := toSessionJSON(s)
		out.LastUpdatedAt, out.SessionMetadata, out.EncryptionKeyArn = "", nil, ""

		writeJSONStatus(w, http.StatusCreated, out)
	})
}

func serveGetSession(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	sessionOp(h, w, "GetSession", func(c driver.Sessions) {
		s, err := c.GetSession(r.Context(), segs[1])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, toSessionJSON(s))
	})
}

func serveUpdateSession(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	sessionOp(h, w, "UpdateSession", func(c driver.Sessions) {
		var in sessionRequest
		if !decodeJSONAllowEmpty(w, r, &in) {
			return
		}

		s, err := c.UpdateSession(r.Context(), segs[1], in.SessionMetadata)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := toSessionJSON(s)
		out.SessionMetadata, out.EncryptionKeyArn = nil, ""

		writeJSON(w, out)
	})
}

func serveEndSession(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	sessionOp(h, w, "EndSession", func(c driver.Sessions) {
		s, err := c.EndSession(r.Context(), segs[1])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, sessionJSON{SessionID: s.ID, SessionArn: s.ARN, SessionStatus: s.Status})
	})
}

func serveDeleteSession(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	sessionOp(h, w, "DeleteSession", func(c driver.Sessions) {
		if err := c.DeleteSession(r.Context(), segs[1]); err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, struct{}{})
	})
}

func serveListSessions(h *Handler, w http.ResponseWriter, r *http.Request, _ []string) {
	sessionOp(h, w, "ListSessions", func(c driver.Sessions) {
		page, ok := queryPage(w, r, "maxResults")
		if !ok {
			return
		}

		sessions, next, err := c.ListSessions(r.Context(), page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]sessionJSON, 0, len(sessions))

		for i := range sessions {
			s := toSessionJSON(&sessions[i])
			s.SessionMetadata, s.EncryptionKeyArn = nil, ""
			out = append(out, s)
		}

		writeJSON(w, map[string]any{"sessionSummaries": out, "nextToken": omitEmpty(next)})
	})
}

// --- invocations ---

type invocationRequest struct {
	InvocationID string `json:"invocationId"`
	Description  string `json:"description"`
}

func serveCreateInvocation(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "CreateInvocation", func(c driver.Invocations) {
		var in invocationRequest
		if !decodeJSONAllowEmpty(w, r, &in) {
			return
		}

		inv, err := c.CreateInvocation(r.Context(), segs[1], in.InvocationID, in.Description)
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusCreated, map[string]string{
			"invocationId": inv.ID, "sessionId": inv.SessionID, "createdAt": inv.CreatedAt,
		})
	})
}

func serveListInvocations(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "ListInvocations", func(c driver.Invocations) {
		page, ok := queryPage(w, r, "maxResults")
		if !ok {
			return
		}

		invs, next, err := c.ListInvocations(r.Context(), segs[1], page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]string, 0, len(invs))
		for _, inv := range invs {
			out = append(out, map[string]string{"invocationId": inv.ID, "sessionId": inv.SessionID, "createdAt": inv.CreatedAt})
		}

		writeJSON(w, map[string]any{"invocationSummaries": out, "nextToken": omitEmpty(next)})
	})
}

type putStepRequest struct {
	InvocationIdentifier string          `json:"invocationIdentifier"`
	InvocationStepID     string          `json:"invocationStepId"`
	InvocationStepTime   string          `json:"invocationStepTime"`
	Payload              json.RawMessage `json:"payload"`
}

func servePutStep(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "PutInvocationStep", func(c driver.Invocations) {
		var in putStepRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		id, err := c.PutInvocationStep(r.Context(), driver.InvocationStepInput{
			SessionID: segs[1], InvocationID: in.InvocationIdentifier, StepID: in.InvocationStepID,
			StepTime: in.InvocationStepTime, Payload: in.Payload,
		})
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, http.StatusCreated, map[string]string{"invocationStepId": id})
	})
}

// stepRequest is the body of ListInvocationSteps and GetInvocationStep.
type stepRequest struct {
	InvocationIdentifier string `json:"invocationIdentifier"`
}

func serveListSteps(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "ListInvocationSteps", func(c driver.Invocations) {
		page, ok := queryPage(w, r, "maxResults")
		if !ok {
			return
		}

		var in stepRequest
		if !decodeJSONAllowEmpty(w, r, &in) {
			return
		}

		steps, next, err := c.ListInvocationSteps(r.Context(), segs[1], in.InvocationIdentifier, page)
		if err != nil {
			writeErr(w, err)

			return
		}

		out := make([]map[string]string, 0, len(steps))
		for _, s := range steps {
			out = append(out, map[string]string{
				"invocationStepId": s.ID, "invocationId": s.InvocationID, "sessionId": s.SessionID, "invocationStepTime": s.StepTime,
			})
		}

		writeJSON(w, map[string]any{"invocationStepSummaries": out, "nextToken": omitEmpty(next)})
	})
}

func serveGetStep(h *Handler, w http.ResponseWriter, r *http.Request, segs []string) {
	withCapability(h, w, "GetInvocationStep", func(c driver.Invocations) {
		var in stepRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		s, err := c.GetInvocationStep(r.Context(), segs[1], in.InvocationIdentifier, segs[3])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, map[string]any{"invocationStep": map[string]any{
			"invocationStepId": s.ID, "invocationId": s.InvocationID, "sessionId": s.SessionID,
			"invocationStepTime": s.StepTime, "payload": s.Payload,
		}})
	})
}

// omitEmpty returns nil for "", so an absent nextToken is left out of JSON
// maps instead of being sent empty (the model requires min length 1).
func omitEmpty(s string) any {
	if s == "" {
		return nil
	}

	return s
}
