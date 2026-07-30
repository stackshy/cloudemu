package ecs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

func (h *Handler) routeAccount(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "PutAccountSetting":
		h.putAccountSetting(w, r, false)
	case "PutAccountSettingDefault":
		h.putAccountSetting(w, r, true)
	case "ListAccountSettings":
		h.listAccountSettings(w, r)
	case "DeleteAccountSetting":
		h.deleteAccountSetting(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) putAccountSetting(w http.ResponseWriter, r *http.Request, isDefault bool) {
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	put := h.ecs.PutAccountSetting
	if isDefault {
		put = h.ecs.PutAccountSettingDefault
	}

	s, err := put(r.Context(), req.Name, req.Value)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"setting": fromAccountSetting(s)})
}

func (h *Handler) listAccountSettings(w http.ResponseWriter, r *http.Request) {
	// The mock ignores the name/value/principal/effective filters and returns
	// every stored setting; the request is decoded to consume the body.
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	settings, err := h.ecs.ListAccountSettings(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"settings": fromAccountSettings(settings)})
}

func (h *Handler) deleteAccountSetting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	s, err := h.ecs.DeleteAccountSetting(r.Context(), req.Name)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"setting": fromAccountSetting(s)})
}
