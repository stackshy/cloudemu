// Package providers implements the Azure Resource Manager resource-providers
// endpoints: the subscription-scoped list, a single provider Get, and the
// register / unregister actions.
//
//	GET  /subscriptions/{sub}/providers
//	GET  /subscriptions/{sub}/providers/{namespace}
//	POST /subscriptions/{sub}/providers/{namespace}/register
//	POST /subscriptions/{sub}/providers/{namespace}/unregister
//
// CLIs and IaC tools (Terraform, az, the SDKs) call this surface at startup to
// discover which resource providers a subscription can use and to register the
// ones a deployment needs. Without it those tools fail before any resource work
// begins, or the register step returns a blob-storage error.
//
// The emulator holds an in-memory registration state per namespace, seeded with
// the providers cloudemu serves (a representative resourceTypes list each). A
// register flips the stored state straight to Registered and unregister back to
// NotRegistered — real Azure transitions Registering→Registered asynchronously,
// but a synchronous terminal state is a documented emulator simplification that
// keeps the poller-free SDK calls (Register/Unregister return the provider
// directly) consistent. State is per-server-instance and is not part of the
// snapshot surface, matching the other self-contained Azure handlers.
package providers

import (
	"net/http"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	stateRegistered    = "Registered"
	stateNotRegistered = "NotRegistered"
	registrationPolicy = "RegistrationRequired"

	actionRegister   = "register"
	actionUnregister = "unregister"

	defaultAPIVersion = "2021-04-01"

	partsSub    = 2 // subscriptions/{sub}
	partsList   = 3 // subscriptions/{sub}/providers
	partsGet    = 4 // subscriptions/{sub}/providers/{namespace}
	partsAction = 5 // subscriptions/{sub}/providers/{namespace}/{action}
)

// kind classifies a matched request path.
type kind int

const (
	kindNone kind = iota
	kindList
	kindGet
	kindRegister
	kindUnregister
)

// resourceType is one entry in a provider's resourceTypes list.
type resourceType struct {
	name      string
	locations []string
}

// providerEntry holds a single resource provider's mutable registration state
// and its static resource-type catalog.
type providerEntry struct {
	namespace     string
	resourceTypes []resourceType
	state         string
}

// Handler serves the resource-providers list / get / register / unregister
// endpoints. It owns its registration state behind mu.
type Handler struct {
	mu    sync.RWMutex
	order []string // lowercased keys, stable list order
	byKey map[string]*providerEntry
}

// New returns a providers handler seeded with the namespaces cloudemu emulates.
func New() *Handler {
	entries := defaultProviders()

	h := &Handler{
		order: make([]string, 0, len(entries)),
		byKey: make(map[string]*providerEntry, len(entries)),
	}

	for i := range entries {
		e := entries[i]
		key := strings.ToLower(e.namespace)
		h.order = append(h.order, key)
		h.byKey[key] = &e
	}

	return h
}

// route classifies urlPath (and method) into one of the served shapes.
func route(method, urlPath string) (k kind, namespace string) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < partsList || parts[0] != "subscriptions" || parts[2] != "providers" {
		return kindNone, ""
	}

	switch len(parts) {
	case partsList:
		if method == http.MethodGet {
			return kindList, ""
		}
	case partsGet:
		if method == http.MethodGet {
			return kindGet, parts[3]
		}
	case partsAction:
		return actionKind(method, parts[3], parts[4])
	}

	return kindNone, ""
}

// actionKind resolves the register / unregister POST actions.
func actionKind(method, namespace, action string) (k kind, ns string) {
	if method != http.MethodPost {
		return kindNone, ""
	}

	switch strings.ToLower(action) {
	case actionRegister:
		return kindRegister, namespace
	case actionUnregister:
		return kindUnregister, namespace
	}

	return kindNone, ""
}

// Matches claims the bare providers list, a single-namespace get, and the
// register / unregister actions. Deeper provider paths
// (/providers/{namespace}/{resourceType}/...) belong to the service handlers.
func (*Handler) Matches(r *http.Request) bool {
	k, _ := route(r.Method, r.URL.Path)

	return k != kindNone
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k, namespace := route(r.Method, r.URL.Path)
	sub := subscriptionID(r.URL.Path)

	switch k {
	case kindList:
		h.serveList(w, sub)
	case kindGet:
		h.serveGet(w, sub, namespace)
	case kindRegister:
		h.serveSetState(w, sub, namespace, stateRegistered)
	case kindUnregister:
		h.serveSetState(w, sub, namespace, stateNotRegistered)
	case kindNone:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown providers path: "+r.URL.Path)
	}
}

func (h *Handler) serveList(w http.ResponseWriter, sub string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	value := make([]map[string]any, 0, len(h.order))
	for _, key := range h.order {
		value = append(value, render(sub, h.byKey[key]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

func (h *Handler) serveGet(w http.ResponseWriter, sub, namespace string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	e, ok := h.byKey[strings.ToLower(namespace)]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceNamespace",
			"The resource namespace '"+namespace+"' is invalid.")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, render(sub, e))
}

func (h *Handler) serveSetState(w http.ResponseWriter, sub, namespace, state string) {
	h.mu.Lock()

	e, ok := h.byKey[strings.ToLower(namespace)]
	if !ok {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "InvalidResourceNamespace",
			"The resource namespace '"+namespace+"' is invalid.")

		return
	}

	e.state = state
	body := render(sub, e)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, body)
}

// render builds the ARM Provider JSON object for one entry under subscription
// sub. Callers hold h.mu.
func render(sub string, e *providerEntry) map[string]any {
	types := make([]map[string]any, 0, len(e.resourceTypes))
	for _, rt := range e.resourceTypes {
		types = append(types, map[string]any{
			"resourceType": rt.name,
			"locations":    rt.locations,
			"apiVersions":  []string{defaultAPIVersion},
		})
	}

	return map[string]any{
		"id":                 "/subscriptions/" + sub + "/providers/" + e.namespace,
		"namespace":          e.namespace,
		"registrationState":  e.state,
		"registrationPolicy": registrationPolicy,
		"resourceTypes":      types,
	}
}

// subscriptionID pulls the {sub} segment out of an ARM providers path.
func subscriptionID(urlPath string) string {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) >= partsSub {
		return parts[1]
	}

	return ""
}
