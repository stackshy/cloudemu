// Package managedkafka implements the Google Cloud Managed Service for Apache
// Kafka control plane (managedkafka.googleapis.com/v1) as a server.Handler. Real
// google.golang.org/api/managedkafka/v1 clients and the Terraform google
// provider's google_managed_kafka_cluster / google_managed_kafka_topic resources
// hit this handler unchanged.
//
// Coverage:
//
//	POST   /v1/…/clusters?clusterId=               : CreateCluster (LRO)
//	GET    /v1/…/clusters                          : ListClusters
//	GET    /v1/…/clusters/{c}                      : GetCluster
//	PATCH  /v1/…/clusters/{c}?updateMask=          : UpdateCluster (LRO)
//	DELETE /v1/…/clusters/{c}                      : DeleteCluster (LRO)
//	POST   /v1/…/clusters/{c}/topics?topicId=      : CreateTopic (sync, returns Topic)
//	GET    /v1/…/clusters/{c}/topics[/{t}]         : List/GetTopic
//	PATCH  /v1/…/clusters/{c}/topics/{t}?updateMask= : UpdateTopic (sync, returns Topic)
//	DELETE /v1/…/clusters/{c}/topics/{t}           : DeleteTopic (sync, returns Empty)
//	GET    /v1/…/operations/{op}                   : Operations.Get (shared poller)
//
// Path sharing: /v1/projects/{p}/locations/{l}/clusters[/{c}] is byte-identical
// to GKE's (container/v1) and AlloyDB's cluster paths, and a custom-endpoint
// client sends the emulator's own Host, so URL alone cannot tell them apart. In
// an assembled server (a shared LRO registry is wired) this handler registers
// AHEAD of GKE/AlloyDB and is told which of them shares the path
// (SetClusterSibling). It claims a cluster request only when it is genuinely
// Managed Kafka traffic:
//
//   - a create whose body carries a Kafka-only key (capacityConfig, gcpConfig,
//     …; GKE wraps its body in {"cluster": …} and AlloyDB bodies carry none);
//   - a GKE/AlloyDB-shaped create naming an id this store owns, which is
//     refused 409 rather than silently shadowing one cluster with the other;
//   - an item request for a cluster this store owns;
//   - a PATCH whose body is Kafka-shaped, for a cluster no sibling owns, so a
//     missing Kafka cluster is Kafka's 404 (GKE has no PATCH and would 405);
//   - a list in a project+location where this store owns a cluster and the
//     sibling owns none. When both own clusters there the list is the
//     sibling's: the response shapes differ and the request carries nothing
//     that says which service it is for, so the pre-existing service keeps its
//     list rather than having it replaced;
//   - anything under clusters/{c}/topics (no sibling service has topics).
//
// A create is refused 409 ALREADY_EXISTS when the sibling already owns that id
// in the location, since one of the two clusters would be unreachable. (Real
// GCP keeps the services' namespaces apart by hostname; the emulator cannot.)
//
// Everything else falls through to GKE/AlloyDB. Operation polls are yielded to
// the shared LRO poller. A standalone package server (no shared registry)
// claims every clusters/topics/operations path and answers operation polls
// from its own private registry.
package managedkafka

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/gcp/lro"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

const (
	pathPrefix    = "/v1/projects/"
	projectsSeg   = "projects"
	locationsSeg  = "locations"
	operationsSeg = "operations"
	clustersSeg   = "clusters"
	topicsSeg     = "topics"

	scopeParts = 4 // [projects, {p}, locations, {l}]

	// rest-segment counts after the location scope.
	restCollection = 1 // [clusters]
	restItem       = 2 // [clusters, {c}]
	restTopics     = 3 // [clusters, {c}, topics]
	restTopic      = 4 // [clusters, {c}, topics, {t}]

	maxProbeBytes = 1 << 20

	// allowMissingParam is AlloyDB's upsert flag on PATCH clusters/{c}; Kafka's
	// UpdateCluster has none, so a PATCH carrying it is never Kafka's.
	allowMissingParam = "allowMissing"
)

// kafkaOnlyKeys are top-level Cluster fields only a Managed Kafka body carries
// (a GKE CreateClusterRequest wraps its cluster in {"cluster": …}; an AlloyDB
// Cluster has none of these).
//
//nolint:gochecknoglobals // immutable lookup set
var kafkaOnlyKeys = []string{
	"capacityConfig", "gcpConfig", "rebalanceConfig", "tlsConfig",
	"updateOptions", "brokerCapacityConfig", "kafkaVersion",
}

