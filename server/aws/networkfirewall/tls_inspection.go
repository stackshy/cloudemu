package networkfirewall

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

type certificateJSON struct {
	CertificateArn string `json:"CertificateArn"`
}

type tlsResponseJSON struct {
	TLSInspectionConfigurationName   string            `json:"TLSInspectionConfigurationName"`
	TLSInspectionConfigurationArn    string            `json:"TLSInspectionConfigurationArn"`
	TLSInspectionConfigurationID     string            `json:"TLSInspectionConfigurationId"`
	TLSInspectionConfigurationStatus string            `json:"TLSInspectionConfigurationStatus,omitempty"`
	Description                      string            `json:"Description,omitempty"`
	NumberOfAssociations             int               `json:"NumberOfAssociations"`
	LastModifiedTime                 *float64          `json:"LastModifiedTime,omitempty"`
	Certificates                     []certificateJSON `json:"Certificates,omitempty"`
	Tags                             []tag             `json:"Tags,omitempty"`
}

type createTLSRequest struct {
	TLSInspectionConfigurationName string          `json:"TLSInspectionConfigurationName"`
	TLSInspectionConfiguration     json.RawMessage `json:"TLSInspectionConfiguration"`
	Description                    string          `json:"Description"`
	Tags                           []tag           `json:"Tags"`
}

type tlsNameArnRequest struct {
	TLSInspectionConfigurationName string `json:"TLSInspectionConfigurationName"`
	TLSInspectionConfigurationArn  string `json:"TLSInspectionConfigurationArn"`
}

type updateTLSRequest struct {
	tlsNameArnRequest
	UpdateToken                string          `json:"UpdateToken"`
	TLSInspectionConfiguration json.RawMessage `json:"TLSInspectionConfiguration"`
	Description                string          `json:"Description"`
}

func toTLSResponseJSON(t *nfdriver.TLSInspectionConfig) tlsResponseJSON {
	modified := epochSeconds(t.LastModified)

	certs := make([]certificateJSON, 0, len(t.CertificateARNs))
	for _, arn := range t.CertificateARNs {
		certs = append(certs, certificateJSON{CertificateArn: arn})
	}

	return tlsResponseJSON{
		TLSInspectionConfigurationName: t.Name, TLSInspectionConfigurationArn: t.ARN,
		TLSInspectionConfigurationID: t.ID, TLSInspectionConfigurationStatus: t.Status,
		Description: t.Description, NumberOfAssociations: t.NumberOfAssociations,
		LastModifiedTime: &modified, Certificates: certs, Tags: mapToTags(t.Tags),
	}
}

func (h *Handler) createTLSInspectionConfiguration(w http.ResponseWriter, r *http.Request) {
	var req createTLSRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tls, ok := capability[nfdriver.TLSInspection](h, w)
	if !ok {
		return
	}

	t, err := tls.CreateTLSInspectionConfiguration(r.Context(), nfdriver.CreateTLSInspectionConfig{
		Name: req.TLSInspectionConfigurationName, Description: req.Description,
		Configuration: req.TLSInspectionConfiguration, Tags: tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":                        t.UpdateToken,
		"TLSInspectionConfigurationResponse": toTLSResponseJSON(t),
	})
}

func (h *Handler) describeTLSInspectionConfiguration(w http.ResponseWriter, r *http.Request) {
	var req tlsNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tls, ok := capability[nfdriver.TLSInspection](h, w)
	if !ok {
		return
	}

	t, err := tls.DescribeTLSInspectionConfiguration(r.Context(), req.TLSInspectionConfigurationName, req.TLSInspectionConfigurationArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":                        t.UpdateToken,
		"TLSInspectionConfigurationResponse": toTLSResponseJSON(t),
		"TLSInspectionConfiguration":         t.Configuration,
	})
}

func (h *Handler) updateTLSInspectionConfiguration(w http.ResponseWriter, r *http.Request) {
	var req updateTLSRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tls, ok := capability[nfdriver.TLSInspection](h, w)
	if !ok {
		return
	}

	t, err := tls.UpdateTLSInspectionConfiguration(r.Context(), req.TLSInspectionConfigurationName, req.TLSInspectionConfigurationArn,
		nfdriver.UpdateTLSInspectionConfig{
			UpdateToken: req.UpdateToken, Description: req.Description, Configuration: req.TLSInspectionConfiguration,
		})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"UpdateToken":                        t.UpdateToken,
		"TLSInspectionConfigurationResponse": toTLSResponseJSON(t),
	})
}

func (h *Handler) deleteTLSInspectionConfiguration(w http.ResponseWriter, r *http.Request) {
	var req tlsNameArnRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tls, ok := capability[nfdriver.TLSInspection](h, w)
	if !ok {
		return
	}

	t, err := tls.DeleteTLSInspectionConfiguration(r.Context(), req.TLSInspectionConfigurationName, req.TLSInspectionConfigurationArn)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TLSInspectionConfigurationResponse": toTLSResponseJSON(t)})
}

func (h *Handler) listTLSInspectionConfigurations(w http.ResponseWriter, r *http.Request) {
	var req listRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tls, ok := capability[nfdriver.TLSInspection](h, w)
	if !ok {
		return
	}

	items, next, err := tls.ListTLSInspectionConfigurations(r.Context(), req.options())
	if err != nil {
		writeErr(w, err)
		return
	}

	metas := make([]map[string]string, 0, len(items))
	for i := range items {
		metas = append(metas, map[string]string{"Name": items[i].Name, "Arn": items[i].ARN})
	}

	wire.WriteJSON(w, withNextToken(map[string]any{"TLSInspectionConfigurations": metas}, next))
}
