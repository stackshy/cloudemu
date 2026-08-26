package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveEventDestinations routes .../event-destinations and .../{edName}.
func (h *Handler) serveEventDestinations(w http.ResponseWriter, r *http.Request, configSet string, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createEventDestination(w, r, configSet)
		case http.MethodGet:
			h.getEventDestinations(w, r, configSet)
		default:
			methodNotAllowed(w)
		}
	case 1:
		switch r.Method {
		case http.MethodPut:
			h.updateEventDestination(w, r, configSet, rest[0])
		case http.MethodDelete:
			h.deleteEventDestination(w, r, configSet, rest[0])
		default:
			methodNotAllowed(w)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func edInputFromJSON(configSet, name string, ed *eventDestinationDefJSON) driver.EventDestinationInput {
	in := driver.EventDestinationInput{ConfigurationSetName: configSet, EventDestinationName: name}
	if ed == nil {
		return in
	}

	in.Enabled = ed.Enabled
	in.MatchingEventTypes = ed.MatchingEventTypes

	if ed.KinesisFirehoseDestination != nil {
		in.KinesisFirehoseARN = ed.KinesisFirehoseDestination.DeliveryStreamArn
		in.KinesisFirehoseRoleARN = ed.KinesisFirehoseDestination.IamRoleArn
	}

	if ed.SnsDestination != nil {
		in.SNSTopicARN = ed.SnsDestination.TopicArn
	}

	if ed.CloudWatchDestination != nil {
		in.CloudWatchNamespace = "cloudwatch"
		in.CloudWatchDimensions = dimsToDriver(ed.CloudWatchDestination.DimensionConfigurations)
	}

	if ed.PinpointDestination != nil {
		in.PinpointApplicationARN = ed.PinpointDestination.ApplicationArn
	}

	return in
}

func dimsToDriver(dims []cloudWatchDimensionConfigJSON) []driver.CloudWatchDimension {
	if len(dims) == 0 {
		return nil
	}

	out := make([]driver.CloudWatchDimension, 0, len(dims))
	for i := range dims {
		out = append(out, driver.CloudWatchDimension{
			DimensionName:         dims[i].DimensionName,
			DimensionValueSource:  dims[i].DimensionValueSource,
			DefaultDimensionValue: dims[i].DefaultDimensionValue,
		})
	}

	return out
}

func dimsToWire(dims []driver.CloudWatchDimension) []cloudWatchDimensionConfigJSON {
	out := make([]cloudWatchDimensionConfigJSON, 0, len(dims))
	for i := range dims {
		out = append(out, cloudWatchDimensionConfigJSON{
			DimensionName:         dims[i].DimensionName,
			DimensionValueSource:  dims[i].DimensionValueSource,
			DefaultDimensionValue: dims[i].DefaultDimensionValue,
		})
	}

	return out
}

// eventDestinationToJSON renders a stored event destination as its wire shape,
// including whichever destination sub-block was configured.
func eventDestinationToJSON(ed *driver.EventDestination) eventDestinationJSON {
	out := eventDestinationJSON{
		Name:               ed.Name,
		Enabled:            ed.Enabled,
		MatchingEventTypes: ed.MatchingEventTypes,
	}

	if ed.SNSTopicARN != "" {
		out.SnsDestination = &snsDestinationJSON{TopicArn: ed.SNSTopicARN}
	}

	if ed.KinesisFirehoseARN != "" || ed.KinesisFirehoseRoleARN != "" {
		out.KinesisFirehoseDestination = &kinesisFirehoseDestinationJSON{
			IamRoleArn:        ed.KinesisFirehoseRoleARN,
			DeliveryStreamArn: ed.KinesisFirehoseARN,
		}
	}

	if ed.CloudWatchNamespace != "" {
		out.CloudWatchDestination = &cloudWatchDestinationJSON{
			DimensionConfigurations: dimsToWire(ed.CloudWatchDimensions),
		}
	}

	if ed.PinpointApplicationARN != "" {
		out.PinpointDestination = &pinpointDestinationJSON{ApplicationArn: ed.PinpointApplicationARN}
	}

	return out
}

func (h *Handler) createEventDestination(w http.ResponseWriter, r *http.Request, configSet string) {
	var req createEventDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := edInputFromJSON(configSet, req.EventDestinationName, req.EventDestination)
	if err := h.ses.CreateConfigurationSetEventDestination(r.Context(), in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) updateEventDestination(w http.ResponseWriter, r *http.Request, configSet, name string) {
	var req updateEventDestinationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	in := edInputFromJSON(configSet, name, req.EventDestination)
	if err := h.ses.UpdateConfigurationSetEventDestination(r.Context(), in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) deleteEventDestination(w http.ResponseWriter, r *http.Request, configSet, name string) {
	if err := h.ses.DeleteConfigurationSetEventDestination(r.Context(), configSet, name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) getEventDestinations(w http.ResponseWriter, r *http.Request, configSet string) {
	eds, err := h.ses.GetConfigurationSetEventDestinations(r.Context(), configSet)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]eventDestinationJSON, 0, len(eds))
	for i := range eds {
		out = append(out, eventDestinationToJSON(&eds[i]))
	}

	writeJSON(w, getEventDestinationsResponse{EventDestinations: out})
}

// putConfigSetOption dispatches the /configuration-sets/{name}/{option} PUTs.
func (h *Handler) putConfigSetOption(w http.ResponseWriter, r *http.Request, name, option string) {
	switch option {
	case "archiving-options":
		h.putArchivingOptions(w, r, name)
	case "delivery-options":
		h.putDeliveryOptions(w, r, name)
	case "reputation-options":
		h.putReputationOptions(w, r, name)
	case "sending":
		h.putSendingOptions(w, r, name)
	case "suppression-options":
		h.putSuppressionOptions(w, r, name)
	case "tracking-options":
		h.putTrackingOptions(w, r, name)
	case "vdm-options":
		h.putVdmOptions(w, r, name)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) putArchivingOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putArchivingOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetArchivingOptions(r.Context(), name, req.ArchiveArn))
}

func (h *Handler) putDeliveryOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putDeliveryOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetDeliveryOptions(r.Context(), name, req.TLSPolicy, req.SendingPoolName))
}

func (h *Handler) putReputationOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putReputationOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetReputationOptions(r.Context(), name, req.ReputationMetricsEnabled))
}

func (h *Handler) putSendingOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putSendingOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetSendingOptions(r.Context(), name, req.SendingEnabled))
}

func (h *Handler) putSuppressionOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putSuppressionOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetSuppressionOptions(r.Context(), name, req.SuppressedReasons))
}

func (h *Handler) putTrackingOptions(w http.ResponseWriter, r *http.Request, name string) {
	var req putTrackingOptionsRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	writeOK(w, h.ses.PutConfigurationSetTrackingOptions(r.Context(), name, req.CustomRedirectDomain))
}

func (h *Handler) putVdmOptions(w http.ResponseWriter, r *http.Request, name string) {
	writeOK(w, h.ses.PutConfigurationSetVdmOptions(r.Context(), name))
}