// ClusterSibling is the view of the service that shares the
// /v1/projects/{p}/locations/{l}/clusters collection in an assembled server
// (GKE or AlloyDB), used to route that collection by ownership. It is wired the
// way the load balancer's BucketLister is: a narrow read-only probe the
// assembling server adapts from the sibling's driver.
type ClusterSibling interface {
	// HasClusters reports whether the sibling serves any cluster for a list of
	// project+location.
	HasClusters(ctx context.Context, project, location string) bool
	// OwnsCluster reports whether the sibling serves cluster id at
	// project+location.
	OwnsCluster(ctx context.Context, project, location, id string) bool
}

// Handler serves managedkafka.googleapis.com v1 requests against a ManagedKafka
// driver.
type Handler struct {
	db mkdriver.ManagedKafka

	// ops records created operations. It is the shared poller's registry in an
	// assembled server, or a private one in a standalone package server.
	ops *lro.Registry

	// poller answers operation polls from ops in a standalone package server;
	// nil once the shared registry is wired (the shared poller answers them).
	poller *lro.Handler

	// sibling is the service sharing the clusters path; nil when none is wired.
	sibling ClusterSibling
}

// New returns a standalone Managed Kafka handler backed by db: it claims every
// Managed Kafka path and answers operation polls itself.
func New(db mkdriver.ManagedKafka) *Handler {
	reg := lro.NewRegistry()

	return &Handler{db: db, ops: reg, poller: lro.New(reg)}
}

// SetOperationRegistry wires the shared LRO poller so created operations are
// resolvable (with their response and metadata) through the full server's
// operations route, and switches Matches to the content+ownership mode an
// assembled server needs.
func (h *Handler) SetOperationRegistry(reg *lro.Registry) {
	h.ops = reg
	h.poller = nil
}

// SetClusterSibling wires the service (GKE or AlloyDB) that serves the same
// clusters collection, so Matches and create route by ownership instead of
// shadowing its clusters.
func (h *Handler) SetClusterSibling(s ClusterSibling) { h.sibling = s }

// route holds the parsed components of a Managed Kafka v1 path.
type route struct {
	project  string
	location string
	resource string // clusters | operations
	cluster  string // cluster id, or operation id for an operations route
	topics   bool   // path is under clusters/{c}/topics
	topic    string // topic id; empty for the topics collection
}

// parseRoute extracts the components of a Managed Kafka v1 path. It recognizes
// only the clusters (with nested topics) and operations resources under a
// locations scope; a custom verb (clusters/{c}:promote, …) is not ours.
func parseRoute(urlPath string) (route, bool) {
	if !strings.HasPrefix(urlPath, pathPrefix) || strings.Contains(urlPath, ":") {
		return route{}, false
	}

	parts := strings.Split(strings.TrimPrefix(urlPath, "/v1/"), "/")
	if len(parts) <= scopeParts || parts[0] != projectsSeg || parts[2] != locationsSeg || slices.Contains(parts, "") {
		return route{}, false
	}

	rt := route{project: parts[1], location: parts[3]}
	if !rt.setRest(parts[scopeParts:]) {
		return route{}, false
	}

	return rt, true
}

// setRest fills the resource components from the segments after the location
// scope: operations[/{op}] or clusters[/{c}[/topics[/{t}]]].
func (rt *route) setRest(rest []string) bool {
	switch {
	case rest[0] == operationsSeg && len(rest) <= restItem:
	case rest[0] == clustersSeg && len(rest) < restTopics:
	case rest[0] == clustersSeg && len(rest) <= restTopic && rest[2] == topicsSeg:
		rt.topics = true
	default:
		return false
	}

	rt.resource = rest[0]

	if len(rest) >= restItem {
		rt.cluster = rest[1]
	}

	if len(rest) == restTopic {
		rt.topic = rest[3]
	}

	return true
}

// Matches claims Managed Kafka paths. See the package doc for how it shares the
// clusters path with GKE and AlloyDB in an assembled server.
func (h *Handler) Matches(r *http.Request) bool {
	if h.poller != nil && h.poller.Matches(r) {
		return true
	}

	rt, ok := parseRoute(r.URL.Path)
	if !ok {
		return false
	}

	standalone := h.poller != nil

	switch {
	case rt.resource == operationsSeg:
		return false
	case standalone || rt.topics:
		return true
	case rt.cluster != "":
		return h.claimsItem(r, &rt)
	case r.Method == http.MethodPost:
		probe := probeBody(r)

		return isKafkaBody(probe) || h.owns(r.Context(), &rt, foreignCreateID(r, probe))
	case r.Method == http.MethodGet:
		all, err := h.db.ListClusters(r.Context(), rt.project, rt.location)

		return err == nil && len(all) > 0 &&
			(h.sibling == nil || !h.sibling.HasClusters(r.Context(), rt.project, rt.location))
	default:
		return false
	}
}

