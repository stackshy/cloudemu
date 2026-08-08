package monitoring

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

func (h *Handler) createAlarm(w http.ResponseWriter, r *http.Request) {
	var body alarmDetails

	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	created, err := h.mon.CreateOCIAlarm(r.Context(), body.toSpec())
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toWireAlarm(created))
}

func (h *Handler) getAlarm(w http.ResponseWriter, r *http.Request, id string) {
	found, err := h.mon.GetOCIAlarm(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toWireAlarm(found))
}

// listAlarms returns the alarms in a compartment. The full alarm is returned
// rather than OCI's narrower AlarmSummary, which is a subset of it.
func (h *Handler) listAlarms(w http.ResponseWriter, r *http.Request) {
	alarms, ok := h.compartmentAlarms(w, r)
	if !ok {
		return
	}

	displayName := r.URL.Query().Get("displayName")
	limit := ocirest.Limit(r)
	out := make([]alarm, 0, len(alarms))

	for _, a := range alarms {
		if len(out) == limit {
			break
		}

		if displayName != "" && a.Spec.DisplayName != displayName {
			continue
		}

		out = append(out, toWireAlarm(a))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) listAlarmsStatus(w http.ResponseWriter, r *http.Request) {
	alarms, ok := h.compartmentAlarms(w, r)
	if !ok {
		return
	}

	limit := ocirest.Limit(r)
	out := make([]alarmStatusSummary, 0, len(alarms))

	for _, a := range alarms {
		if len(out) == limit {
			break
		}

		out = append(out, alarmStatusSummary{
			ID:                 a.ID,
			DisplayName:        a.Spec.DisplayName,
			Severity:           a.Spec.Severity,
			Status:             a.Status,
			TimestampTriggered: timestamp(a.TimeTriggered),
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) updateAlarm(w http.ResponseWriter, r *http.Request, id string) {
	var body alarmDetails

	if !ocirest.DecodeJSON(w, r, &body) {
		return
	}

	current, err := h.mon.GetOCIAlarm(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	spec := current.Spec
	applyPatch(&spec, &body)

	updated, err := h.mon.UpdateOCIAlarm(r.Context(), id, spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toWireAlarm(updated))
}

func (h *Handler) deleteAlarm(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.mon.DeleteOCIAlarm(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

func (h *Handler) getAlarmHistory(w http.ResponseWriter, r *http.Request, id string) {
	found, err := h.mon.GetOCIAlarm(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	entries, err := h.mon.OCIAlarmHistory(r.Context(), id, ocirest.Limit(r))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	out := alarmHistoryCollection{
		AlarmID:   id,
		IsEnabled: found.Spec.IsEnabled,
		Entries:   make([]alarmHistoryEntry, 0, len(entries)),
	}

	for _, e := range entries {
		out.Entries = append(out.Entries, alarmHistoryEntry{
			Timestamp:          timestamp(e.Timestamp),
			TimestampTriggered: timestamp(e.Timestamp),
			Summary:            e.NewState + ": " + e.Reason,
		})
	}

	ocirest.WriteJSON(w, r, http.StatusOK, out)
}

// compartmentAlarms resolves the required compartmentId and lists it, writing
// the error response itself when either step fails.
func (h *Handler) compartmentAlarms(w http.ResponseWriter, r *http.Request) ([]*mondriver.OCIAlarm, bool) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return nil, false
	}

	alarms, err := h.mon.ListOCIAlarms(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return nil, false
	}

	return alarms, true
}

func (d *alarmDetails) toSpec() mondriver.OCIAlarmSpec {
	return mondriver.OCIAlarmSpec{
		DisplayName:                d.DisplayName,
		CompartmentID:              d.CompartmentID,
		MetricCompartmentID:        d.MetricCompartmentID,
		Namespace:                  d.Namespace,
		ResourceGroup:              d.ResourceGroup,
		Query:                      d.Query,
		Resolution:                 d.Resolution,
		PendingDuration:            d.PendingDuration,
		Severity:                   d.Severity,
		Body:                       d.Body,
		MessageFormat:              d.MessageFormat,
		RepeatNotificationDuration: d.RepeatNotificationDuration,
		Destinations:               d.Destinations,
		FreeformTags:               d.FreeformTags,
		DefinedTags:                d.DefinedTags,
		IsEnabled:                  d.IsEnabled == nil || *d.IsEnabled,
	}
}

// applyPatch overlays the fields an UpdateAlarm body supplied onto the stored
// spec, leaving the rest as they were.
func applyPatch(spec *mondriver.OCIAlarmSpec, body *alarmDetails) {
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&spec.DisplayName, body.DisplayName},
		{&spec.MetricCompartmentID, body.MetricCompartmentID},
		{&spec.Namespace, body.Namespace},
		{&spec.ResourceGroup, body.ResourceGroup},
		{&spec.Query, body.Query},
		{&spec.Resolution, body.Resolution},
		{&spec.PendingDuration, body.PendingDuration},
		{&spec.Severity, body.Severity},
		{&spec.Body, body.Body},
		{&spec.MessageFormat, body.MessageFormat},
		{&spec.RepeatNotificationDuration, body.RepeatNotificationDuration},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}

	if body.Destinations != nil {
		spec.Destinations = body.Destinations
	}

	if body.FreeformTags != nil {
		spec.FreeformTags = body.FreeformTags
	}

	if body.DefinedTags != nil {
		spec.DefinedTags = body.DefinedTags
	}

	if body.IsEnabled != nil {
		spec.IsEnabled = *body.IsEnabled
	}
}

func toWireAlarm(a *mondriver.OCIAlarm) alarm {
	return alarm{
		ID:                         a.ID,
		DisplayName:                a.Spec.DisplayName,
		CompartmentID:              a.Spec.CompartmentID,
		MetricCompartmentID:        a.Spec.MetricCompartmentID,
		Namespace:                  a.Spec.Namespace,
		ResourceGroup:              a.Spec.ResourceGroup,
		Query:                      a.Spec.Query,
		Resolution:                 a.Spec.Resolution,
		PendingDuration:            a.Spec.PendingDuration,
		Severity:                   a.Spec.Severity,
		Body:                       a.Spec.Body,
		MessageFormat:              a.Spec.MessageFormat,
		RepeatNotificationDuration: a.Spec.RepeatNotificationDuration,
		Destinations:               a.Spec.Destinations,
		FreeformTags:               a.Spec.FreeformTags,
		DefinedTags:                a.Spec.DefinedTags,
		IsEnabled:                  a.Spec.IsEnabled,
		LifecycleState:             a.LifecycleState,
		TimeCreated:                timestamp(a.TimeCreated),
		TimeUpdated:                timestamp(a.TimeUpdated),
	}
}
