package ssm

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

type serviceSettingRequest struct {
	SettingID    string `json:"SettingId"`
	SettingValue string `json:"SettingValue"`
}

// serviceSettingJSON is the wire shape of a ServiceSetting.
type serviceSettingJSON struct {
	ARN              string  `json:"ARN,omitempty"`
	LastModifiedDate float64 `json:"LastModifiedDate,omitempty"`
	LastModifiedUser string  `json:"LastModifiedUser,omitempty"`
	SettingID        string  `json:"SettingId,omitempty"`
	SettingValue     string  `json:"SettingValue,omitempty"`
	Status           string  `json:"Status,omitempty"`
}

type serviceSettingResponse struct {
	ServiceSetting serviceSettingJSON `json:"ServiceSetting"`
}

// serviceSettings returns the driver's service settings, or writes an error
// when the driver has none.
func (h *Handler) serviceSettings(w http.ResponseWriter) (ssmdriver.ServiceSettings, bool) {
	s, ok := h.store.(ssmdriver.ServiceSettings)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnsupportedOperationException", "this driver does not support service settings")
	}

	return s, ok
}

func (h *Handler) getServiceSetting(w http.ResponseWriter, r *http.Request) {
	store, ok := h.serviceSettings(w)
	if !ok {
		return
	}

	var req serviceSettingRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	setting, err := store.GetServiceSetting(r.Context(), req.SettingID)
	if err != nil {
		writeSettingErr(w, err)
		return
	}

	wire.WriteJSON(w, serviceSettingResponse{ServiceSetting: toServiceSettingJSON(setting)})
}

func (h *Handler) updateServiceSetting(w http.ResponseWriter, r *http.Request) {
	store, ok := h.serviceSettings(w)
	if !ok {
		return
	}

	var req serviceSettingRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := store.UpdateServiceSetting(r.Context(), req.SettingID, req.SettingValue); err != nil {
		writeSettingErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) resetServiceSetting(w http.ResponseWriter, r *http.Request) {
	store, ok := h.serviceSettings(w)
	if !ok {
		return
	}

	var req serviceSettingRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	setting, err := store.ResetServiceSetting(r.Context(), req.SettingID)
	if err != nil {
		writeSettingErr(w, err)
		return
	}

	wire.WriteJSON(w, serviceSettingResponse{ServiceSetting: toServiceSettingJSON(setting)})
}

// writeSettingErr maps an unknown setting id to ServiceSettingNotFound.
func writeSettingErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ssmdriver.ErrServiceSettingNotFound) {
		wire.WriteJSONError(w, http.StatusBadRequest, "ServiceSettingNotFound", cerrors.Message(err))
		return
	}

	writeErr(w, err)
}

func toServiceSettingJSON(s *ssmdriver.ServiceSetting) serviceSettingJSON {
	return serviceSettingJSON{
		ARN:              s.ARN,
		LastModifiedDate: epochSeconds(s.LastModifiedDate),
		LastModifiedUser: s.LastModifiedUser,
		SettingID:        s.SettingID,
		SettingValue:     s.SettingValue,
		Status:           s.Status,
	}
}

// policyErrorCode returns the AWS exception for a parameter policy error.
func policyErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, ssmdriver.ErrInvalidPolicyType):
		return "InvalidPolicyTypeException", true
	case errors.Is(err, ssmdriver.ErrInvalidPolicyAttribute):
		return "InvalidPolicyAttributeException", true
	case errors.Is(err, ssmdriver.ErrIncompatiblePolicy):
		return "IncompatiblePolicyException", true
	case errors.Is(err, ssmdriver.ErrPoliciesLimitExceeded):
		return "PoliciesLimitExceededException", true
	default:
		return "", false
	}
}
