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
// Both lifecycles (CreateOrUpdate / Get / List / Delete) run against the same
// driver. Publish/subscribe are not part of the ARM control plane and so are
// not exposed here.
//
// Coverage:
//
//	PUT    .../namespaces/{ns}                          — Namespaces.CreateOrUpdate
//	GET    .../namespaces/{ns}                          — Namespaces.Get
//	DELETE .../namespaces/{ns}                          — Namespaces.BeginDelete (completes inline)
//	GET    .../namespaces                               — Namespaces.List (RG-scoped)
//	PUT    .../namespaces/{ns}/notificationHubs/{h}     — Client.CreateOrUpdate (hub)
//	GET    .../namespaces/{ns}/notificationHubs/{h}     — Client.Get (hub)
//	DELETE .../namespaces/{ns}/notificationHubs/{h}     — Client.Delete (hub)
//	GET    .../namespaces/{ns}/notificationHubs         — Client.List (hubs)
package notificationhubs

import (
	"net/http"
	"strings"

	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
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

// Matches claims ARM URLs targeting Microsoft.NotificationHubs/namespaces. A
// distinct ARM provider name from every other Azure handler, so registration
// order is unconstrained; registered before the BlobStorage fallback.
func (*Handler) Matches(r *http.Request) bool {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rp.Provider == providerName && strings.EqualFold(rp.ResourceType, typeNamespaces)
}

// ServeHTTP routes on the parsed path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp, ok := azurearm.ParsePath(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed ARM path")
		return
	}

	switch {
	case rp.ResourceName == "":
		// .../namespaces (RG-scoped namespace list)
		h.serveNamespaceCollection(w, r, &rp)
	case rp.SubResource == "":
		// .../namespaces/{ns}
		h.serveNamespace(w, r, &rp)
	case strings.EqualFold(rp.SubResource, subHubs) && rp.SubResourceName == "":
		// .../namespaces/{ns}/notificationHubs (hub list)
		h.serveHubCollection(w, r, &rp)
	case strings.EqualFold(rp.SubResource, subHubs):
		// .../namespaces/{ns}/notificationHubs/{hub}
		h.serveHub(w, r, &rp)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported Notification Hubs path")
	}
}

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
