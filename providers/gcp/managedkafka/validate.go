package managedkafka

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

// Real-service limits, as documented on the google.golang.org/api/managedkafka/v1
// CapacityConfig / AccessConfig / ClustersCreateCall types.
const (
	minVcpuCount = 3

	gib = int64(1) << 30

	// minBytesPerVcpu / maxBytesPerVcpu bound the vCPU:GiB ratio to 1:1..1:8.
	minBytesPerVcpu = 1 * gib
	maxBytesPerVcpu = 8 * gib

	minNetworkConfigs = 1
	maxNetworkConfigs = 10

	// maxCAPools is the trustConfig.casConfigs limit.
	maxCAPools = 10

	// minBrokerDiskGib is brokerCapacityConfig.diskSizeGib's documented minimum.
	minBrokerDiskGib = 100

	// maxTopicIDLen is Apache Kafka's own topic-name length limit.
	maxTopicIDLen = 249

	maskAll = "*"

	pathCapacity  = "capacityConfig"
	pathRebalance = "rebalanceConfig"
	pathLabels    = "labels"
	pathName      = "name"
	pathVersion   = "kafkaVersion"
	pathTLS       = "tlsConfig"
	pathUpdateOps = "updateOptions"
	pathBroker    = "brokerCapacityConfig"

	rebalanceUnspecified = "MODE_UNSPECIFIED"
	rebalanceNone        = "NO_REBALANCE"
	rebalanceOnScaleUp   = "AUTO_REBALANCE_ON_SCALE_UP"
	subnetPathSegments   = 6 // projects/{p}/regions/{r}/subnetworks/{s}
	subnetProjectsIdx    = 0
	subnetRegionsIdx     = 2
	subnetSubnetworksIdx = 4
)

var (
	// clusterIDPattern is the RFC 1035 label the real API enforces (1-63 chars).
	clusterIDPattern = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

	// topicIDPattern is Apache Kafka's legal topic-name alphabet.
	topicIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

	// caPoolPattern is a CA Service pool name; it need not share the cluster's
	// project or location.
	caPoolPattern = regexp.MustCompile(`^projects/[^/]+/locations/[^/]+/caPools/[^/]+$`)
)

// applyClusterDefaults fills the fields the real API defaults when a create (or
// a masked update) leaves them unset: rebalanceConfig.mode NO_REBALANCE and
// kafkaVersion 3.7.x.
func applyClusterDefaults(c *mkdriver.Cluster) {
	if c.RebalanceMode == "" || c.RebalanceMode == rebalanceUnspecified {
		c.RebalanceMode = rebalanceNone
	}

	if c.KafkaVersion == "" {
		c.KafkaVersion = defaultKafkaVersion
	}
}

// validateClusterID enforces the clusterId format.
func validateClusterID(id string) error {
	if id == "" {
		return cerrors.New(cerrors.InvalidArgument, "cluster_id is required")
	}

	if !clusterIDPattern.MatchString(id) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"cluster_id %q must be 1-63 characters and match [a-z]([-a-z0-9]*[a-z0-9])?", id)
	}

	return nil
}

// validateTopicID enforces the topicId format.
func validateTopicID(id string) error {
	if id == "" {
		return cerrors.New(cerrors.InvalidArgument, "topic_id is required")
	}

	if len(id) > maxTopicIDLen || !topicIDPattern.MatchString(id) || id == "." || id == ".." {
		return cerrors.Newf(cerrors.InvalidArgument, "topic_id %q is not a valid Kafka topic name", id)
	}

	return nil
}

// validateCluster checks the cluster configuration: capacity, network access
// (subnets in the cluster's region), the rebalance mode enum, TLS trust config
// and per-broker disk.
func validateCluster(c *mkdriver.Cluster) error {
	if err := validateCapacity(c.VcpuCount, c.MemoryBytes); err != nil {
		return err
	}

	if err := validateSubnets(c.Subnets, c.Location); err != nil {
		return err
	}

	if err := validateTLS(c.TLS); err != nil {
		return err
	}

	if c.BrokerDiskSizeGib != 0 && c.BrokerDiskSizeGib < minBrokerDiskGib {
		return cerrors.Newf(cerrors.InvalidArgument,
			"broker_capacity_config.disk_size_gib must be at least %d, got %d", minBrokerDiskGib, c.BrokerDiskSizeGib)
	}

	switch c.RebalanceMode {
	case rebalanceNone, rebalanceOnScaleUp:
		return nil
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "rebalance_config.mode %q is not a valid mode", c.RebalanceMode)
	}
}

// validateTLS requires at most maxCAPools trust-config CA pools, each a CA
// Service pool name.
func validateTLS(tls *mkdriver.TLSConfig) error {
	if tls == nil {
		return nil
	}

	if len(tls.CAPools) > maxCAPools {
		return cerrors.Newf(cerrors.InvalidArgument,
			"tls_config.trust_config.cas_configs must contain at most %d entries, got %d", maxCAPools, len(tls.CAPools))
	}

	for _, p := range tls.CAPools {
		if !caPoolPattern.MatchString(p) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"cas_configs.ca_pool %q must be projects/{project}/locations/{location}/caPools/{ca_pool}", p)
		}
	}

	return nil
}