// claimsItem decides an item request (clusters/{c}) in an assembled server: a
// cluster this store owns is Kafka's, one the sibling owns is not, and for an
// id nobody owns a Kafka-shaped PATCH is claimed so it gets Kafka's 404.
func (h *Handler) claimsItem(r *http.Request, rt *route) bool {
	if h.owns(r.Context(), rt, rt.cluster) {
		return true
	}

	if h.siblingOwns(r.Context(), rt, rt.cluster) || r.Method != http.MethodPatch ||
		r.URL.Query().Has(allowMissingParam) {
		return false
	}

	return isKafkaPatchBody(probeBody(r))
}

// owns reports whether this store has cluster id at rt's project+location.
func (h *Handler) owns(ctx context.Context, rt *route, id string) bool {
	if id == "" {
		return false
	}

	_, err := h.db.GetCluster(ctx, rt.project, rt.location, id)

	return err == nil
}

// siblingOwns reports whether the wired sibling serves cluster id at rt's
// project+location.
func (h *Handler) siblingOwns(ctx context.Context, rt *route, id string) bool {
	return h.sibling != nil && h.sibling.OwnsCluster(ctx, rt.project, rt.location, id)
}

// probeBody reads a request body's top-level JSON object and restores the body
// so a fall-through handler still sees the full request. A missing or non-object
// body probes as nil.
func probeBody(r *http.Request) map[string]json.RawMessage {
	if r.Body == nil {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return nil
	}

	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return nil
	}

	return probe
}

// isKafkaBody reports whether a probed body carries a Kafka-only Cluster key.
func isKafkaBody(probe map[string]json.RawMessage) bool {
	for _, k := range kafkaOnlyKeys {
		if _, ok := probe[k]; ok {
			return true
		}
	}

	return false
}

// isKafkaPatchBody reports whether a probed PATCH body is a Kafka Cluster: a
// Kafka-only key, or a labels-only body (a Kafka labels update), which carries
// no GKE {"cluster"/"update": …} wrapper.
func isKafkaPatchBody(probe map[string]json.RawMessage) bool {
	if isKafkaBody(probe) {
		return true
	}

	if _, ok := probe["labels"]; !ok {
		return false
	}

	for k := range probe {
		if k != "labels" && k != "name" {
			return false
		}
	}

	return true
}

// foreignCreateID returns the cluster id a non-Kafka create names: AlloyDB's
// ?clusterId= or GKE's {"cluster":{"name": …}}.
func foreignCreateID(r *http.Request, probe map[string]json.RawMessage) string {
	if id := r.URL.Query().Get(clusterIDParam); id != "" {
		return id
	}

	var gke struct {
		Name string `json:"name"`
	}

	if raw, ok := probe["cluster"]; ok && json.Unmarshal(raw, &gke) == nil {
		return gke.Name
	}

	return ""
}

// ServeHTTP routes on the parsed path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.poller != nil && h.poller.Matches(r) {
		h.poller.ServeHTTP(w, r)
		return
	}

	rt, ok := parseRoute(r.URL.Path)
	if !ok || rt.resource == operationsSeg {
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Managed Kafka path")
		return
	}

	switch {
	case rt.topics && rt.topic == "":
		h.serveTopicCollection(w, r, &rt)
	case rt.topics:
		h.serveTopicItem(w, r, &rt)
	case rt.cluster == "":
		h.serveClusterCollection(w, r, &rt)
	default:
		h.serveClusterItem(w, r, &rt)
	}
}

func (h *Handler) serveClusterCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createCluster(w, r, rt)
	case http.MethodGet:
		h.listClusters(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveClusterItem(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getCluster(w, r, rt)
	case http.MethodPatch:
		h.updateCluster(w, r, rt)
	case http.MethodDelete:
		h.deleteCluster(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveTopicCollection(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodPost:
		h.createTopic(w, r, rt)
	case http.MethodGet:
		h.listTopics(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) serveTopicItem(w http.ResponseWriter, r *http.Request, rt *route) {
	switch r.Method {
	case http.MethodGet:
		h.getTopic(w, r, rt)
	case http.MethodPatch:
		h.updateTopic(w, r, rt)
	case http.MethodDelete:
		h.deleteTopic(w, r, rt)
	default:
		writeMethodNotAllowed(w)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
