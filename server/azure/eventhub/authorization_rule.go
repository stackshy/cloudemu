package eventhub

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// authTarget is the resolved holder of a set of authorization rules, at either
// namespace or event-hub scope. It carries the ARM id prefix, resource type and
// connection-string parts so one set of handlers serves both scopes.
type authTarget struct {
	rules      map[string]*authRuleRecord
	idPrefix   string
	typeStr    string
	namespace  string
	entityPath string // "" at namespace scope; the event hub name at hub scope
}

// authResolver resolves the target rule set under h.mu; ok is false when the
// parent (namespace or event hub) does not exist.
type authResolver func() (authTarget, bool)

func (h *Handler) nsAuthTargetLocked(ep ehPath) (authTarget, bool) {
	ns, ok := h.getNS(ep)
	if !ok {
		return authTarget{}, false
	}

	return authTarget{
		rules:     ns.AuthRules,
		idPrefix:  nsIDPrefix(ep),
		typeStr:   providerName + "/Namespaces/AuthorizationRules",
		namespace: ep.namespace,
	}, true
}

func (h *Handler) ehAuthTargetLocked(ep ehPath, eh string) (authTarget, bool) {
	rec, ok := h.eventHubLocked(ep, eh)
	if !ok {
		return authTarget{}, false
	}

	return authTarget{
		rules:      rec.AuthRules,
		idPrefix:   eventHubIDPrefix(ep, eh),
		typeStr:    providerName + "/Namespaces/EventHubs/AuthorizationRules",
		namespace:  ep.namespace,
		entityPath: eh,
	}, true
}

// authRuleDispatch routes .../authorizationRules[/{name}[/{action}]]. rest is
// the path after "authorizationRules"; resolve yields the rule set under lock.
func (h *Handler) authRuleDispatch(w http.ResponseWriter, r *http.Request, rest []string, resolve authResolver) {
	switch {
	case len(rest) == 0:
		h.listAuthRules(w, r, resolve)
	case len(rest) == 1:
		h.serveAuthRuleItem(w, r, resolve, rest[0])
	case len(rest) == namePairLen && eq(rest[1], actionKeys):
		h.listKeys(w, r, resolve, rest[0])
	case len(rest) == namePairLen && eq(rest[1], actionRegen):
		h.regenerateKeys(w, r, resolve, rest[0])
	default:
		notImplemented(w)
	}
}

func (h *Handler) serveAuthRuleItem(w http.ResponseWriter, r *http.Request, resolve authResolver, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createAuthRule(w, r, resolve, name)
	case http.MethodGet:
		h.getAuthRule(w, resolve, name)
	case http.MethodDelete:
		h.deleteAuthRule(w, resolve, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createAuthRule(w http.ResponseWriter, r *http.Request, resolve authResolver, name string) {
	var req createAuthRuleRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	tgt, ok := resolve()
	if !ok {
		h.mu.Unlock()
		writeAuthScopeNotFound(w)

		return
	}

	rec, existed := tgt.rules[name]
	if !existed {
		rec = &authRuleRecord{Name: name, PrimaryKey: generateKey(), SecondaryKey: generateKey()}
		tgt.rules[name] = rec
	}

	rec.Rights = append([]string(nil), req.Properties.Rights...)

	resource := toAuthRuleResource(tgt, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getAuthRule(w http.ResponseWriter, resolve authResolver, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	tgt, ok := resolve()
	if !ok {
		writeAuthScopeNotFound(w)
		return
	}

	rec, ok := tgt.rules[name]
	if !ok {
		writeAuthRuleNotFound(w, name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAuthRuleResource(tgt, rec))
}

func (h *Handler) deleteAuthRule(w http.ResponseWriter, resolve authResolver, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	tgt, ok := resolve()
	if !ok {
		writeAuthScopeNotFound(w)
		return
	}

	if _, ok := tgt.rules[name]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(tgt.rules, name)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listAuthRules(w http.ResponseWriter, r *http.Request, resolve authResolver) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	tgt, ok := resolve()
	if !ok {
		h.mu.RUnlock()
		writeAuthScopeNotFound(w)

		return
	}

	out := make([]any, 0, len(tgt.rules))
	for _, n := range sortedKeys(tgt.rules) {
		out = append(out, toAuthRuleResource(tgt, tgt.rules[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, out))
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, resolve authResolver, name string) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	tgt, ok := resolve()
	if !ok {
		writeAuthScopeNotFound(w)
		return
	}

	rec, ok := tgt.rules[name]
	if !ok {
		writeAuthRuleNotFound(w, name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(tgt, rec))
}

func (h *Handler) regenerateKeys(w http.ResponseWriter, r *http.Request, resolve authResolver, name string) {
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

	tgt, ok := resolve()
	if !ok {
		writeAuthScopeNotFound(w)
		return
	}

	rec, ok := tgt.rules[name]
	if !ok {
		writeAuthRuleNotFound(w, name)
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

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(tgt, rec))
}

func writeAuthScopeNotFound(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule scope not found")
}

func writeAuthRuleNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule not found: "+name)
}

func toAuthRuleResource(tgt authTarget, rec *authRuleRecord) authRuleResource {
	return authRuleResource{
		ID:         tgt.idPrefix + "/authorizationRules/" + rec.Name,
		Name:       rec.Name,
		Type:       tgt.typeStr,
		Properties: authRuleProperties{Rights: rec.Rights},
	}
}

func toAccessKeys(tgt authTarget, rec *authRuleRecord) accessKeys {
	return accessKeys{
		KeyName:                   rec.Name,
		PrimaryKey:                rec.PrimaryKey,
		SecondaryKey:              rec.SecondaryKey,
		PrimaryConnectionString:   connectionString(tgt, rec.Name, rec.PrimaryKey),
		SecondaryConnectionString: connectionString(tgt, rec.Name, rec.SecondaryKey),
	}
}

func connectionString(tgt authTarget, ruleName, key string) string {
	cs := "Endpoint=sb://" + tgt.namespace + ehHost + "/;SharedAccessKeyName=" + ruleName +
		";SharedAccessKey=" + key

	if tgt.entityPath != "" {
		cs += ";EntityPath=" + tgt.entityPath
	}

	return cs
}
