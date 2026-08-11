// Package kafka provides an in-memory mock implementation of Amazon MSK
// (Managed Streaming for Apache Kafka). It implements the full 59-operation MSK
// control plane: provisioned (v1/v2) and serverless clusters, configurations
// and revisions, cluster operations and updates, nodes, versions, tags, VPC
// connections, topic management, SCRAM-secret associations, cluster resource
// policies, and cross-cluster replicators.
//
// Account-level resources (clusters, configurations, VPC connections,
// replicators) live in Mock-level stores; per-cluster children (topics, SCRAM
// secrets, policy, operations) live on clusterData under its lock so a mutation
// is atomic with its reads.
//
// Clusters are provisioned immediately Active (deterministic — no wall-clock
// wait), with a server-minted ARN. Cluster names are unique per account
// (ConflictException on a duplicate). All reads deep-copy maps, slices, and
// json.RawMessage so a caller can never mutate stored state through a result.
package kafka

import (
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// Compile-time check that Mock implements driver.Kafka.
var _ driver.Kafka = (*Mock)(nil)

const (
	// defaultMaxResults caps a page when the caller requests none.
	defaultMaxResults = 100
	maxClusterNameLen = 64
	minClusterNameLen = 1
	defaultKafkaVer   = "3.6.0"
)

// clusterData is the full server-side state of a cluster plus its own lock.
// operations holds this cluster's operation records, oldest first, appended by
// every mutating op; the same records are indexed globally by opARN for the
// Describe*Operation ops. topics, scramSecrets and policy hold the per-cluster
// child resources (topic management, SCRAM-secret associations, and the single
// resource policy); all live under mu so a mutation is atomic with its reads.
type clusterData struct {
	cluster       driver.Cluster
	operations    []driver.ClusterOperation
	topics        map[string]driver.Topic
	scramSecrets  []string
	policy        string
	policyVersion string
	mu            sync.RWMutex
}

// vpcConnData is a stored VPC connection plus its lock. State transitions (e.g.
// a client-vpc-connection reject) mutate under mu.
type vpcConnData struct {
	vpc driver.VpcConnection
	mu  sync.RWMutex
}

// replicatorData is a stored replicator plus its lock. UpdateReplicationInfo
// mutates the replication-info raw blocks and bumps the version under mu.
type replicatorData struct {
	replicator driver.Replicator
	version    string
	mu         sync.RWMutex
}

// configData is the full server-side state of a configuration plus its lock.
type configData struct {
	config driver.Configuration
	mu     sync.RWMutex
}

// Mock is an in-memory implementation of Amazon MSK.
type Mock struct {
	// clusters is keyed by cluster ARN. clusterNames claims each (unique)
	// cluster name to its ARN so a duplicate name is a ConflictException,
	// matching real MSK. Keying the store by ARN keeps the name-uniqueness
	// invariant, so a create-mutex serializes the name-claim + insert.
	clusters     *memstore.Store[*clusterData]
	clusterNames *memstore.Store[string]
	// configs is keyed by configuration ARN.
	configs *memstore.Store[*configData]
	// operations indexes every cluster operation by its operation ARN so a
	// DescribeClusterOperation resolves without scanning every cluster. It points
	// at the owning clusterData; the record itself lives in that cluster's
	// operations slice.
	operations *memstore.Store[*clusterData]

	// vpcConns is keyed by VPC-connection ARN (account-level). replicators is
	// keyed by replicator ARN (account-level); its name-uniqueness invariant is
	// enforced by scanning under createMu, like clusters.
	vpcConns    *memstore.Store[*vpcConnData]
	replicators *memstore.Store[*replicatorData]

	// createMu serializes cluster creation so the name-claim and ARN insert are
	// atomic with respect to a concurrent duplicate-name create. It also
	// serializes replicator creation so the name-scan and ARN insert are atomic.
	createMu sync.Mutex

	opts *config.Options
}

// New creates a new MSK mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:     memstore.New[*clusterData](),
		clusterNames: memstore.New[string](),
		configs:      memstore.New[*configData](),
		operations:   memstore.New[*clusterData](),
		vpcConns:     memstore.New[*vpcConnData](),
		replicators:  memstore.New[*replicatorData](),
		opts:         opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// clusterARN mints an MSK cluster ARN: arn:aws:kafka:region:account:cluster/<name>/<uuid>.
func (m *Mock) clusterARN(name string) string {
	return idgen.AWSARN("kafka", m.opts.Region, m.opts.AccountID,
		"cluster/"+name+"/"+idgen.GenerateID(""))
}

// configARN mints an MSK configuration ARN.
func (m *Mock) configARN(name string) string {
	return idgen.AWSARN("kafka", m.opts.Region, m.opts.AccountID,
		"configuration/"+name+"/"+idgen.GenerateID(""))
}

// operationARN mints an MSK cluster-operation ARN.
func (m *Mock) operationARN() string {
	return idgen.AWSARN("kafka", m.opts.Region, m.opts.AccountID,
		"cluster-operation/"+idgen.GenerateID(""))
}

// vpcConnectionARN mints an MSK VPC-connection ARN.
func (m *Mock) vpcConnectionARN() string {
	return idgen.AWSARN("kafka", m.opts.Region, m.opts.AccountID,
		"vpc-connection/"+idgen.GenerateID(""))
}

// replicatorARN mints an MSK replicator ARN under the replicator resource kind.
func (m *Mock) replicatorARN(name string) string {
	return idgen.AWSARN("kafka", m.opts.Region, m.opts.AccountID,
		"replicator/"+name+"/"+idgen.GenerateID(""))
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	return append([]byte(nil), in...)
}

func copyRaw(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}

// paginate returns the window and next token for a slice of length n, honoring
// an opaque numeric offset token. A corrupt or out-of-range token is a
// BadRequestException rather than a silent reset to page one.
func (*Mock) paginate(n int, page driver.Page) (start, end int, next string, err error) {
	start = 0

	if page.NextToken != "" {
		start, err = strconv.Atoi(page.NextToken)
		if err != nil || start < 0 || start > n {
			return 0, 0, "", badRequest("invalid pagination token: %q", page.NextToken)
		}
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, "", nil
	}

	return start, end, strconv.Itoa(end), nil
}
