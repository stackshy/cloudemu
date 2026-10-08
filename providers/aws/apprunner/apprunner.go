// Package apprunner provides an in-memory mock of the AWS App Runner control
// plane: services and their operation history, plus the auto scaling
// configuration, connection, VPC connector and observability configuration
// resources a service composes.
//
// The mock is control-plane only. It runs no container runtime. A service is
// created directly into the terminal RUNNING state (App Runner's real create is
// asynchronous), so an IaC apply completes without a provisioning wait.
// PauseService moves RUNNING to PAUSED, ResumeService moves PAUSED back to
// RUNNING, and DeleteService removes the service (reporting DELETED); illegal
// transitions are rejected with InvalidStateException and an unknown ARN yields
// ResourceNotFoundException. The computed fields (ids, ARNs, service URL, status,
// timestamps) are minted once at create and stored, so repeated reads never
// drift, and the configuration blocks round-trip verbatim.
package apprunner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time checks that Mock implements driver.AppRunner and every optional
// capability the wire handler serves.
var (
	_ driver.AppRunner             = (*Mock)(nil)
	_ driver.VpcIngressConnections = (*Mock)(nil)
	_ driver.CustomDomains         = (*Mock)(nil)
	_ driver.DefaultAutoScaling    = (*Mock)(nil)
)

// NetworkResolver looks up the VPC networking a VPC connector refers to. The
// networking mock satisfies it; without one, connector subnets and security
// groups are accepted as given.
type NetworkResolver interface {
	DescribeSubnets(ctx context.Context, ids []string) ([]netdriver.SubnetInfo, error)
	DescribeSecurityGroups(ctx context.Context, ids []string) ([]netdriver.SecurityGroupInfo, error)
}

// serviceName is the App Runner service marker used in every ARN.
const serviceName = "apprunner"

// idBytes is the number of random bytes rendered into a 32-hex resource id.
const idBytes = 16

// firstRevision is the revision number of a versioned resource's first revision.
const firstRevision = 1

// Auto scaling configuration defaults, applied when the caller omits a value, so
// reads are stable and match the real service.
const (
	defaultMaxConcurrency = 100
	defaultMinSize        = 1
	defaultMaxSize        = 25
)

// Mock is an in-memory implementation of the AWS App Runner control plane. Each
// store is keyed by the resource's ARN.
type Mock struct {
	// refMu serializes every change to the set of references a service holds on a
	// shared configuration (service create/update/delete) with the delete of
	// that configuration, so an in-use check and its delete are one step.
	refMu         sync.Mutex
	services      *memstore.Store[driver.Service]
	autoScaling   *memstore.Store[driver.AutoScalingConfiguration]
	connections   *memstore.Store[driver.Connection]
	vpcConnectors *memstore.Store[driver.VpcConnector]
	observability *memstore.Store[driver.ObservabilityConfiguration]
	ingress       *memstore.Store[driver.VpcIngressConnection]
	domains       *memstore.Store[domainRecord]
	opts          *config.Options

	// settling overlays a transient OPERATION_IN_PROGRESS / PENDING_* status on
	// services, ingress connections and custom domains when the server runs with
	// async settling; it is inactive (terminal status at once) by default so IaC
	// waiters complete.
	settling *settle.Set

	monitoring mondriver.Monitoring
	logs       logdriver.Logging
	network    NetworkResolver

	// revMarks remembers the highest revision ever minted per configuration name
	// ("kind/name"), so a deleted revision's number is never handed out again.
	revMu    sync.Mutex
	revMarks map[string]int32
}

// SetMonitoring wires the CloudWatch backend that receives the AWS/AppRunner
// metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) { m.monitoring = mon }

// SetLogSink wires CloudWatch Logs so a service's service and application log
// groups are created with it and removed when it is deleted.
func (m *Mock) SetLogSink(l logdriver.Logging) { m.logs = l }

// SetNetworkResolver wires the networking mock so VPC connector subnets and
// security groups are cross-checked.
func (m *Mock) SetNetworkResolver(r NetworkResolver) { m.network = r }

// New creates a new App Runner mock with the given options.
func New(opts *config.Options) *Mock {
	m := &Mock{
		services:      memstore.New[driver.Service](),
		autoScaling:   memstore.New[driver.AutoScalingConfiguration](),
		connections:   memstore.New[driver.Connection](),
		vpcConnectors: memstore.New[driver.VpcConnector](),
		observability: memstore.New[driver.ObservabilityConfiguration](),
		ingress:       memstore.New[driver.VpcIngressConnection](),
		domains:       memstore.New[domainRecord](),
		settling:      settle.NewSet(),
		revMarks:      map[string]int32{},
		opts:          opts,
	}

	m.seedDefaultAutoScaling()

	return m
}

