package notificationhubs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// policyKeyBody is the RegenerateKeys request (armnotificationhubs.PolicykeyResource).
type policyKeyBody struct {
	PolicyKey string `json:"policyKey,omitempty"`
}

// namespaceAuthRuleRegenerate serves NamespacesClient.RegenerateKeys.
func (h *Handler) namespaceAuthRuleRegenerate(
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

	h.regenerateKeys(w, r, az, rp.ResourceName, ruleName, rp.ResourceName)
}

// hubAuthRuleRegenerate serves NotificationHubsClient.RegenerateKeys.
func (h *Handler) hubAuthRuleRegenerate(
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

	h.regenerateKeys(w, r, az, key, ruleName, rp.ResourceName)
}

// regenerateKeys rotates the requested key of a rule and writes the resulting
// ResourceListKeys. A recognized default rule is materialized on demand so its
// keys can be rotated even when it was never explicitly created, matching how
// ListKeys treats defaults.
func (*Handler) regenerateKeys(
	w http.ResponseWriter, r *http.Request, az notifdriver.AzureNotificationHubs,
	resourceKey, ruleName, namespace string,
) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var body policyKeyBody
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	if _, err := az.GetSASRule(r.Context(), resourceKey, ruleName); err != nil {
		rights, isDefault := defaultRuleRights[ruleName]
		if !isDefault {
			azurearm.WriteCErr(w, err)
			return
		}

		if _, perr := az.PutSASRule(r.Context(), resourceKey, ruleName,
			notifdriver.AzureSASRule{Rights: rights}); perr != nil {
			azurearm.WriteCErr(w, perr)
			return
		}
	}

	rule, err := az.RegenerateSASKey(r.Context(), resourceKey, ruleName, body.PolicyKey)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, resourceListKeys{
		KeyName:                   ruleName,
		PrimaryKey:                rule.PrimaryKey,
		SecondaryKey:              rule.SecondaryKey,
		PrimaryConnectionString:   sasConnectionString(namespace, ruleName, rule.PrimaryKey),
		SecondaryConnectionString: sasConnectionString(namespace, ruleName, rule.SecondaryKey),
	})
}
