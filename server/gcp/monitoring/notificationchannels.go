package monitoring

import (
	"context"
	"encoding/json"
	"net/http"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// serveNotificationChannels routes /v3/projects/{p}/notificationChannels[/{id}].
func (h *Handler) serveNotificationChannels(w http.ResponseWriter, r *http.Request, project string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createChannel(w, r, project)
		case http.MethodGet:
			h.listChannels(w, r, project)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		}

		return
	}

	id := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getChannel(w, r, project, id)
	case http.MethodPatch:
		h.patchChannel(w, r, project, id)
	case http.MethodDelete:
		h.deleteChannel(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

// channelUpdater is the optional monitoring-driver extension that persists a
// notification-channel patch. Only the GCP mock implements it; a driver without
// it gets a 501 on PATCH rather than a shared-interface method it can't satisfy.
type channelUpdater interface {
	UpdateNotificationChannel(
		ctx context.Context, id string, apply func(*mondriver.NotificationChannelInfo),
	) (*mondriver.NotificationChannelInfo, error)
}

// patchChannel serves notificationChannels.patch: it merges the request body's
// present fields (displayName, type, labels) onto the stored channel, keeping
// its stable name, and returns the updated channel.
func (h *Handler) patchChannel(w http.ResponseWriter, r *http.Request, project, id string) {
	updater, ok := h.mon.(channelUpdater)
	if !ok {
		writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "notification channel patch unsupported")
		return
	}

	var body notificationChannel

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	info, err := updater.UpdateNotificationChannel(r.Context(), id, func(ch *mondriver.NotificationChannelInfo) {
		if body.DisplayName != "" {
			ch.Name = body.DisplayName
		}

		if body.Type != "" {
			ch.Type = body.Type
		}

		if body.Labels != nil {
			ch.Tags = body.Labels
			ch.Endpoint = channelEndpoint(body.Labels)
		}
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toChannelJSON(project, info))
}

func (h *Handler) createChannel(w http.ResponseWriter, r *http.Request, project string) {
	var body notificationChannel

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	cfg := mondriver.NotificationChannelConfig{
		Name:     body.DisplayName,
		Type:     body.Type,
		Endpoint: channelEndpoint(body.Labels),
		Tags:     body.Labels,
	}

	info, err := h.mon.CreateNotificationChannel(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toChannelJSON(project, info))
}

func (h *Handler) listChannels(w http.ResponseWriter, r *http.Request, project string) {
	infos, err := h.mon.ListNotificationChannels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := notificationChannelsList{NotificationChannels: make([]notificationChannel, 0, len(infos))}
	for i := range infos {
		out.NotificationChannels = append(out.NotificationChannels, toChannelJSON(project, &infos[i]))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getChannel(w http.ResponseWriter, r *http.Request, project, id string) {
	info, err := h.mon.GetNotificationChannel(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toChannelJSON(project, info))
}

func (h *Handler) deleteChannel(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.mon.DeleteNotificationChannel(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

// channelEndpoint picks a representative endpoint from GCP channel labels
// (email_address for email, url for webhooks, channel_name for chat).
func channelEndpoint(labels map[string]string) string {
	for _, k := range []string{"email_address", "url", "channel_name", "topic"} {
		if v, ok := labels[k]; ok {
			return v
		}
	}

	return ""
}

func toChannelJSON(project string, info *mondriver.NotificationChannelInfo) notificationChannel {
	enabled := true

	return notificationChannel{
		Name:               "projects/" + project + "/notificationChannels/" + info.ID,
		Type:               info.Type,
		DisplayName:        info.Name,
		Labels:             info.Tags,
		VerificationStatus: "VERIFICATION_STATUS_UNSPECIFIED",
		Enabled:            &enabled,
	}
}