// defaultAutoScalingID derives the stable id of the account's default auto
// scaling configuration from its region and account, so a restored snapshot
// lands on the same ARN the fresh mock seeded instead of adding a second default.
func (m *Mock) defaultAutoScalingID() string {
	sum := sha256.Sum256([]byte(defaultConfigName + "/" + m.opts.Region + "/" + m.opts.AccountID))

	return hex.EncodeToString(sum[:idBytes])
}

// seededDefaultAutoScalingARN is the ARN of the DefaultConfiguration the mock
// starts with.
func (m *Mock) seededDefaultAutoScalingARN() string {
	return m.autoScalingARN(defaultConfigName, firstRevision, m.defaultAutoScalingID())
}

// defaultAutoScalingARN is the ARN of the configuration currently flagged as the
// account's default, which UpdateDefaultAutoScalingConfiguration can move.
func (m *Mock) defaultAutoScalingARN() string {
	all := m.autoScaling.SortedValues()
	for i := range all {
		if all[i].IsDefault && all[i].Status == driver.AutoScalingStatusActive {
			return all[i].AutoScalingConfigurationArn
		}
	}

	return m.seededDefaultAutoScalingARN()
}

// seedDefaultAutoScaling stores the DefaultConfiguration auto scaling
// configuration every real App Runner account starts with. A service created
// without an auto scaling configuration is associated with it.
func (m *Mock) seedDefaultAutoScaling() {
	arn := m.seededDefaultAutoScalingARN()

	m.autoScaling.Set(arn, driver.AutoScalingConfiguration{
		AutoScalingConfigurationArn:      arn,
		AutoScalingConfigurationName:     defaultConfigName,
		AutoScalingConfigurationRevision: firstRevision,
		Latest:                           true,
		IsDefault:                        true,
		Status:                           driver.AutoScalingStatusActive,
		MaxConcurrency:                   defaultMaxConcurrency,
		MinSize:                          defaultMinSize,
		MaxSize:                          defaultMaxSize,
		CreatedAt:                        m.now(),
	})
}

// newID mints a random 32-character lowercase hex resource id.
func newID() string {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never fails on supported platforms; fall back to a
		// zeroed id rather than panicking in a control-plane emulator.
		return "00000000000000000000000000000000"
	}

	return hex.EncodeToString(b)
}

// serviceARN mints the stable ARN for a service:
// arn:aws:apprunner:<region>:<acct>:service/<name>/<id>.
func (m *Mock) serviceARN(name, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, "service/"+name+"/"+id)
}

// serviceURL mints the stable subdomain URL for a service.
func (m *Mock) serviceURL(id string) string {
	return id + "." + m.opts.Region + ".awsapprunner.com"
}

// autoScalingARN mints the stable versioned ARN for an auto scaling
// configuration: .../autoscalingconfiguration/<name>/<revision>/<id>.
func (m *Mock) autoScalingARN(name string, revision int32, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID,
		"autoscalingconfiguration/"+name+"/"+strconv.Itoa(int(revision))+"/"+id)
}

// vpcConnectorARN mints the stable versioned ARN for a VPC connector.
func (m *Mock) vpcConnectorARN(name string, revision int32, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID,
		"vpcconnector/"+name+"/"+strconv.Itoa(int(revision))+"/"+id)
}

// observabilityARN mints the stable versioned ARN for an observability
// configuration.
func (m *Mock) observabilityARN(name string, revision int32, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID,
		"observabilityconfiguration/"+name+"/"+strconv.Itoa(int(revision))+"/"+id)
}

// connectionARN mints the stable ARN for a connection.
func (m *Mock) connectionARN(name, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, "connection/"+name+"/"+id)
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token. With no MaxResults every remaining
// result is returned in one page, as the API documents.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	if page.MaxResults <= 0 {
		return start, n, ""
	}

	end = start + int(page.MaxResults)
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// nextRevision returns the next revision number of a named configuration: one past
// the highest of the stored revisions and every revision ever minted for the name.
func (m *Mock) nextRevision(kind, name string, storedHighest int32) int32 {
	m.revMu.Lock()
	defer m.revMu.Unlock()

	key := kind + "/" + name
	next := max(storedHighest, m.revMarks[key]) + 1
	m.revMarks[key] = next

	return next
}

// settleWindow is how long a resource reports a transient status under async
// settling.
const settleWindow = settle.DefaultClusterSettle
