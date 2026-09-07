package notifications

import (
	"net/http"

	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// publishMessage is the ONS data plane: a message posted to the topic's own
// endpoint, which CloudEmu serves on the same listener as the control plane.
func (h *Handler) publishMessage(w http.ResponseWriter, r *http.Request, topicID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req messageDetails

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	msg, err := h.extras.PublishMessage(r.Context(), topicID, notifprovider.MessageSpec{
		Title: req.Title,
		Body:  req.Body,
		Type:  r.URL.Query().Get("messageType"),
	})
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, publishResult{MessageID: msg.ID, TimeStamp: msg.Timestamp})
}
