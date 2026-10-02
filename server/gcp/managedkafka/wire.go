package managedkafka

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

const (
	clusterTypeURL  = "type.googleapis.com/google.cloud.managedkafka.v1.Cluster"
	emptyTypeURL    = "type.googleapis.com/google.protobuf.Empty"
	opMetaTypeURL   = "type.googleapis.com/google.cloud.managedkafka.v1.OperationMetadata"
	int64Base       = 10
	int64Bits       = 64
	jsonNull        = "null"
	jsonQuote       = '"'
	enumUnspecified = 0
)

// Proto enum name tables, indexed by the enum number (google.cloud.managedkafka.v1).
// Index 0 is the *_UNSPECIFIED value.
//
//nolint:gochecknoglobals // immutable ordinal enum tables
var (
	rebalanceModeNames = []string{"MODE_UNSPECIFIED", "NO_REBALANCE", "AUTO_REBALANCE_ON_SCALE_UP"}
	clusterStateNames  = []string{"STATE_UNSPECIFIED", "CREATING", "ACTIVE", "DELETING", "UPDATING"}

	errEnumValue = errors.New("enum value is neither a name nor a number")
	errEnumRange = errors.New("enum number is out of range")
)

// int64String is a proto3-JSON int64: marshaled as a decimal string, and
// accepted on input as either a string or a bare number (both are legal proto3
// JSON). The Go discovery client tags these fields `json:",string"`.
type int64String int64

// MarshalJSON renders the value as a JSON string.
func (v int64String) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(v), int64Base))
}

// UnmarshalJSON accepts "123" or 123.
func (v *int64String) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)

	n, err := strconv.ParseInt(s, int64Base, int64Bits)
	if err != nil {
		return err
	}

	*v = int64String(n)

	return nil
}

// decodeEnum reads a proto3-JSON enum, which may be the value's name (a JSON
// string, as the discovery client, gcloud and Terraform send) or its number (as
// the GAPIC REST client sends under UseEnumNumbers). A number resolves through
// names; 0 (the *_UNSPECIFIED value) and null decode to "" (unset). An unknown
// number is an error, surfaced as 400 by the body decoder. A name is kept
// verbatim so the provider validates it.
func decodeEnum(b []byte, names []string) (string, error) {
	s := strings.TrimSpace(string(b))
	if s == jsonNull {
		return "", nil
	}

	if s != "" && s[0] == jsonQuote {
		var name string
		err := json.Unmarshal(b, &name)

		return name, err
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return "", fmt.Errorf("%w: %s", errEnumValue, s)
	}

	if n == enumUnspecified {
		return "", nil
	}

	if n < 0 || n >= len(names) {
		return "", fmt.Errorf("%w: %d", errEnumRange, n)
	}

	return names[n], nil
}

// rebalanceMode is RebalanceConfig.mode (name or number on input, name on
// output).
type rebalanceMode string

// UnmarshalJSON accepts the mode's name or number.
func (m *rebalanceMode) UnmarshalJSON(b []byte) error {
	s, err := decodeEnum(b, rebalanceModeNames)
	*m = rebalanceMode(s)

	return err
}

// clusterState is Cluster.state: output only, but a client that round-trips a
// fetched cluster sends it back (the GAPIC client as a number), so it must
// decode either way. Its input value is ignored.
type clusterState string

// UnmarshalJSON accepts the state's name or number.
func (s *clusterState) UnmarshalJSON(b []byte) error {
	v, err := decodeEnum(b, clusterStateNames)
	*s = clusterState(v)

	return err
}

type capacityJSON struct {
	VcpuCount   int64String `json:"vcpuCount,omitempty"`
	MemoryBytes int64String `json:"memoryBytes,omitempty"`
}

type networkConfigJSON struct {
	Subnet string `json:"subnet,omitempty"`
}

type accessConfigJSON struct {
	NetworkConfigs []networkConfigJSON `json:"networkConfigs,omitempty"`
}

type gcpConfigJSON struct {
	AccessConfig *accessConfigJSON `json:"accessConfig,omitempty"`
	KmsKey       string            `json:"kmsKey,omitempty"`
}

type rebalanceJSON struct {
	Mode rebalanceMode `json:"mode,omitempty"`
}

type casConfigJSON struct {
	CaPool string `json:"caPool,omitempty"`
}

type trustConfigJSON struct {
	CasConfigs []casConfigJSON `json:"casConfigs,omitempty"`
}