// validateCapacity enforces vcpuCount >= 3 and 1 GiB..8 GiB of memory per vCPU
// (inclusive).
func validateCapacity(vcpu, memory int64) error {
	if vcpu < minVcpuCount {
		return cerrors.Newf(cerrors.InvalidArgument,
			"capacity_config.vcpu_count must be at least %d, got %d", minVcpuCount, vcpu)
	}

	if vcpu > math.MaxInt64/maxBytesPerVcpu {
		return cerrors.Newf(cerrors.InvalidArgument, "capacity_config.vcpu_count %d is too large", vcpu)
	}

	if memory < vcpu*minBytesPerVcpu || memory > vcpu*maxBytesPerVcpu {
		return cerrors.Newf(cerrors.InvalidArgument,
			"capacity_config.memory_bytes must be between 1 GiB and 8 GiB per vCPU (%d..%d for %d vCPUs), got %d",
			vcpu*minBytesPerVcpu, vcpu*maxBytesPerVcpu, vcpu, memory)
	}

	return nil
}

// validateSubnets requires 1..10 network configs, each naming a subnet as
// projects/{project}/regions/{region}/subnetworks/{subnet} in the cluster's
// region (the project may differ), as the real API requires.
func validateSubnets(subnets []string, location string) error {
	if len(subnets) < minNetworkConfigs || len(subnets) > maxNetworkConfigs {
		return cerrors.Newf(cerrors.InvalidArgument,
			"gcp_config.access_config.network_configs must contain %d to %d entries, got %d",
			minNetworkConfigs, maxNetworkConfigs, len(subnets))
	}

	for _, s := range subnets {
		if !validSubnet(s) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"network_configs.subnet %q must be projects/{project}/regions/{region}/subnetworks/{subnet}", s)
		}

		if region := strings.Split(s, "/")[subnetRegionsIdx+1]; region != location {
			return cerrors.Newf(cerrors.InvalidArgument,
				"network_configs.subnet %q is in region %q; it must be in the cluster's region %q", s, region, location)
		}
	}

	return nil
}

// validSubnet reports whether s is a well-formed subnetwork resource name.
func validSubnet(s string) bool {
	parts := strings.Split(s, "/")
	if len(parts) != subnetPathSegments {
		return false
	}

	for _, p := range parts {
		if p == "" {
			return false
		}
	}

	return parts[subnetProjectsIdx] == "projects" && parts[subnetRegionsIdx] == "regions" &&
		parts[subnetSubnetworksIdx] == "subnetworks"
}

// validateTopic requires positive partition and replication counts.
func validateTopic(t *mkdriver.Topic) error {
	if t.PartitionCount <= 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "partition_count must be greater than 0, got %d", t.PartitionCount)
	}

	if t.ReplicationFactor <= 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "replication_factor must be greater than 0, got %d", t.ReplicationFactor)
	}

	return nil
}

// clusterMaskAppliers maps each mutable cluster field-mask path (camelCase) to
// the copy it performs from the request onto the stored cluster.
//
//nolint:gochecknoglobals // immutable lookup table
var clusterMaskAppliers = map[string]func(dst, src *mkdriver.Cluster){
	pathCapacity: func(dst, src *mkdriver.Cluster) {
		dst.VcpuCount, dst.MemoryBytes = src.VcpuCount, src.MemoryBytes
	},
	"capacityConfig.vcpuCount":              func(dst, src *mkdriver.Cluster) { dst.VcpuCount = src.VcpuCount },
	"capacityConfig.memoryBytes":            func(dst, src *mkdriver.Cluster) { dst.MemoryBytes = src.MemoryBytes },
	"gcpConfig.accessConfig":                copySubnets,
	"gcpConfig.accessConfig.networkConfigs": copySubnets,
	pathRebalance:                           func(dst, src *mkdriver.Cluster) { dst.RebalanceMode = src.RebalanceMode },
	"rebalanceConfig.mode":                  func(dst, src *mkdriver.Cluster) { dst.RebalanceMode = src.RebalanceMode },
	pathLabels:                              func(dst, src *mkdriver.Cluster) { dst.Labels = cloneStringMap(src.Labels) },
	pathVersion:                             func(dst, src *mkdriver.Cluster) { dst.KafkaVersion = src.KafkaVersion },
	pathTLS:                                 func(dst, src *mkdriver.Cluster) { dst.TLS = cloneTLS(src.TLS) },
	"tlsConfig.sslPrincipalMappingRules":    copyPrincipalRules,
	"tlsConfig.trustConfig":                 copyCAPools,
	"tlsConfig.trustConfig.casConfigs":      copyCAPools,
	pathUpdateOps:                           copyUpdateOptions,
	"updateOptions.allowBrokerDownscaleOnClusterUpscale": copyUpdateOptions,
	pathBroker:                         func(dst, src *mkdriver.Cluster) { dst.BrokerDiskSizeGib = src.BrokerDiskSizeGib },
	"brokerCapacityConfig.diskSizeGib": func(dst, src *mkdriver.Cluster) { dst.BrokerDiskSizeGib = src.BrokerDiskSizeGib },
}

