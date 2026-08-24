package notificationhubs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Default authorization rules Azure auto-creates. ListKeys on one of these
// succeeds even without an explicit CreateOrUpdate.
var defaultRuleRights = map[string][]string{ //nolint:gochecknoglobals // static lookup table
	"RootManageSharedAccessKey":          {"Listen", "Manage", "Send"},
	"DefaultFullSharedAccessSignature":   {"Listen", "Manage", "Send"},
	"DefaultListenSharedAccessSignature": {"Listen"},
}

// requireAz resolves the Azure-only capability or writes a 501.
func (h *Handler) requireAz(w http.ResponseWriter) (notifdriver.AzureNotificationHubs, bool) {
	az, ok := h.az()
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"authorization rules are not supported by this notification driver")

		return nil, false
	}

	return az, true
}

// --- namespace authorization rules ---

func (h *Handler) serveNamespaceAuthRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, ruleName string,
) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	if _, err := h.notif.GetTopic(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.serveAuthRule(w, r, az, authRuleCtx{
		resourceKey: rp.ResourceName,
		ruleName:    ruleName,
		id:          nsAuthRuleID(rp, ruleName),
		typ:         nsAuthRuleType,
	})
}

func (h *Handler) listNamespaceAuthRules(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	h.listAuthRules(w, r, az, rp.ResourceName, func(name string) (string, string) {
		return nsAuthRuleID(rp, name), nsAuthRuleType
	})
}

func (h *Handler) namespaceAuthRuleKeys(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, ruleName string,
) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	h.writeKeys(w, r, az, rp.ResourceName, ruleName, rp.ResourceName)
}

// --- hub authorization rules ---

func (h *Handler) serveHubAuthRule(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hub, ruleName string,
) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	key := hubKey(rp.ResourceName, hub)
	if _, err := h.notif.GetTopic(r.Context(), key); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.serveAuthRule(w, r, az, authRuleCtx{
		resourceKey: key,
		ruleName:    ruleName,
		id:          hubAuthRuleID(rp, hub, ruleName),
		typ:         hubAuthRuleType,
	})
}

func (h *Handler) listHubAuthRules(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hub string) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	h.listAuthRules(w, r, az, hubKey(rp.ResourceName, hub), func(name string) (string, string) {
		return hubAuthRuleID(rp, hub, name), hubAuthRuleType
	})
}

func (h *Handler) hubAuthRuleKeys(
	w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, hub, ruleName string,
) {
	az, ok := h.requireAz(w)
	if !ok {
		return
	}

	h.writeKeys(w, r, az, hubKey(rp.ResourceName, hub), ruleName, rp.ResourceName)
}

// --- shared implementation ---

type authRuleCtx struct {
	resourceKey string
	ruleName    string
	id          string
	typ         string
}

func (h *Handler) serveAuthRule(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c authRuleCtx,
) {
	switch r.Method {
	case http.MethodPut:
		h.putAuthRule(w, r, az, c)
	case http.MethodGet:
		rule, err := az.GetSASRule(r.Context(), c.resourceKey, c.ruleName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		azurearm.WriteJSON(w, http.StatusOK, toAuthRuleJSON(c, rule))
	case http.MethodDelete:
		if err := az.DeleteSASRule(r.Context(), c.resourceKey, c.ruleName); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) putAuthRule(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs, c authRuleCtx,
) {
	var body authRulePutBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	rights := []string(nil)
	if body.Properties != nil {
		rights = body.Properties.Rights
	}

	rule, err := az.PutSASRule(r.Context(), c.resourceKey, c.ruleName, notifdriver.AzureSASRule{Rights: rights})
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAuthRuleJSON(c, rule))
}

func (*Handler) listAuthRules(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs,
	resourceKey string, idType func(string) (string, string),
) {
	rules, err := az.ListSASRules(r.Context(), resourceKey)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]authRuleJSON, 0, len(rules))
	for name, rule := range rules {
		id, typ := idType(name)
		out = append(out, toAuthRuleJSON(authRuleCtx{ruleName: name, id: id, typ: typ}, rule))
	}

	azurearm.WriteJSON(w, http.StatusOK, authRuleListResult{Value: out})
}

// writeKeys resolves and writes the ListKeys response for a rule, lazily
// materializing a recognized default rule so its keys are always available.
func (*Handler) writeKeys(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs,
	resourceKey, ruleName, namespace string,
) {
	rule, err := az.GetSASRule(r.Context(), resourceKey, ruleName)
	if err != nil {
		rights, isDefault := defaultRuleRights[ruleName]
		if !isDefault {
			azurearm.WriteCErr(w, err)
			return
		}

		rule, err = az.PutSASRule(r.Context(), resourceKey, ruleName, notifdriver.AzureSASRule{Rights: rights})
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}
	}

	azurearm.WriteJSON(w, http.StatusOK, resourceListKeys{
		KeyName:                   ruleName,
		PrimaryKey:                rule.PrimaryKey,
		SecondaryKey:              rule.SecondaryKey,
		PrimaryConnectionString:   sasConnectionString(namespace, ruleName, rule.PrimaryKey),
		SecondaryConnectionString: sasConnectionString(namespace, ruleName, rule.SecondaryKey),
	})
}

func toAuthRuleJSON(c authRuleCtx, rule notifdriver.AzureSASRule) authRuleJSON {
	return authRuleJSON{
		ID:   c.id,
		Name: c.ruleName,
		Type: c.typ,
		Properties: &authRuleProperties{
			Rights:       rule.Rights,
			PrimaryKey:   rule.PrimaryKey,
			SecondaryKey: rule.SecondaryKey,
			KeyName:      c.ruleName,
		},
	}
}

func nsAuthRuleID(rp *azurearm.ResourcePath, ruleName string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNamespaces, rp.ResourceName) +
		"/" + subAuthorizationRules + "/" + ruleName
}

func hubAuthRuleID(rp *azurearm.ResourcePath, hub, ruleName string) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeNamespaces, rp.ResourceName) +
		"/" + subHubs + "/" + hub + "/" + subAuthorizationRules + "/" + ruleName
}

// checkNamespaceAvailability serves Namespaces.CheckAvailability. The emulator
// treats every non-empty name as available.
func (h *Handler) checkNamespaceAvailability(w http.ResponseWriter, r *http.Request, _ *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var body checkAvailabilityBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	_, err := h.notif.GetTopic(r.Context(), body.Name)

	azurearm.WriteJSON(w, http.StatusOK, checkAvailabilityResult{
		Name:         body.Name,
		IsAvailiable: err != nil,
	})
}
