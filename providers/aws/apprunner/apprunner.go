// Package apprunner provides an in-memory mock of the AWS App Runner control
// plane: services and their operation history, plus the auto scaling
// configuration, connection, VPC connector and observability configuration
// resources a service composes.
//
// The mock is control-plane only — it runs no container runtime. A service is
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
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// Compile-time check that Mock implements driver.AppRunner.
var _ driver.AppRunner = (*Mock)(nil)

// serviceName is the App Runner service marker used in every ARN.
const serviceName = "apprunner"

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 20

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
	services      *memstore.Store[driver.Service]
	autoScaling   *memstore.Store[driver.AutoScalingConfiguration]
	connections   *memstore.Store[driver.Connection]
	vpcConnectors *memstore.Store[driver.VpcConnector]
	observability *memstore.Store[driver.ObservabilityConfiguration]
	opts          *config.Options
}

// New creates a new App Runner mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		services:      memstore.New[driver.Service](),
		autoScaling:   memstore.New[driver.AutoScalingConfiguration](),
		connections:   memstore.New[driver.Connection](),
		vpcConnectors: memstore.New[driver.VpcConnector](),
		observability: memstore.New[driver.ObservabilityConfiguration](),
		opts:          opts,
	}
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
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
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
