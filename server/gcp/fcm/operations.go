package fcm

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

// sendMessageRequest is the FCM messages:send request body. Only the fields the
// notification driver can carry are modeled; the rest are ignored.
type sendMessageRequest struct {
	Message      *fcmMessage `json:"message"`
	ValidateOnly bool        `json:"validateOnly,omitempty"`
}

type fcmMessage struct {
	Topic        string            `json:"topic,omitempty"`
	Token        string            `json:"token,omitempty"`
	Condition    string            `json:"condition,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Notification *fcmNotification  `json:"notification,omitempty"`
}

type fcmNotification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// messageResponse is the FCM messages:send response: the resource name of the
// sent message. The SDK reads this into Message.Name.
type messageResponse struct {
	Name string `json:"name"`
}

// sendMessage maps messages:send to Notification.Publish. The message's topic
// string is the target topic; token/condition-addressed messages (no topic)
// publish to a synthetic topic so the round-trip still returns a message id.
// The target topic is auto-provisioned on first send, mirroring FCM where
// publishing to a topic with no subscribers still succeeds.
func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request, rt route) {
	var body sendMessageRequest
	if !gcprest.DecodeJSON(w, r, &body) {
		return
	}

	if body.Message == nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "message is required")
		return
	}

	if body.ValidateOnly {
		// Dry run: validate the request only — do NOT auto-create the topic,
		// publish, or emit metrics. Real FCM returns a fabricated message name.
		gcprest.WriteJSON(w, http.StatusOK, messageResponse{
			Name: "projects/" + rt.project + "/messages/fake_message_id",
		})
		return
	}

	topic := body.Message.Topic
	if topic == "" {
		// Token- or condition-addressed message: no named topic. Use a
		// synthetic per-project topic so Publish has a valid target.
		topic = tokenTopic
	}

	h.ensureTopic(r, topic)

	subject := ""
	msg := ""

	if body.Message.Notification != nil {
		subject = body.Message.Notification.Title
		msg = body.Message.Notification.Body
	}

	if msg == "" {
		// FCM requires a payload; data-only messages carry no notification body.
		// Fall back to a non-empty marker so the driver (which rejects empty
		// messages) accepts data-only sends.
		msg = messageBody(body.Message)
	}

	out, err := h.notif.Publish(r.Context(), notifdriver.PublishInput{
		TopicID:    topic,
		Subject:    subject,
		Message:    msg,
		Attributes: body.Message.Data,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, messageResponse{
		Name: "projects/" + rt.project + "/messages/" + out.MessageID,
	})
}

// ensureTopic creates the target topic if it does not already exist. Errors
// other than AlreadyExists are ignored here; a real failure surfaces on the
// subsequent Publish.
func (h *Handler) ensureTopic(r *http.Request, name string) {
	if _, err := h.notif.GetTopic(r.Context(), name); err == nil {
		return
	}

	if _, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{Name: name}); err != nil {
		if !cerrors.IsAlreadyExists(err) {
			return
		}
	}
}

// messageBody produces a non-empty message body for a data-only FCM message so
// the driver's non-empty-message validation passes.
func messageBody(m *fcmMessage) string {
	if len(m.Data) > 0 {
		return "data"
	}

	return "message"
}