func copyUpdateOptions(dst, src *mkdriver.Cluster) {
	dst.AllowBrokerDownscaleOnClusterUpscale = src.AllowBrokerDownscaleOnClusterUpscale
}

// copyPrincipalRules sets tlsConfig.sslPrincipalMappingRules, creating the TLS
// block if the cluster had none.
func copyPrincipalRules(dst, src *mkdriver.Cluster) {
	rules := ""
	if src.TLS != nil {
		rules = src.TLS.SSLPrincipalMappingRules
	}

	dst.TLS = ensureTLS(dst.TLS)
	dst.TLS.SSLPrincipalMappingRules = rules
}

// copyCAPools sets tlsConfig.trustConfig.casConfigs, creating the TLS block if
// the cluster had none.
func copyCAPools(dst, src *mkdriver.Cluster) {
	var pools []string
	if src.TLS != nil {
		pools = append([]string(nil), src.TLS.CAPools...)
	}

	dst.TLS = ensureTLS(dst.TLS)
	dst.TLS.CAPools = pools
}

func ensureTLS(t *mkdriver.TLSConfig) *mkdriver.TLSConfig {
	if t == nil {
		return &mkdriver.TLSConfig{}
	}

	return t
}

// clusterFixedPaths are cluster field-mask paths that exist on the resource but
// cannot be updated (immutable or output-only).
//
//nolint:gochecknoglobals // immutable lookup set
var clusterFixedPaths = map[string]bool{
	pathName: true, "state": true, "createTime": true, "updateTime": true,
	"satisfiesPzi": true, "satisfiesPzs": true, "gcpConfig.kmsKey": true,
	"brokerDetails": true,
}

func copySubnets(dst, src *mkdriver.Cluster) { dst.Subnets = append([]string(nil), src.Subnets...) }

// applyClusterMask copies the masked fields of src onto dst. The mask is
// required; "*" updates every mutable field. "gcpConfig" updates the access
// config and rejects a kmsKey change (kmsKey is immutable).
func applyClusterMask(dst, src *mkdriver.Cluster, mask []string) error {
	if len(mask) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "update_mask is required")
	}

	for _, raw := range mask {
		path := camelPath(raw)

		switch {
		case path == maskAll || path == "gcpConfig":
			if src.KmsKey != "" && src.KmsKey != dst.KmsKey {
				return cerrors.New(cerrors.InvalidArgument, "gcp_config.kms_key is immutable")
			}

			applyAllCluster(dst, src, path)
		case clusterMaskAppliers[path] != nil:
			clusterMaskAppliers[path](dst, src)
		case clusterFixedPaths[path]:
			return cerrors.Newf(cerrors.InvalidArgument, "field %q in update_mask is immutable or output only", raw)
		default:
			return cerrors.Newf(cerrors.InvalidArgument, "unknown field %q in update_mask", raw)
		}
	}

	return nil
}

// applyAllCluster applies the "*" mask (every mutable field) or the "gcpConfig"
// mask (the access config only).
func applyAllCluster(dst, src *mkdriver.Cluster, path string) {
	copySubnets(dst, src)

	if path != maskAll {
		return
	}

	for _, p := range []string{pathCapacity, pathRebalance, pathLabels, pathVersion, pathTLS, pathUpdateOps, pathBroker} {
		clusterMaskAppliers[p](dst, src)
	}
}

// applyTopicMask copies the masked fields of src onto dst. The mask is
// required; "*" updates every mutable field (partitionCount, configs), and
// rejects a replicationFactor change.
func applyTopicMask(dst, src *mkdriver.Topic, mask []string) error {
	if len(mask) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "update_mask is required")
	}

	for _, raw := range mask {
		switch camelPath(raw) {
		case maskAll:
			if src.ReplicationFactor != 0 && src.ReplicationFactor != dst.ReplicationFactor {
				return cerrors.New(cerrors.InvalidArgument, "replication_factor is immutable")
			}

			dst.PartitionCount = src.PartitionCount
			dst.Configs = cloneStringMap(src.Configs)
		case "partitionCount":
			dst.PartitionCount = src.PartitionCount
		case "configs":
			dst.Configs = cloneStringMap(src.Configs)
		case "replicationFactor", pathName:
			return cerrors.Newf(cerrors.InvalidArgument, "field %q in update_mask is immutable", raw)
		default:
			return cerrors.Newf(cerrors.InvalidArgument, "unknown field %q in update_mask", raw)
		}
	}

	return nil
}

// camelPath converts a snake_case field-mask path (the proto form) to the
// camelCase JSON form, so both spellings are accepted.
func camelPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.Contains(p, "_") {
		return p
	}

	var b strings.Builder

	upper := false

	for _, r := range p {
		if r == '_' {
			upper = true
			continue
		}

		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}

		b.WriteRune(r)
	}

	return b.String()
}
