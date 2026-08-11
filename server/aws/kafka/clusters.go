package kafka

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// subStorage is the cluster/broker storage sub-resource path segment.
const subStorage = "storage"

// routeV1Clusters dispatches /v1/clusters and its sub-paths. rest is the path
// below "clusters".
func (h *Handler) routeV1Clusters(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createCluster(w, r)
		case http.MethodGet:
			h.listClusters(w, r, false)
		default:
			methodNotAllowed(w)
		}

		return
	}

	arn := rest[0]

	if len(rest) == 1 {
		h.serveClusterByArn(w, r, arn)

		return
	}

	h.serveClusterSubresource(w, r, arn, rest[1:])
}

// serveClusterByArn handles GET (describe) and DELETE on /v1/clusters/{arn}.
func (h *Handler) serveClusterByArn(w http.ResponseWriter, r *http.Request, arn string) {
	switch r.Method {
	case http.MethodGet:
		h.describeCluster(w, r, arn)
	case http.MethodDelete:
		h.deleteCluster(w, r, arn)
	default:
		methodNotAllowed(w)
	}
}

// serveClusterSubresource dispatches /v1/clusters/{arn}/<sub>. The fully
// implemented sub-path is bootstrap-brokers; the rest are phase-1 stubs whose
// routes are wired so later phases only implement the driver.
//
//nolint:gocyclo // one arm per cluster sub-resource; large by MSK API design.
func (h *Handler) serveClusterSubresource(w http.ResponseWriter, r *http.Request, arn string, rest []string) {
	switch rest[0] {
	case "bootstrap-brokers":
		h.getBootstrapBrokers(w, r, arn)
	case "nodes":
		h.routeClusterNodes(w, r, arn, rest[1:])
	case rootOperations:
		h.listClusterOperations(w, r, arn, false)
	case subStorage:
		h.updateStorage(w, r, arn)
	case "configuration":
		h.updateClusterConfiguration(w, r, arn)
	case "version":
		h.updateClusterKafkaVersion(w, r, arn)
	case "connectivity":
		h.updateConnectivity(w, r, arn)
	case "monitoring":
		h.updateMonitoring(w, r, arn)
	case "security":
		h.updateSecurity(w, r, arn)
	case "rebalancing":
		h.updateRebalancing(w, r, arn)
	case "reboot-broker":
		h.rebootBroker(w, r, arn)
	case "topics":
		h.routeTopics(w, r, arn, rest[1:])
	case "scram-secrets":
		h.routeScramSecrets(w, r, arn)
	case "policy":
		h.routeClusterPolicy(w, r, arn)
	case "client-vpc-connections":
		h.listClientVpcConnections(w, r, arn)
	case "client-vpc-connection":
		h.rejectClientVpcConnection(w, r, arn)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// routeClusterNodes handles /v1/clusters/{arn}/nodes and .../nodes/{count,storage,type}.
func (h *Handler) routeClusterNodes(w http.ResponseWriter, r *http.Request, arn string, rest []string) {
	if len(rest) == 0 {
		h.listNodes(w, r, arn)

		return
	}

	switch rest[0] {
	case "count":
		h.updateBrokerCount(w, r, arn)
	case subStorage:
		h.updateBrokerStorage(w, r, arn)
	case "type":
		h.updateBrokerType(w, r, arn)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// createCluster handles POST /v1/clusters (fully implemented).
func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var req createClusterRequest

	body, ok := decodeBody(w, r, &req)
	if !ok {
		return
	}

	out, err := h.k.CreateCluster(r.Context(), driver.CreateClusterInput{
		ClusterName:         req.ClusterName,
		KafkaVersion:        req.KafkaVersion,
		NumberOfBrokerNodes: req.NumberOfBrokerNodes,
		BrokerNodeGroupInfo: bngToDriver(req.BrokerNodeGroupInfo),
		StorageMode:         req.StorageMode,
		EnhancedMonitoring:  req.EnhancedMonitoring,
		Tags:                req.Tags,
		RawOptions:          rawFieldsExcept(body, modeledClusterFields()),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"clusterArn":  out.ClusterARN,
		"clusterName": out.ClusterName,
		"state":       out.State,
	})
}

// describeCluster handles GET /v1/clusters/{arn} (fully implemented).
func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DescribeCluster(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"clusterInfo": marshalWire(clusterToWire(out))})
}

// listClusters handles GET /v1/clusters (fully implemented).
func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request, _ bool) {
	prefix := r.URL.Query().Get("clusterNameFilter")

	list, next, err := h.k.ListClusters(r.Context(), prefix, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	infos := make([]map[string]json.RawMessage, 0, len(list))
	for i := range list {
		infos = append(infos, clusterToWire(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"clusterInfoList": infos}, next))
}

// deleteCluster handles DELETE /v1/clusters/{arn} (fully implemented).
func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, arn string) {
	currentVersion := r.URL.Query().Get("currentVersion")

	arnOut, state, err := h.k.DeleteCluster(r.Context(), arn, currentVersion)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"clusterArn": arnOut, "state": state})
}

// getBootstrapBrokers handles GET /v1/clusters/{arn}/bootstrap-brokers.
func (h *Handler) getBootstrapBrokers(w http.ResponseWriter, r *http.Request, arn string) {
	brokers, err := h.k.GetBootstrapBrokers(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make(map[string]any, len(brokers))
	for k, v := range brokers {
		out[k] = v
	}

	writeJSON(w, out)
}

// marshalWire marshals a wire map to a json.RawMessage.
func marshalWire(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}

	return b
}
