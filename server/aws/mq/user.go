package mq

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// serveUserCollection handles GET /v1/brokers/{id}/users (ListUsers). There is
// no create on the collection path — CreateUser carries the username in the URI.
func (h *Handler) serveUserCollection(w http.ResponseWriter, r *http.Request, brokerID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.listUsers(w, r, brokerID)
}

// serveUserItem handles /v1/brokers/{id}/users/{username}: POST creates,
// GET describes, PUT updates, DELETE removes.
func (h *Handler) serveUserItem(w http.ResponseWriter, r *http.Request, brokerID, username string) {
	switch r.Method {
	case http.MethodPost:
		h.createUser(w, r, brokerID, username)
	case http.MethodGet:
		h.describeUser(w, r, brokerID, username)
	case http.MethodPut:
		h.updateUser(w, r, brokerID, username)
	case http.MethodDelete:
		h.deleteUser(w, r, brokerID, username)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request, brokerID, username string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.mq.CreateUser(r.Context(), brokerID, userFromBody(raw, username)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) describeUser(w http.ResponseWriter, r *http.Request, brokerID, username string) {
	u, err := h.mq.DescribeUser(r.Context(), brokerID, username)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, userToWire(brokerID, u))
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request, brokerID, username string) {
	raw, ok := decodeBodyMap(w, r)
	if !ok {
		return
	}

	if err := h.mq.UpdateUser(r.Context(), brokerID, userFromBody(raw, username)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request, brokerID, username string) {
	if err := h.mq.DeleteUser(r.Context(), brokerID, username); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request, brokerID string) {
	q := r.URL.Query()

	users, next, err := h.mq.ListUsers(r.Context(), brokerID, driver.Page{
		NextToken:  q.Get("nextToken"),
		MaxResults: atoiDefault(q.Get("maxResults")),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	summaries := make([]map[string]any, 0, len(users))
	for i := range users {
		summaries = append(summaries, map[string]any{"username": users[i].Username})
	}

	body := map[string]any{"brokerId": brokerID, "users": summaries}
	if next != "" {
		body["nextToken"] = next
	}

	writeJSON(w, body)
}

// userToWire renders a DescribeUser response. The password is never included.
func userToWire(brokerID string, u *driver.User) map[string]any {
	out := map[string]any{
		"brokerId":        brokerID,
		"username":        u.Username,
		"consoleAccess":   u.ConsoleAccess,
		"replicationUser": u.ReplicationUser,
	}
	if u.Groups != nil {
		out["groups"] = u.Groups
	}

	return out
}
