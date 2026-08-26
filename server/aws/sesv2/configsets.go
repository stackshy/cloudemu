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

	if err := h.ses.CreateConfigurationSet(r.Context(), in); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
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
		DeliveryOptions:      deliveryOptionsJSON{TLSPolicy: cs.TLSPolicy, SendingPoolName: cs.SendingPoolN},
		Tags:                 mapToTags(cs.Tags),
	})
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
