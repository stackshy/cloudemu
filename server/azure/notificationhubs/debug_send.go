package notificationhubs

import (
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// debugSendResponse is the NotificationHubs.DebugSend result
// (armnotificationhubs.DebugSendResponse). properties.success / .failure are the
// device outcome counts; properties.results itemizes each targeted registration.
type debugSendResponse struct {
	Location   string            `json:"location,omitempty"`
	Properties *debugSendResult  `json:"properties,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type debugSendResult struct {
	Success float64          `json:"success"`
	Failure float64          `json:"failure"`
	Results []debugSendEntry `json:"results"`
}

type debugSendEntry struct {
	ApplicationPlatformID string `json:"applicationPlatformId,omitempty"`
	PnsHandle             string `json:"pnsHandle,omitempty"`
	RegistrationID        string `json:"registrationId,omitempty"`
	Outcome               string `json:"outcome"`
}

// debugSend serves NotificationHubs.DebugSend: it targets the hub's device
// registrations with a test notification and reports the per-device outcome. The
// emulator has no live PNS, so every current registration is a Success; a hub
// with no registrations reports zero success and zero failure.
func (h *Handler) debugSend(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hub string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	// The debug payload is advisory in the emulator; drain it so the connection
	// is reusable, but the outcome depends only on the registered devices.
	_, _ = io.Copy(io.Discard, r.Body)

	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	key := hubKey(rp.ResourceName, hub)
	if _, err := h.notif.GetTopic(r.Context(), key); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	regs, err := az.ListRegistrations(r.Context(), key)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	results := make([]debugSendEntry, 0, len(regs))
	for i := range regs {
		results = append(results, debugSendEntry{
			ApplicationPlatformID: regs[i].Platform,
			PnsHandle:             regs[i].Handle,
			RegistrationID:        regs[i].RegistrationID,
			Outcome:               "Success",
		})
	}

	// DebugSend answers 201 Created.
	azurearm.WriteJSON(w, http.StatusCreated, debugSendResponse{
		Location: defaultLocation,
		Properties: &debugSendResult{
			Success: float64(len(results)),
			Failure: 0,
			Results: results,
		},
	})
}
