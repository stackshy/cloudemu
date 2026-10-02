package sql

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// tdeName is the only transparentDataEncryption sub-resource name Azure defines
// ("current"), echoed on read responses.
const tdeName = "current"

func (h *Handler) transparentDataEncryption() (rdsdriver.TransparentDataEncryptions, bool) {
	c, ok := h.db.(rdsdriver.TransparentDataEncryptions)
	return c, ok
}

type armTDE struct {
	ID         string     `json:"id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Type       string     `json:"type,omitempty"`
	Properties *armTDECfg `json:"properties,omitempty"`
}

type armTDECfg struct {
	State string `json:"state,omitempty"`
}

// serveTDE handles the database transparentDataEncryption/current sub-resource.
// Real Azure SQL TDE PUT is synchronous, so every verb returns 200 inline with
// no LRO. There is no Delete: TDE cannot be removed, only toggled.
func (h *Handler) serveTDE(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	tde, ok := h.transparentDataEncryption()
	if !ok {
		writeUnsupported(w, "transparentDataEncryption")
		return
	}

	// A path ending at .../transparentDataEncryption (no "/current" name) is
	// the list (ListByDatabase); one with the name is the single resource.
	if rp.Rest == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listTDE(w, r, rp, tde)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putTDE(w, r, rp, tde)
	case http.MethodGet:
		h.getTDE(w, r, rp, tde)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putTDE(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, tde rdsdriver.TransparentDataEncryptions,
) {
	var body armTDE
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := rdsdriver.TransparentDataEncryptionConfig{Server: rp.ResourceName, Database: rp.SubResourceName}
	if body.Properties != nil {
		cfg.State = body.Properties.State
	}

	out, err := tde.SetTransparentDataEncryption(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMTDE(out, rp))
}

func (*Handler) getTDE(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, tde rdsdriver.TransparentDataEncryptions,
) {
	out, err := tde.GetTransparentDataEncryption(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toARMTDE(out, rp))
}

func (*Handler) listTDE(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, tde rdsdriver.TransparentDataEncryptions,
) {
	items, err := tde.ListTransparentDataEncryption(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]armTDE, 0, len(items))
	for i := range items {
		out = append(out, toARMTDE(&items[i], rp))
	}

	azurearm.WriteJSON(w, http.StatusOK, armList[armTDE]{Value: out})
}

func toARMTDE(t *rdsdriver.TransparentDataEncryption, rp *azurearm.ResourcePath) armTDE {
	dbID := armDatabaseID(rp.Subscription, rp.ResourceGroup, t.Server, t.Database)

	return armTDE{
		ID:         dbID + "/" + subTDE + "/" + tdeName,
		Name:       tdeName,
		Type:       providerName + "/" + resourceServers + "/" + subResourceDatabases + "/" + subTDE,
		Properties: &armTDECfg{State: t.State},
	}
}
