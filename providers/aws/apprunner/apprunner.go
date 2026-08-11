// Package apprunner provides an in-memory mock implementation of AWS App Runner.
//
// The mock does not build or run containers: CreateService completes
// deterministically (Status immediately RUNNING) with a synthesized ServiceUrl,
// and lifecycle transitions (Pause/Resume/Delete) mutate stored state directly.
// Large nested configuration blocks are stored verbatim as json.RawMessage.
//
// Every App Runner operation is implemented: services, auto scaling
// configurations, connections, observability configurations, VPC connectors, VPC
// ingress connections, per-service custom domains, per-service operations, and
// resource tags.
package apprunner

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// Compile-time check that Mock implements driver.AppRunner.
var _ driver.AppRunner = (*Mock)(nil)

const (
	// defaultPageSize is the ListX page size when MaxResults is unset.
	defaultPageSize = 100
	// service segment of App Runner ARNs.
	serviceSegment = "apprunner"
)

// Mock is an in-memory implementation of AWS App Runner.
type Mock struct {
	services    *memstore.Store[*serviceData]
	connections *memstore.Store[*connectionData]

	// ascMu guards the auto scaling configuration revision map: revision
	// counters must be managed atomically under one lock so concurrent
	// same-name creates get distinct, incrementing revisions.
	ascMu sync.Mutex
	// ascByArn stores every ASC revision keyed by its full ARN.
	ascByArn map[string]*driver.AutoScalingConfiguration
	// ascLatestRev tracks the highest revision minted per configuration name.
	ascLatestRev map[string]int32

	// obsMu guards the observability configuration revision map, mirroring the
	// ASC revision model (concurrent same-name creates increment atomically).
	obsMu sync.Mutex
	// obsByArn stores every observability configuration revision by its ARN.
	obsByArn map[string]*driver.ObservabilityConfiguration
	// obsLatestRev tracks the highest revision minted per configuration name.
	obsLatestRev map[string]int32

	// vpcConnectors holds VPC connector revisions keyed by their ARN. App Runner
	// supports one revision per name today, so a plain store is sufficient.
	vpcConnectors *memstore.Store[*vpcConnectorData]

	// vpcIngress holds VPC ingress connections keyed by their ARN.
	vpcIngress *memstore.Store[*vpcIngressData]

	opts *config.Options
}

// serviceData is a service plus its own lock, including the per-service
// operations list and custom-domain map surfaced by ListOperations and
// DescribeCustomDomains.
type serviceData struct {
	svc     driver.Service
	ops     []driver.OperationSummary
	domains map[string]*driver.CustomDomain
	mu      sync.RWMutex
}

// vpcConnectorData is a VPC connector plus its own lock.
type vpcConnectorData struct {
	vc driver.VpcConnector
	mu sync.RWMutex
}

// vpcIngressData is a VPC ingress connection plus its own lock.
type vpcIngressData struct {
	vic driver.VpcIngressConnection
	mu  sync.RWMutex
}

// connectionData is a connection plus its own lock.
type connectionData struct {
	conn driver.Connection
	mu   sync.RWMutex
}

// New creates a new App Runner mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		services:      memstore.New[*serviceData](),
		connections:   memstore.New[*connectionData](),
		ascByArn:      make(map[string]*driver.AutoScalingConfiguration),
		ascLatestRev:  make(map[string]int32),
		obsByArn:      make(map[string]*driver.ObservabilityConfiguration),
		obsLatestRev:  make(map[string]int32),
		vpcConnectors: memstore.New[*vpcConnectorData](),
		vpcIngress:    memstore.New[*vpcIngressData](),
		opts:          opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) serviceArn(name, id string) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID, "service/"+name+"/"+id)
}

func (m *Mock) connectionArn(name string) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID, "connection/"+name+"/"+idgen.GenerateID(""))
}

func (m *Mock) ascArn(name string, revision int32) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID,
		"autoscalingconfiguration/"+name+"/"+itoa(revision)+"/"+idgen.GenerateID(""))
}

func (m *Mock) obsArn(name string, revision int32) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID,
		"observabilityconfiguration/"+name+"/"+itoa(revision)+"/"+idgen.GenerateID(""))
}

func (m *Mock) vpcConnectorArn(name string, revision int32) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID,
		"vpcconnector/"+name+"/"+itoa(revision)+"/"+idgen.GenerateID(""))
}

func (m *Mock) vpcIngressArn(name string) string {
	return idgen.AWSARN(serviceSegment, m.opts.Region, m.opts.AccountID,
		"vpcingressconnection/"+name+"/"+idgen.GenerateID(""))
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

// copyRaw deep-copies a json.RawMessage so Describe/List callers can't mutate
// stored bytes.
func copyRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	return append(json.RawMessage(nil), in...)
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}
