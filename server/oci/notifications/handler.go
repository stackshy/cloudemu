// Package notifications implements OCI's Notifications (ONS) REST API against
// a CloudEmu notification driver.
//
// Everything sits under the /20181201 prefix:
//
//	POST/GET             /20181201/topics                                — create, list
//	GET/PUT/DELETE       /20181201/topics/{topicId}                      — get, update, delete
//	POST                 /20181201/topics/{topicId}/messages             — PublishMessage
//	POST                 /20181201/topics/{topicId}/actions/changeCompartment
//	POST/GET             /20181201/subscriptions                         — create, list
//	GET/PUT/DELETE       /20181201/subscriptions/{id}                    — get, update, delete
//	GET                  /20181201/subscriptions/{id}/confirmation       — ConfirmSubscription
//	GET                  /20181201/subscriptions/{id}/unsubscription     — UnsubscribeSubscription
//	POST                 /20181201/subscriptions/{id}/actions/{changeCompartment,resendConfirmation}
//
// Real ONS splits the control plane from the data plane by host, not by
// prefix: PublishMessage goes to the topic's own apiEndpoint. CloudEmu serves
// both on one listener, so every topic reports the requesting origin as its
// apiEndpoint and a publish lands back here.
//
// A subscription is created PENDING and receives nothing until it is
// confirmed. Real ONS mails the confirmation token to the endpoint; the
// emulator has no channel to mail it on, so a PENDING subscription carries the
// token in its response body. DeleteTopic is asynchronous in real ONS and
// answers 204 with an opc-work-request-id; every other mutation here is
// synchronous.
package notifications

import (
	"context"
	"net/http"
	"strings"

	notifprovider "github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// apiVersion is the Notifications API version every ONS path carries.
const apiVersion = "20181201"

// Collections this handler claims.
const (
	segTopics        = "topics"
	segSubscriptions = "subscriptions"
)

// Sub-collections and actions.
const (
	subMessages       = "messages"
	subConfirmation   = "confirmation"
	subUnsubscription = "unsubscription"
	subActions        = "actions"

	actionChangeCompartment = "changeCompartment"
	actionResendConfirm     = "resendConfirmation"
)

// Error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/{collection}/{id}/{sub}/{action}.
const maxPathSegments = 5

// Extras is the OCI-only surface the portable notification driver cannot
// express: a subscription's compartment, tags, metadata and delivery policy,
// the PENDING confirmation handshake, and a topic's lifecycle state, short id
// and etag. *providers/oci/notifications.Mock satisfies it; any driver that
// does not is served 501 for every path this handler claims.
type Extras interface {
	TopicDetails(id string) (notifprovider.TopicDetails, bool)

	CreateSubscription(
		ctx context.Context, spec notifprovider.SubscriptionSpec,
	) (*notifprovider.Subscription, error)
	GetSubscription(ctx context.Context, id string) (*notifprovider.Subscription, error)
	ListOCISubscriptions(ctx context.Context, compartmentID, topicID string) ([]notifprovider.Subscription, error)
	UpdateSubscription(
		ctx context.Context, id string, patch notifprovider.SubscriptionPatch,
	) (*notifprovider.Subscription, error)
	ConfirmSubscription(ctx context.Context, id, token, protocol string) (*notifprovider.ConfirmationResult, error)
	UnsubscribeByToken(ctx context.Context, id, token, protocol string) error
	ResendSubscriptionConfirmation(ctx context.Context, id string) (*notifprovider.Subscription, error)
	ChangeSubscriptionCompartment(ctx context.Context, id, compartmentID string) error

	PublishMessage(
		ctx context.Context, topicID string, spec notifprovider.MessageSpec,
	) (*notifprovider.Message, error)
}

// Handler serves OCI Notifications against a notification driver.
type Handler struct {
	notif  notifdriver.Notification
	extras Extras
	work   *workrequest.Store
}

// New returns a Notifications handler. work records the asynchronous topic
// delete; a nil store leaves that path unserved.
func New(n notifdriver.Notification, work *workrequest.Store) *Handler {
	extras, _ := n.(Extras)

	return &Handler{notif: n, extras: extras, work: work}
}

// route is a parsed Notifications path.
type route struct {
	Collection string
	ID         string
	Sub        string
	Action     string
}

// Matches claims the two ONS collections under /20181201, and nothing else
// sharing that prefix.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	return rt.Collection == segTopics || rt.Collection == segSubscriptions
}

// ServeHTTP routes on collection, then on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed notifications path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired notification driver does not implement OCI Notifications")

		return
	}

	if rt.Collection == segTopics {
		h.serveTopics(w, r, rt)
		return
	}

	h.serveSubscriptions(w, r, rt)
}

// parsePath splits /20181201/{collection}[/{id}[/{sub}[/{action}]]].
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments || parts[0] != apiVersion {
		return route{}, false
	}

	rt := route{Collection: parts[1]}

	if len(parts) > 2 { //nolint:mnd // the id follows the collection
		rt.ID = parts[2]
	}

	if len(parts) > 3 { //nolint:mnd // then the sub-collection
		rt.Sub = parts[3]
	}

	if len(parts) > 4 { //nolint:mnd // then the action on it
		rt.Action = parts[4]
	}

	return rt, true
}

// apiEndpoint is the origin a topic's data plane is reachable at. Real ONS
// hands back a per-cell host; CloudEmu serves the data plane on the listener
// the caller already reached.
func apiEndpoint(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

// refuseDefinedTags rejects a body carrying defined tags, which CloudEmu does
// not model. Echoing them back empty would leave a caller's tags looking
// applied.
func refuseDefinedTags(w http.ResponseWriter, r *http.Request, tags definedTags) bool {
	if len(tags) == 0 {
		return true
	}

	ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
		"definedTags are not modeled by this emulator; use freeformTags")

	return false
}

func notFound(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown notifications path "+r.URL.Path)
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed,
		r.Method+" is not allowed on "+r.URL.Path)
}
