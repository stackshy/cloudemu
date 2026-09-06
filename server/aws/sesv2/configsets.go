package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveConfigSets routes /configuration-sets and its sub-paths.
func (h *Handler) serveConfigSets(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createConfigSet(w, r)
		case http.MethodGet:
			h.listConfigSets(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		switch r.Method {
		case http.MethodGet:
			h.getConfigSet(w, r, rest[0])
		case http.MethodDelete:
			h.deleteConfigSet(w, r, rest[0])
		default:
			methodNotAllowed(w)
		}
	default:
		h.serveConfigSetSub(w, r, rest[0], rest[1:])
	}
}

// serveConfigSetSub routes /configuration-sets/{name}/{sub...}.
func (h *Handler) serveConfigSetSub(w http.ResponseWriter, r *http.Request, name string, sub []string) {
	if sub[0] == "event-destinations" {
		h.serveEventDestinations(w, r, name, sub[1:])

		return
	}

	if len(sub) != 1 {
		notFound(w, r.URL.Path)

		return
	}

	if r.Method != http.MethodPut {
		methodNotAllowed(w)

		return
	}

	h.putConfigSetOption(w, r, name, sub[0])
}

func (h *Handler) createConfigSet(w http.ResponseWriter, r *http.Request) {
	var req createConfigurationSetRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if err := h.ses.CreateConfigurationSet(r.Context(), configSetInputFromRequest(&req)); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

// configSetInputFromRequest maps a create request, including its optional
// nested option blocks, onto the driver input.
func configSetInputFromRequest(req *createConfigurationSetRequest) driver.CreateConfigurationSetInput {
	in := driver.CreateConfigurationSetInput{
		Name: req.ConfigurationSetName,
		Tags: tagsToMap(req.Tags),
	}

	if req.SendingOptions != nil {
		in.SendingEnabled = req.SendingOptions.SendingEnabled
	}

	if req.ReputationOptions != nil {
		in.ReputationOn = req.ReputationOptions.ReputationMetricsEnabled
	}

	if req.DeliveryOptions != nil {
		in.TLSPolicy = req.DeliveryOptions.TLSPolicy
		in.SendingPoolN = req.DeliveryOptions.SendingPoolName
	}

	if req.SuppressionOptions != nil {
		in.SuppressedReasons = req.SuppressionOptions.SuppressedReasons
	}

	if req.TrackingOptions != nil {
		in.CustomRedirectDom = req.TrackingOptions.CustomRedirectDomain
		in.TrackingHTTPSPolicy = req.TrackingOptions.HTTPSPolicy
	}

	applyVdmToInput(&in, req.VdmOptions)

	return in
}

// applyVdmToInput copies the VDM option block onto the driver input.
func applyVdmToInput(in *driver.CreateConfigurationSetInput, vdm *vdmOptionsJSON) {
	if vdm == nil {
		return
	}

	in.VdmConfigured = true
	if vdm.DashboardOptions != nil {
		in.VdmEngagement = vdm.DashboardOptions.EngagementMetrics
	}

	if vdm.GuardianOptions != nil {
		in.VdmGuardianDelivery = vdm.GuardianOptions.OptimizedSharedDelivery
	}
}

func (h *Handler) getConfigSet(w http.ResponseWriter, r *http.Request, name string) {
	cs, err := h.ses.GetConfigurationSet(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getConfigurationSetResponse{
		ConfigurationSetName: cs.Name,
		SendingOptions:       sendingOptionsJSON{SendingEnabled: cs.SendingEnabled},
		ReputationOptions:    reputationOptionsJSON{ReputationMetricsEnabled: cs.ReputationOn},
		DeliveryOptions:      deliveryOptionsToJSON(cs),
		SuppressionOptions:   suppressionOptionsToJSON(cs),
		TrackingOptions:      trackingOptionsToJSON(cs),
		VdmOptions:           vdmOptionsToJSON(cs),
		Tags:                 mapToTags(cs.Tags),
	})
}

// deliveryOptionsToJSON renders the config-set delivery options, or nil when
// no TLS policy or sending pool is set, so an unconfigured set does not report
// an empty delivery-options block (which drives IaC drift).
func deliveryOptionsToJSON(cs *driver.ConfigurationSet) *deliveryOptionsJSON {
	if cs.TLSPolicy == "" && cs.SendingPoolN == "" {
		return nil
	}

	return &deliveryOptionsJSON{TLSPolicy: cs.TLSPolicy, SendingPoolName: cs.SendingPoolN}
}

// suppressionOptionsToJSON renders the config-set suppression options, or nil
// when no suppressed reasons are set.
func suppressionOptionsToJSON(cs *driver.ConfigurationSet) *suppressionOptionsJSON {
	if len(cs.SuppressedReasons) == 0 {
		return nil
	}

	return &suppressionOptionsJSON{SuppressedReasons: cs.SuppressedReasons}
}

// trackingOptionsToJSON renders the config-set tracking options, or nil when
// no custom redirect domain or HTTPS policy is set.
func trackingOptionsToJSON(cs *driver.ConfigurationSet) *trackingOptionsJSON {
	if cs.CustomRedirectDom == "" && cs.TrackingHTTPSPolicy == "" {
		return nil
	}

	return &trackingOptionsJSON{CustomRedirectDomain: cs.CustomRedirectDom, HTTPSPolicy: cs.TrackingHTTPSPolicy}
}

// vdmOptionsToJSON renders the config-set VDM options, or nil when VDM is not
// configured on the set.
func vdmOptionsToJSON(cs *driver.ConfigurationSet) *vdmOptionsJSON {
	if !cs.VdmEnabled && cs.VdmEngagement == "" && cs.VdmGuardianDelivery == "" {
		return nil
	}

	out := &vdmOptionsJSON{}
	if cs.VdmEngagement != "" {
		out.DashboardOptions = &vdmDashboardOptionsJSON{EngagementMetrics: cs.VdmEngagement}
	}

	if cs.VdmGuardianDelivery != "" {
		out.GuardianOptions = &vdmGuardianOptionsJSON{OptimizedSharedDelivery: cs.VdmGuardianDelivery}
	}

	return out
}

func (h *Handler) deleteConfigSet(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.ses.DeleteConfigurationSet(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) listConfigSets(w http.ResponseWriter, r *http.Request) {
	names, err := h.ses.ListConfigurationSets(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	start, end, next := pageWindow(len(names), r.URL.Query())

	writeJSON(w, listConfigurationSetsResponse{ConfigurationSets: names[start:end], NextToken: next})
}
