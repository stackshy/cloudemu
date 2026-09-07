// Package mq implements the Amazon MQ control-plane API (managed message broker
// for ActiveMQ and RabbitMQ; REST-JSON, awsRestjson1) as a server.Handler.
// Point the real aws-sdk-go-v2/service/mq client (or the `aws mq` CLI, or the
// aws_mq_broker / aws_mq_configuration Terraform resources) at a Server
// registered with this handler and the broker, configuration, user and tagging
// operations work end-to-end against an in-memory driver.
//
// MQ routes by HTTP verb + path under the /v1/ prefix (e.g.
// POST /v1/brokers, GET /v1/brokers/{brokerId}, POST /v1/configurations,
// GET /v1/tags/{arn}); there is no X-Amz-Target header. Matches claims the
// /v1/brokers and /v1/configurations trees, and the /v1/tags paths carrying an
// MQ (:mq:) ARN, so it runs before the S3 catch-all and never shadows a sibling
// service's tag operations (Batch, AppSync and Kafka scope /v1/tags to their
// own ARN markers).
package mq

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// apiPrefix is the version prefix every MQ operation path carries.
const apiPrefix = "/v1/"

// Path roots below the /v1/ prefix.
const (
	rootBrokers        = "brokers"
	rootConfigurations = "configurations"
	rootTags           = "tags"
	subReboot          = "reboot"
	subUsers           = "users"
	subRevisions       = "revisions"
)

// arnMarker scopes the shared /v1/tags root to MQ ARNs.
const arnMarker = ":mq:"

// Path-depth (segment count below a root) markers used when routing.
const (
	depthSubresource = 2
	depthLeaf        = 3
)

// Handler serves Amazon MQ requests against a driver.
type Handler struct {
	mq driver.MQ
}

// New returns an MQ handler backed by d.
func New(d driver.MQ) *Handler {
	return &Handler{mq: d}
}

// Matches claims the MQ path shapes. The /v1/brokers and /v1/configurations
// trees are unique to MQ. The /v1/tags root is shared with other restJson1
// services, so it is claimed only for MQ ARNs; a non-MQ ARN falls through.
func (*Handler) Matches(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		return false
	}

	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		return false
	}

	switch segs[0] {
	case rootBrokers, rootConfigurations:
		return true
	case rootTags:
		return len(segs) >= 2 && strings.Contains(strings.Join(segs[1:], "/"), arnMarker)
	default:
		return false
	}
}

// ServeHTTP dispatches an MQ request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(strings.TrimPrefix(escapedPath(r), apiPrefix))
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	switch segs[0] {
	case rootBrokers:
		h.serveBrokers(w, r, segs[1:])
	case rootConfigurations:
		h.serveConfigurations(w, r, segs[1:])
	case rootTags:
		h.serveTags(w, r, segs[1:])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveBrokers routes the /v1/brokers tree.
func (h *Handler) serveBrokers(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveBrokerCollection(w, r)
	case 1:
		h.serveBrokerItem(w, r, rest[0])
	case depthSubresource:
		h.serveBrokerSubresource(w, r, rest[0], rest[1])
	case depthLeaf:
		h.serveBrokerUserItem(w, r, rest[0], rest[1], rest[2])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveBrokerCollection handles GET /v1/brokers (list) and POST /v1/brokers.
func (h *Handler) serveBrokerCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listBrokers(w, r)
	case http.MethodPost:
		h.createBroker(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveBrokerItem handles the verb-keyed operations on a single broker.
func (h *Handler) serveBrokerItem(w http.ResponseWriter, r *http.Request, brokerID string) {
	switch r.Method {
	case http.MethodGet:
		h.describeBroker(w, r, brokerID)
	case http.MethodPut:
		h.updateBroker(w, r, brokerID)
	case http.MethodDelete:
		h.deleteBroker(w, r, brokerID)
	default:
		methodNotAllowed(w)
	}
}

// serveBrokerSubresource handles /v1/brokers/{id}/reboot and
// /v1/brokers/{id}/users (collection).
func (h *Handler) serveBrokerSubresource(w http.ResponseWriter, r *http.Request, brokerID, sub string) {
	switch sub {
	case subReboot:
		h.rebootBroker(w, r, brokerID)
	case subUsers:
		h.serveUserCollection(w, r, brokerID)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveBrokerUserItem handles /v1/brokers/{id}/users/{username}.
func (h *Handler) serveBrokerUserItem(w http.ResponseWriter, r *http.Request, brokerID, sub, username string) {
	if sub != subUsers {
		notFoundPath(w, r.URL.Path)

		return
	}

	h.serveUserItem(w, r, brokerID, username)
}

// serveConfigurations routes the /v1/configurations tree.
func (h *Handler) serveConfigurations(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveConfigurationCollection(w, r)
	case 1:
		h.serveConfigurationItem(w, r, rest[0])
	case depthLeaf:
		h.serveConfigurationRevision(w, r, rest[0], rest[1], rest[2])
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// serveConfigurationCollection handles GET /v1/configurations (list) and
// POST /v1/configurations.
func (h *Handler) serveConfigurationCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listConfigurations(w, r)
	case http.MethodPost:
		h.createConfiguration(w, r)
	default:
		methodNotAllowed(w)
	}
}

// serveConfigurationItem handles GET/PUT on a single configuration.
func (h *Handler) serveConfigurationItem(w http.ResponseWriter, r *http.Request, configID string) {
	switch r.Method {
	case http.MethodGet:
		h.describeConfiguration(w, r, configID)
	case http.MethodPut:
		h.updateConfiguration(w, r, configID)
	default:
		methodNotAllowed(w)
	}
}

// serveConfigurationRevision handles GET
// /v1/configurations/{id}/revisions/{revision}.
func (h *Handler) serveConfigurationRevision(w http.ResponseWriter, r *http.Request, configID, sub, revision string) {
	if sub != subRevisions || r.Method != http.MethodGet {
		notFoundPath(w, r.URL.Path)

		return
	}

	h.describeConfigurationRevision(w, r, configID, revision)
}

// escapedPath returns the request path preserving percent-encoding so a path
// label that contains a delimiter survives as one segment.
func escapedPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}

	return r.URL.EscapedPath()
}

// splitPath splits a URL path into its non-empty segments, percent-decoding
// each segment so an ARN or name label is delivered whole to a handler.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))

	for _, seg := range raw {
		if dec, err := url.PathUnescape(seg); err == nil {
			out = append(out, dec)
		} else {
			out = append(out, seg)
		}
	}

	return out
}
