package servicebus

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// maxAuthRulesPerScope is the number of SAS authorization rules Service Bus
// allows on one namespace, queue or topic.
const maxAuthRulesPerScope = 12

const (
	rightListen = "Listen"
	rightSend   = "Send"
	rightManage = "Manage"
)

// authTarget is the resolved holder of a set of authorization rules, at
// namespace, queue or topic scope. It carries the ARM id prefix, resource type
// and connection-string parts so one set of handlers serves every scope.
type authTarget struct {
	rules      map[string]*authRuleRecord
	idPrefix   string
	typeStr    string
	location   string
	namespace  string
	entityPath string // "" at namespace scope; the queue or topic name otherwise
}

// authResolver resolves the target rule set under h.mu; ok is false when the
// parent (namespace, queue or topic) does not exist.
type authResolver func() (authTarget, bool)

func nsIDPrefix(sp sbPath) string {
	return azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace)
}

func (h *Handler) nsAuthTargetLocked(sp sbPath) (authTarget, bool) {
	ns, ok := h.getNS(sp)
	if !ok {
		return authTarget{}, false
	}

	return authTarget{
		rules:     ns.AuthRules,
		idPrefix:  nsIDPrefix(sp),
		typeStr:   providerName + "/Namespaces/AuthorizationRules",
		location:  ns.Location,
		namespace: sp.namespace,
	}, true
}

func (h *Handler) queueAuthTargetLocked(sp sbPath, queue string) (authTarget, bool) {
	ns, ok := h.getNS(sp)
	if !ok {
		return authTarget{}, false
	}

	q, ok := ns.Queues[queue]
	if !ok {
		return authTarget{}, false
	}

	return authTarget{
		rules:      q.AuthRules,
		idPrefix:   nsIDPrefix(sp) + "/queues/" + q.Name,
		typeStr:    providerName + "/Namespaces/Queues/AuthorizationRules",
		location:   ns.Location,
		namespace:  sp.namespace,
		entityPath: q.Name,
	}, true
}

func (h *Handler) topicAuthTargetLocked(sp sbPath, topic string) (authTarget, bool) {
	ns, ok := h.getNS(sp)
	if !ok {
		return authTarget{}, false
	}

	t, ok := ns.Topics[topic]
	if !ok {
		return authTarget{}, false
	}

	return authTarget{
		rules:      t.AuthRules,
		idPrefix:   nsIDPrefix(sp) + "/topics/" + t.Name,
		typeStr:    providerName + "/Namespaces/Topics/AuthorizationRules",
		location:   ns.Location,
		namespace:  sp.namespace,
		entityPath: t.Name,
	}, true
}

// serveAuthRule dispatches the namespace-level .../authorizationRules subtree.
func (h *Handler) serveAuthRule(w http.ResponseWriter, r *http.Request, sp sbPath) {
	h.authRuleDispatch(w, r, sp.segs[1:], func() (authTarget, bool) {
		return h.nsAuthTargetLocked(sp)
	})
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

// validateRights mirrors Service Bus: rights is a non-empty subset of
// Listen/Send/Manage, and Manage requires both Listen and Send.
func validateRights(rights []string) string {
	if len(rights) == 0 {
		return "at least one access right is required"
	}

	have := map[string]bool{}

	for _, r := range rights {
		switch {
		case eq(r, rightListen):
			have[rightListen] = true
		case eq(r, rightSend):
			have[rightSend] = true
		case eq(r, rightManage):
			have[rightManage] = true
		default:
			return "invalid access right: " + r
		}
	}

	if have[rightManage] && (!have[rightListen] || !have[rightSend]) {
		return "Manage right requires Listen and Send rights"
	}

	return ""
}

func (h *Handler) createAuthRule(w http.ResponseWriter, r *http.Request, resolve authResolver, name string) {
	var req createAuthRuleRequest
	if !decodeBody(w, r, &req) {
		return
	}

	if msg := validateRights(req.Properties.Rights); msg != "" {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", msg)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	tgt, ok := resolve()
	if !ok {
		writeAuthScopeNotFound(w)
		return
	}

	rec, existed := tgt.rules[name]
	if !existed {
		if len(tgt.rules) >= maxAuthRulesPerScope {
			azurearm.WriteError(w, http.StatusBadRequest, "BadRequest",
				"the maximum number of authorization rules has been reached")

			return
		}

		rec = &authRuleRecord{Name: name, PrimaryKey: generateKey(), SecondaryKey: generateKey()}
		tgt.rules[name] = rec
	}

	rec.Rights = append([]string(nil), req.Properties.Rights...)

	azurearm.WriteJSON(w, http.StatusOK, toAuthRuleResource(&tgt, rec))
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

	azurearm.WriteJSON(w, http.StatusOK, toAuthRuleResource(&tgt, rec))
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
		out = append(out, toAuthRuleResource(&tgt, tgt.rules[n]))
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

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(&tgt, rec))
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

	if !eq(req.KeyType, "PrimaryKey") && !eq(req.KeyType, "SecondaryKey") {
		azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", "invalid keyType: "+req.KeyType)
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

	azurearm.WriteJSON(w, http.StatusOK, toAccessKeys(&tgt, rec))
}

func writeAuthScopeNotFound(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule scope not found")
}

func writeAuthRuleNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "authorization rule not found: "+name)
}

func toAuthRuleResource(tgt *authTarget, rec *authRuleRecord) authRuleResource {
	return authRuleResource{
		ID:         tgt.idPrefix + "/authorizationRules/" + rec.Name,
		Name:       rec.Name,
		Type:       tgt.typeStr,
		Location:   tgt.location,
		Properties: authRuleProperties{Rights: append([]string(nil), rec.Rights...)},
	}
}

func toAccessKeys(tgt *authTarget, rec *authRuleRecord) accessKeys {
	return accessKeys{
		KeyName:                   rec.Name,
		PrimaryKey:                rec.PrimaryKey,
		SecondaryKey:              rec.SecondaryKey,
		PrimaryConnectionString:   connectionString(tgt, rec.Name, rec.PrimaryKey),
		SecondaryConnectionString: connectionString(tgt, rec.Name, rec.SecondaryKey),
	}
}

func connectionString(tgt *authTarget, ruleName, key string) string {
	cs := "Endpoint=sb://" + tgt.namespace + sbHost + "/;SharedAccessKeyName=" + ruleName +
		";SharedAccessKey=" + key

	if tgt.entityPath != "" {
		cs += ";EntityPath=" + tgt.entityPath
	}

	return cs
}
