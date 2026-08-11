package kafka

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// createClusterV2Request is the CreateClusterV2 request body. clusterName and
// tags are promoted; provisioned/serverless round-trip raw (the driver models
// provisioned fields and carries serverless verbatim).
type createClusterV2Request struct {
	ClusterName string            `json:"clusterName"`
	Tags        map[string]string `json:"tags"`
	Provisioned json.RawMessage   `json:"provisioned"`
	Serverless  json.RawMessage   `json:"serverless"`
}

// provisionedRequest is the modeled subset of the v2 "provisioned" block.
type provisionedRequest struct {
	KafkaVersion        string          `json:"kafkaVersion"`
	NumberOfBrokerNodes int32           `json:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo json.RawMessage `json:"brokerNodeGroupInfo"`
	StorageMode         string          `json:"storageMode"`
	EnhancedMonitoring  string          `json:"enhancedMonitoring"`
}

// modeledProvisionedFields lists v2 provisioned keys promoted to typed fields.
func modeledProvisionedFields() map[string]struct{} {
	return map[string]struct{}{
		"kafkaVersion": {}, "numberOfBrokerNodes": {}, "brokerNodeGroupInfo": {},
		"storageMode": {}, "enhancedMonitoring": {},
	}
}

// routeV2Clusters dispatches /api/v2/clusters and its sub-paths.
func (h *Handler) routeV2Clusters(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createClusterV2(w, r)
		case http.MethodGet:
			h.listClustersV2(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if len(rest) == 1 {
		if r.Method == http.MethodGet {
			h.describeClusterV2(w, r, rest[0])

			return
		}

		methodNotAllowed(w)

		return
	}

	if rest[1] == "operations" {
		h.listClusterOperations(w, r, rest[0], true)

		return
	}

	notFoundPath(w, r.URL.Path)
}

func (h *Handler) createClusterV2(w http.ResponseWriter, r *http.Request) {
	var req createClusterV2Request

	body, ok := decodeBody(w, r, &req)
	if !ok {
		return
	}

	in := driver.CreateClusterV2Input{
		ClusterName: req.ClusterName,
		Tags:        req.Tags,
		Serverless:  req.Serverless,
		RawOptions:  rawFieldsExcept(body, modeledClusterV2Fields()),
	}

	if len(req.Provisioned) > 0 {
		in.Provisioned = provisionedToDriver(req.Provisioned)
	}

	out, err := h.k.CreateClusterV2(r.Context(), in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"clusterArn":  out.ClusterARN,
		"clusterName": out.ClusterName,
		"clusterType": out.ClusterType,
		"state":       out.State,
	})
}

// modeledClusterV2Fields lists top-level v2 keys carried as typed input.
func modeledClusterV2Fields() map[string]struct{} {
	return map[string]struct{}{
		"clusterName": {}, "tags": {}, "provisioned": {}, "serverless": {},
	}
}

// provisionedToDriver converts the raw v2 provisioned block into the driver's
// CreateClusterInput, carrying unmodeled sub-blocks as raw options.
func provisionedToDriver(raw json.RawMessage) *driver.CreateClusterInput {
	var p provisionedRequest
	if err := json.Unmarshal(raw, &p); err != nil {
		return &driver.CreateClusterInput{}
	}

	return &driver.CreateClusterInput{
		KafkaVersion:        p.KafkaVersion,
		NumberOfBrokerNodes: p.NumberOfBrokerNodes,
		BrokerNodeGroupInfo: bngToDriver(p.BrokerNodeGroupInfo),
		StorageMode:         p.StorageMode,
		EnhancedMonitoring:  p.EnhancedMonitoring,
		RawOptions:          rawFieldsExcept(raw, modeledProvisionedFields()),
	}
}

func (h *Handler) describeClusterV2(w http.ResponseWriter, r *http.Request, arn string) {
	out, err := h.k.DescribeClusterV2(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]json.RawMessage{"clusterInfo": marshalWire(clusterToWireV2(out))})
}

func (h *Handler) listClustersV2(w http.ResponseWriter, r *http.Request) {
	list, next, err := h.k.ListClustersV2(r.Context(),
		r.URL.Query().Get("clusterNameFilter"), r.URL.Query().Get("clusterTypeFilter"), pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	infos := make([]map[string]json.RawMessage, 0, len(list))
	for i := range list {
		infos = append(infos, clusterToWireV2(&list[i]))
	}

	writeJSON(w, withNext(map[string]any{"clusterInfoList": infos}, next))
}
