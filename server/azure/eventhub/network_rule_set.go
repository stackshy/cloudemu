package eventhub

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const segNetworkRuleSets = "networkRuleSets"

// serveNetworkRuleSet serves the namespace's networkRuleSets/default singleton.
func (h *Handler) serveNetworkRuleSet(w http.ResponseWriter, r *http.Request, ep ehPath) {
	azurearm.ServeNetworkRuleSet(w, r, ep.segs[1:], azurearm.NetworkRuleSetStore{
		Type: providerName + "/Namespaces/NetworkRuleSets",
		Load: func() (string, string, azurearm.NetworkRuleSetProps, bool) {
			h.mu.RLock()
			defer h.mu.RUnlock()

			ns, ok := h.getNS(ep)
			if !ok {
				return "", "", azurearm.NetworkRuleSetProps{}, false
			}

			return nsIDPrefix(ep), ns.Location, azurearm.CloneNetworkRuleSet(ns.NetworkRuleSet), true
		},
		Store: func(p *azurearm.NetworkRuleSetProps) (string, string, bool) {
			h.mu.Lock()
			defer h.mu.Unlock()

			ns, ok := h.getNS(ep)
			if !ok {
				return "", "", false
			}

			ns.NetworkRuleSet = p

			return nsIDPrefix(ep), ns.Location, true
		},
		NotFound: func(w http.ResponseWriter) { writeNSNotFound(w, ep.namespace) },
	})
}
