package cognito

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

type softwareTokenMfaJSON struct {
	Enabled bool `json:"Enabled"`
}

type smsConfigurationJSON struct {
	SnsCallerArn string `json:"SnsCallerArn,omitempty"`
	ExternalID   string `json:"ExternalId,omitempty"`
	SnsRegion    string `json:"SnsRegion,omitempty"`
}

type smsMfaConfigJSON struct {
	SmsAuthenticationMessage string                `json:"SmsAuthenticationMessage,omitempty"`
	SmsConfiguration         *smsConfigurationJSON `json:"SmsConfiguration,omitempty"`
}

type mfaConfigResponse struct {
	MfaConfiguration              string                `json:"MfaConfiguration,omitempty"`
	SoftwareTokenMfaConfiguration *softwareTokenMfaJSON `json:"SoftwareTokenMfaConfiguration,omitempty"`
	SmsMfaConfiguration           *smsMfaConfigJSON     `json:"SmsMfaConfiguration,omitempty"`
}

func mfaConfigToWire(c *driver.UserPoolMfaConfig) mfaConfigResponse {
	out := mfaConfigResponse{MfaConfiguration: c.MFAConfiguration}

	if c.SoftwareTokenMfaConfiguration != nil {
		out.SoftwareTokenMfaConfiguration = &softwareTokenMfaJSON{Enabled: c.SoftwareTokenMfaConfiguration.Enabled}
	}

	if c.SmsMfaConfiguration != nil {
		sms := &smsMfaConfigJSON{SmsAuthenticationMessage: c.SmsMfaConfiguration.SmsAuthenticationMessage}
		if s := c.SmsMfaConfiguration.SmsConfiguration; s != nil {
			sms.SmsConfiguration = &smsConfigurationJSON{
				SnsCallerArn: s.SnsCallerARN,
				ExternalID:   s.ExternalID,
				SnsRegion:    s.SnsRegion,
			}
		}

		out.SmsMfaConfiguration = sms
	}

	return out
}

func smsMfaFromWire(in *smsMfaConfigJSON) *driver.SmsMfaConfig {
	if in == nil {
		return nil
	}

	out := &driver.SmsMfaConfig{SmsAuthenticationMessage: in.SmsAuthenticationMessage}
	if s := in.SmsConfiguration; s != nil {
		out.SmsConfiguration = &driver.SmsConfiguration{
			SnsCallerARN: s.SnsCallerArn,
			ExternalID:   s.ExternalID,
			SnsRegion:    s.SnsRegion,
		}
	}

	return out
}

type getUserPoolMfaConfigRequest struct {
	UserPoolID string `json:"UserPoolId"`
}

func (h *Handler) getUserPoolMfaConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getUserPoolMfaConfigRequest) (any, error) {
		cfg, err := h.cognito.GetUserPoolMfaConfig(ctx, req.UserPoolID)
		if err != nil {
			return nil, err
		}

		return mfaConfigToWire(cfg), nil
	})
}

type setUserPoolMfaConfigRequest struct {
	UserPoolID                    string                `json:"UserPoolId"`
	MfaConfiguration              string                `json:"MfaConfiguration"`
	SoftwareTokenMfaConfiguration *softwareTokenMfaJSON `json:"SoftwareTokenMfaConfiguration"`
	SmsMfaConfiguration           *smsMfaConfigJSON     `json:"SmsMfaConfiguration"`
}

func (h *Handler) setUserPoolMfaConfig(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *setUserPoolMfaConfigRequest) (any, error) {
		in := driver.SetUserPoolMfaConfigInput{
			UserPoolID:          req.UserPoolID,
			MFAConfiguration:    req.MfaConfiguration,
			SmsMfaConfiguration: smsMfaFromWire(req.SmsMfaConfiguration),
		}
		if s := req.SoftwareTokenMfaConfiguration; s != nil {
			in.SoftwareTokenMfaConfiguration = &driver.SoftwareTokenMfaConfig{Enabled: s.Enabled}
		}

		cfg, err := h.cognito.SetUserPoolMfaConfig(ctx, in)
		if err != nil {
			return nil, err
		}

		return mfaConfigToWire(cfg), nil
	})
}
