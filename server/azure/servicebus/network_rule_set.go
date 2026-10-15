package servicebus

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const segNetworkRuleSets = "networkRuleSets"

// serveNetworkRuleSet serves the namespace's networkRuleSets/default singleton.
func (h *Handler) serveNetworkRuleSet(w http.ResponseWriter, r *http.Request, sp sbPath) {
	azurearm.ServeNetworkRuleSet(w, r, sp.segs[1:], azurearm.NetworkRuleSetStore{
		Type: providerName + "/Namespaces/NetworkRuleSets",
		Load: func() (string, string, azurearm.NetworkRuleSetProps, bool) {
			h.mu.RLock()
			defer h.mu.RUnlock()

			ns, ok := h.getNS(sp)
			if !ok {
				return "", "", azurearm.NetworkRuleSetProps{}, false
			}

			return nsIDPrefix(sp), ns.Location, azurearm.CloneNetworkRuleSet(ns.NetworkRuleSet), true
		},
		Store: func(p *azurearm.NetworkRuleSetProps) (string, string, bool) {
			h.mu.Lock()
			defer h.mu.Unlock()

			ns, ok := h.getNS(sp)
			if !ok {
				return "", "", false
			}

			ns.NetworkRuleSet = p

			return nsIDPrefix(sp), ns.Location, true
		},
		NotFound: func(w http.ResponseWriter) { writeNSNotFound(w, sp.namespace) },
	})
}

const segDRConfigs = "disasterRecoveryConfigs"

// serveDRConfigs answers the geo-DR pairing reads. No pairing is modeled, so
// the list is empty and a named pairing is not found; writes are 501.
// azurerm's queue and topic authorization rule create polls the list to wait
// for replication.
func (h *Handler) serveDRConfigs(w http.ResponseWriter, r *http.Request, sp sbPath) {
	switch {
	case len(sp.segs) == 1:
		h.listChildren(w, r, sp, func(*namespaceState) []any { return []any{} })
	case r.Method == http.MethodGet:
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
			"disaster recovery config not found: "+strings.Join(sp.segs[1:], "/"))
	default:
		notImplemented(w)
	}
}