type tlsConfigJSON struct {
	SslPrincipalMappingRules string           `json:"sslPrincipalMappingRules,omitempty"`
	TrustConfig              *trustConfigJSON `json:"trustConfig,omitempty"`
}

type updateOptionsJSON struct {
	AllowBrokerDownscaleOnClusterUpscale bool `json:"allowBrokerDownscaleOnClusterUpscale,omitempty"`
}

type brokerCapacityJSON struct {
	DiskSizeGib int64String `json:"diskSizeGib,omitempty"`
}

// clusterJSON mirrors the managedkafka v1 Cluster message. Output-only fields
// (name, state, createTime, updateTime, satisfiesPzi/Pzs) are ignored on input.
type clusterJSON struct {
	Name                 string              `json:"name,omitempty"`
	CapacityConfig       *capacityJSON       `json:"capacityConfig,omitempty"`
	GcpConfig            *gcpConfigJSON      `json:"gcpConfig,omitempty"`
	RebalanceConfig      *rebalanceJSON      `json:"rebalanceConfig,omitempty"`
	KafkaVersion         string              `json:"kafkaVersion,omitempty"`
	TLSConfig            *tlsConfigJSON      `json:"tlsConfig,omitempty"`
	UpdateOptions        *updateOptionsJSON  `json:"updateOptions,omitempty"`
	BrokerCapacityConfig *brokerCapacityJSON `json:"brokerCapacityConfig,omitempty"`
	Labels               map[string]string   `json:"labels,omitempty"`
	State                clusterState        `json:"state,omitempty"`
	CreateTime           string              `json:"createTime,omitempty"`
	UpdateTime           string              `json:"updateTime,omitempty"`
	SatisfiesPzi         bool                `json:"satisfiesPzi,omitempty"`
	SatisfiesPzs         bool                `json:"satisfiesPzs,omitempty"`
}

// topicJSON mirrors the managedkafka v1 Topic message (int32 counts are plain
// JSON numbers).
type topicJSON struct {
	Name              string            `json:"name,omitempty"`
	PartitionCount    int32             `json:"partitionCount,omitempty"`
	ReplicationFactor int32             `json:"replicationFactor,omitempty"`
	Configs           map[string]string `json:"configs,omitempty"`
}

