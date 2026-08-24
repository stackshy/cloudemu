// Package notificationhubs implements the Azure Notification Hubs
// (Microsoft.NotificationHubs) ARM REST API as a server.Handler. Real
// github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs
// clients configured with a custom endpoint hit this handler the same way they
// hit management.azure.com, driving the shared notification driver.
//
// Azure models notifications as namespaces that contain notification hubs. The
// notification driver only exposes a flat topic space, so this handler maps:
//
//   - a namespace          → a topic keyed by the namespace name
//   - a notification hub   → a topic keyed by "{namespace}/{hub}"
//
// Namespace SKU, Shared Access authorization rules (+ ListKeys) and data-plane
// device registrations have no cross-cloud analog and are served through the
// notification driver's AzureNotificationHubs optional capability.
//
// Coverage:
//
//	PUT/GET/DELETE .../namespaces/{ns}                                     — Namespaces CRUD
//	GET            .../namespaces                                          — Namespaces List / ListAll
//	POST           .../checkNamespaceAvailability                          — Namespaces.CheckAvailability
//	PUT/GET/DELETE .../namespaces/{ns}/AuthorizationRules/{rule}           — namespace SAS rules
//	POST           .../namespaces/{ns}/AuthorizationRules/{rule}/listKeys  — namespace SAS keys
//	PUT/GET/DELETE .../namespaces/{ns}/notificationHubs/{h}                — hubs CRUD
//	PUT/GET/DELETE .../namespaces/{ns}/notificationHubs/{h}/AuthorizationRules/{rule}
//	POST           .../namespaces/{ns}/notificationHubs/{h}/AuthorizationRules/{rule}/listKeys
package notificationhubs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

const (
	providerName   = "Microsoft.NotificationHubs"
	typeNamespaces = "namespaces"
	subHubs        = "notificationHubs"
)

// Handler serves Microsoft.NotificationHubs ARM requests against a notification
// driver.
type Handler struct {
	notif notifdriver.Notification
}

// New returns an Azure Notification Hubs handler backed by n.
func New(n notifdriver.Notification) *Handler {
	return &Handler{notif: n}
}

// Matches claims ARM URLs targeting Microsoft.NotificationHubs namespaces and
// the subscription-scoped checkNamespaceAvailability action.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok || rp.Provider != providerName {
		return false
	}

	return strings.EqualFold(rp.ResourceType, typeNamespaces) ||
		strings.EqualFold(rp.ResourceType, typeCheckNSAvail)
}

// ServeHTTP routes on the path segments trailing the provider.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	if strings.EqualFold(rp.ResourceType, typeCheckNSAvail) {
		h.checkNamespaceAvailability(w, r, &rp)
		return
	}

	// Segments trailing ".../namespaces": [ns, sub..., subName, action].
	seg := namespaceTail(r.URL.Path)
	h.route(w, r, &rp, seg)
}

// namespaceTail returns the path segments that follow the "namespaces"
// collection segment.
func namespaceTail(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := range parts {
		if strings.EqualFold(parts[i], typeNamespaces) {
			return parts[i+1:]
		}
	}

	return nil
}

// route dispatches on the trailing segments after ".../namespaces".
//
//nolint:cyclop,gocyclo // flat routing table over path shapes
func (h *Handler) route(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, seg []string) {
	switch {
	case len(seg) == 0:
		h.serveNamespaceCollection(w, r, rp)
	case len(seg) == 1:
		h.serveNamespace(w, r, rp)
	case len(seg) == 2 && eq(seg[1], subAuthorizationRules):
		h.listNamespaceAuthRules(w, r, rp)
	case len(seg) == 3 && eq(seg[1], subAuthorizationRules):
		h.serveNamespaceAuthRule(w, r, rp, seg[2])
	case len(seg) == 4 && eq(seg[1], subAuthorizationRules) && eq(seg[3], actionListKeys):
		h.namespaceAuthRuleKeys(w, r, rp, seg[2])
	case len(seg) == 2 && eq(seg[1], subNotificationHubs):
		h.serveHubCollection(w, r, rp)
	case len(seg) == 3 && eq(seg[1], subNotificationHubs):
		h.serveHub(w, r, rp)
	case eq(seg[1], subNotificationHubs):
		h.routeHubSub(w, r, rp, seg)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported Notification Hubs path")
	}
}

// routeHubSub dispatches hub sub-resources: AuthorizationRules and listKeys.
func (h *Handler) routeHubSub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath, seg []string) {
	hub := seg[2]

	switch {
	case len(seg) == 4 && eq(seg[3], subAuthorizationRules):
		h.listHubAuthRules(w, r, rp, hub)
	case len(seg) == 5 && eq(seg[3], subAuthorizationRules):
		h.serveHubAuthRule(w, r, rp, hub, seg[4])
	case len(seg) == 6 && eq(seg[3], subAuthorizationRules) && eq(seg[5], actionListKeys):
		h.hubAuthRuleKeys(w, r, rp, hub, seg[4])
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported Notification Hubs path")
	}
}

func eq(a, b string) bool { return strings.EqualFold(a, b) }

func (h *Handler) serveNamespaceCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listNamespaces(w, r, rp)
}

func (h *Handler) serveNamespace(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateNamespace(w, r, rp)
	case http.MethodGet:
		h.getNamespace(w, r, rp)
	case http.MethodDelete:
		h.deleteNamespace(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveHubCollection(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listHubs(w, r, rp)
}

func (h *Handler) serveHub(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateHub(w, r, rp)
	case http.MethodGet:
		h.getHub(w, r, rp)
	case http.MethodDelete:
		h.deleteHub(w, r, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
}
