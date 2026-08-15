package kafka

import (
	"net/http"
)

// routeV1 dispatches the /v1/* subtree.
func (h *Handler) routeV1(w http.ResponseWriter, r *http.Request, segs []string) {
	switch segs[0] {
	case rootClusters:
		h.routeV1Clusters(w, r, segs[1:])
	case "configurations":
		h.routeConfigurations(w, r, segs[1:])
	case rootOperations:
		h.routeOperations(w, r, segs[1:], false)
	case "vpc-connection":
		h.routeVpcConnection(w, r, segs[1:])
	case "vpc-connections":
		h.routeVpcConnections(w, r, segs[1:])
	case "tags":
		h.routeTags(w, r, segs[1:])
	case "kafka-versions":
		h.listKafkaVersions(w, r)
	case "compatible-kafka-versions":
		h.getCompatibleKafkaVersions(w, r)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// routeV2 dispatches the /api/v2/* subtree.
func (h *Handler) routeV2(w http.ResponseWriter, r *http.Request, segs []string) {
	switch segs[0] {
	case rootClusters:
		h.routeV2Clusters(w, r, segs[1:])
	case rootOperations:
		h.routeOperations(w, r, segs[1:], true)
	default:
		notFoundPath(w, r.URL.Path)
	}
}

// routeReplication dispatches the /replication/v1/* subtree.
func (h *Handler) routeReplication(w http.ResponseWriter, r *http.Request, segs []string) {
	if segs[0] != "replicators" {
		notFoundPath(w, r.URL.Path)

		return
	}

	h.routeReplicators(w, r, segs[1:])
}