// operationJSON mirrors google.longrunning.Operation. Mutating ops complete
// inline, so `done` is always true.
type operationJSON struct {
	Name     string          `json:"name"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// operationMetadataJSON mirrors google.cloud.managedkafka.v1.OperationMetadata.
type operationMetadataJSON struct {
	CreateTime string `json:"createTime,omitempty"`
	EndTime    string `json:"endTime,omitempty"`
	Target     string `json:"target,omitempty"`
	Verb       string `json:"verb,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

// toDriverCluster converts a request body into a driver cluster scoped to rt.
func toDriverCluster(in *clusterJSON, rt *route, id string) *mkdriver.Cluster {
	c := &mkdriver.Cluster{
		Project:      rt.project,
		Location:     rt.location,
		ID:           id,
		Labels:       in.Labels,
		KafkaVersion: in.KafkaVersion,
	}

	if in.CapacityConfig != nil {
		c.VcpuCount = int64(in.CapacityConfig.VcpuCount)
		c.MemoryBytes = int64(in.CapacityConfig.MemoryBytes)
	}

	if in.GcpConfig != nil {
		c.KmsKey = in.GcpConfig.KmsKey

		if in.GcpConfig.AccessConfig != nil {
			for _, nc := range in.GcpConfig.AccessConfig.NetworkConfigs {
				c.Subnets = append(c.Subnets, nc.Subnet)
			}
		}
	}

	if in.RebalanceConfig != nil {
		c.RebalanceMode = string(in.RebalanceConfig.Mode)
	}

	if in.TLSConfig != nil {
		c.TLS = &mkdriver.TLSConfig{SSLPrincipalMappingRules: in.TLSConfig.SslPrincipalMappingRules}

		if in.TLSConfig.TrustConfig != nil {
			for _, cas := range in.TLSConfig.TrustConfig.CasConfigs {
				c.TLS.CAPools = append(c.TLS.CAPools, cas.CaPool)
			}
		}
	}

	if in.UpdateOptions != nil {
		c.AllowBrokerDownscaleOnClusterUpscale = in.UpdateOptions.AllowBrokerDownscaleOnClusterUpscale
	}

	if in.BrokerCapacityConfig != nil {
		c.BrokerDiskSizeGib = int64(in.BrokerCapacityConfig.DiskSizeGib)
	}

	return c
}

// fromDriverCluster renders a driver cluster as managedkafka v1 wire JSON.
func fromDriverCluster(c *mkdriver.Cluster) clusterJSON {
	out := clusterJSON{
		Name: clusterName(c.Project, c.Location, c.ID),
		CapacityConfig: &capacityJSON{
			VcpuCount:   int64String(c.VcpuCount),
			MemoryBytes: int64String(c.MemoryBytes),
		},
		GcpConfig:    &gcpConfigJSON{KmsKey: c.KmsKey, AccessConfig: &accessConfigJSON{}},
		KafkaVersion: c.KafkaVersion,
		Labels:       c.Labels,
		State:        clusterState(c.State),
		CreateTime:   gcprest.FormatTime(c.CreateTime),
		UpdateTime:   gcprest.FormatTime(c.UpdateTime),
		SatisfiesPzi: c.SatisfiesPzi,
		SatisfiesPzs: c.SatisfiesPzs,
	}

	for _, s := range c.Subnets {
		out.GcpConfig.AccessConfig.NetworkConfigs = append(out.GcpConfig.AccessConfig.NetworkConfigs,
			networkConfigJSON{Subnet: s})
	}

	if c.RebalanceMode != "" {
		out.RebalanceConfig = &rebalanceJSON{Mode: rebalanceMode(c.RebalanceMode)}
	}

	if c.TLS != nil {
		out.TLSConfig = &tlsConfigJSON{SslPrincipalMappingRules: c.TLS.SSLPrincipalMappingRules}

		if len(c.TLS.CAPools) > 0 {
			out.TLSConfig.TrustConfig = &trustConfigJSON{}
			for _, p := range c.TLS.CAPools {
				out.TLSConfig.TrustConfig.CasConfigs = append(out.TLSConfig.TrustConfig.CasConfigs, casConfigJSON{CaPool: p})
			}
		}
	}

	if c.AllowBrokerDownscaleOnClusterUpscale {
		out.UpdateOptions = &updateOptionsJSON{AllowBrokerDownscaleOnClusterUpscale: true}
	}

	if c.BrokerDiskSizeGib != 0 {
		out.BrokerCapacityConfig = &brokerCapacityJSON{DiskSizeGib: int64String(c.BrokerDiskSizeGib)}
	}

	return out
}

// toDriverTopic converts a request body into a driver topic scoped to rt.
func toDriverTopic(in *topicJSON, rt *route, id string) *mkdriver.Topic {
	return &mkdriver.Topic{
		Project:           rt.project,
		Location:          rt.location,
		ClusterID:         rt.cluster,
		ID:                id,
		PartitionCount:    in.PartitionCount,
		ReplicationFactor: in.ReplicationFactor,
		Configs:           in.Configs,
	}
}

// fromDriverTopic renders a driver topic as managedkafka v1 wire JSON.
func fromDriverTopic(t *mkdriver.Topic) topicJSON {
	return topicJSON{
		Name:              topicName(t.Project, t.Location, t.ClusterID, t.ID),
		PartitionCount:    t.PartitionCount,
		ReplicationFactor: t.ReplicationFactor,
		Configs:           t.Configs,
	}
}

// writeOperation writes a completed operation whose response is v (typed as
// typeURL) and whose metadata is the driver operation's OperationMetadata, and
// records both with the LRO poller, so a client polling the returned name
// resolves the same done operation.
func (h *Handler) writeOperation(w http.ResponseWriter, op *mkdriver.Operation, v any, typeURL string) {
	resp, err := gcprest.TypedAny(v, typeURL)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	meta, err := gcprest.TypedAny(operationMetadataJSON{
		CreateTime: gcprest.FormatTime(op.CreateTime),
		EndTime:    gcprest.FormatTime(op.EndTime),
		Target:     op.TargetName,
		Verb:       op.Type,
		APIVersion: op.APIVersion,
	}, opMetaTypeURL)
	if err != nil {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}

	h.ops.RegisterWithMetadata(op.Name, resp, meta)

	gcprest.WriteJSON(w, http.StatusOK, operationJSON{Name: op.Name, Metadata: meta, Done: true, Response: resp})
}

// clusterName builds the full cluster resource name.
func clusterName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + clustersSeg + "/" + id
}

// topicName builds the full topic resource name.
func topicName(project, location, clusterID, id string) string {
	return clusterName(project, location, clusterID) + "/" + topicsSeg + "/" + id
}
