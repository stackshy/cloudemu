package servicebus

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveAuthRule dispatches .../authorizationRules[/{name}[/{action}]].
func (h *Handler) serveAuthRule(w http.ResponseWriter, r *http.Request, sp sbPath) {
	segs := sp.segs

	switch {
	case len(segs) == authRuleColl:
		h.listAuthRules(w, r, sp)
	case len(segs) == authRuleItem:
		h.serveAuthRuleItem(w, r, sp, segs[1])
	case len(segs) == authRuleAction && eq(segs[2], actionKeys):
		h.listKeys(w, r, sp, segs[1])
	case len(segs) == authRuleAction && eq(segs[2], actionRegen):
		h.regenerateKeys(w, r, sp, segs[1])
	default:
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "unsupported path")
	}
}

func (h *Handler) serveAuthRuleItem(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createAuthRule(w, r, sp, name)
	case http.MethodGet:
		h.getAuthRule(w, sp, name)
	case http.MethodDelete:
		h.deleteAuthRule(w, sp, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createAuthRule(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	var req createAuthRuleRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	rec, existed := ns.AuthRules[name]
	if !existed {
		rec = &authRuleRecord{Name: name, PrimaryKey: generateKey(), SecondaryKey: generateKey()}
		ns.AuthRules[name] = rec
	}

	rec.Rights = append([]string(nil), req.Properties.Rights...)

	resource := toAuthRuleResource(sp, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getAuthRule(w http.ResponseWriter, sp sbPath, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rec, ok := h.lookupAuthRule(sp, name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAuthRuleResource(sp, rec))
}

func (h *Handler) deleteAuthRule(w http.ResponseWriter, sp sbPath, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ns, ok := h.getNS(sp)
	if !ok {
		writeNSNotFound(w, sp.namespace)
		return
	}

	if _, ok := ns.AuthRules[name]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(ns.AuthRules, name)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listAuthRules(w http.ResponseWriter, r *http.Request, sp sbPath) {
	h.listChildren(w, r, sp, func(ns *namespaceState) []any {
		out := make([]any, 0, len(ns.AuthRules))
		for _, n := range sortedKeys(ns.AuthRules) {
			out = append(out, toAuthRuleResource(sp, ns.AuthRules[n]))
		}

		return out
	})
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	rec, ok := h.lookupAuthRule(sp, name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(sp.namespace, rec))
}

func (h *Handler) regenerateKeys(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var req regenerateKeysRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rec, ok := h.lookupAuthRule(sp, name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule not found: "+name)
		return
	}

	newKey := req.Key
	if newKey == "" {
		newKey = generateKey()
	}

	if eq(req.KeyType, "SecondaryKey") {
		rec.SecondaryKey = newKey
	} else {
		rec.PrimaryKey = newKey
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(sp.namespace, rec))
}

// lookupAuthRule returns the auth rule; caller must hold h.mu.
func (h *Handler) lookupAuthRule(sp sbPath, name string) (*authRuleRecord, bool) {
	ns, ok := h.getNS(sp)
	if !ok {
		return nil, false
	}

	rec, ok := ns.AuthRules[name]

	return rec, ok
}

func toAuthRuleResource(sp sbPath, rec *authRuleRecord) authRuleResource {
	return authRuleResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/authorizationRules/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/AuthorizationRules",
		Properties: authRuleProperties{Rights: rec.Rights},
	}
}

func toAccessKeys(namespace string, rec *authRuleRecord) accessKeys {
	return accessKeys{
		KeyName:                   rec.Name,
		PrimaryKey:                rec.PrimaryKey,
		SecondaryKey:              rec.SecondaryKey,
		PrimaryConnectionString:   connectionString(namespace, rec.Name, rec.PrimaryKey),
		SecondaryConnectionString: connectionString(namespace, rec.Name, rec.SecondaryKey),
	}
}

func connectionString(namespace, ruleName, key string) string {
	return "Endpoint=sb://" + namespace + sbHost + "/;SharedAccessKeyName=" + ruleName +
		";SharedAccessKey=" + key
}
