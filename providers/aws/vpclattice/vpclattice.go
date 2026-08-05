// Package vpclattice provides an in-memory mock of the AWS VPC Lattice control
// plane. It satisfies services/vpclattice/driver so the real
// aws-sdk-go-v2/service/vpclattice client works against it via the AWS server
// (REST-JSON, path + method routing).
package vpclattice

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

// Compile-time check that Mock implements the driver contract.
var _ driver.VPCLattice = (*Mock)(nil)

const authTypeNone = "NONE"

// Mock is an in-memory mock of AWS VPC Lattice.
type Mock struct {
	serviceNetworks *memstore.Store[*driver.ServiceNetwork]    // keyed by service-network ID
	services        *memstore.Store[*driver.Service]           // keyed by service ID
	listeners       *memstore.Store[*driver.Listener]          // keyed by listener ID
	rules           *memstore.Store[*driver.Rule]              // keyed by rule ID
	targetGroups    *memstore.Store[*driver.TargetGroup]       // keyed by target-group ID
	targets         *memstore.Store[[]driver.RegisteredTarget] // keyed by target-group ID
	snVpcAssocs     *memstore.Store[*driver.SNVpcAssociation]  // keyed by association ID
	snSvcAssocs     *memstore.Store[*driver.SNServiceAssociation]
	snResAssocs     *memstore.Store[*driver.SNResourceAssociation]
	resourceConfigs *memstore.Store[*driver.ResourceConfiguration]
	resourceGws     *memstore.Store[*driver.ResourceGateway]
	accessLogSubs   *memstore.Store[*driver.AccessLogSubscription]
	authPolicies    *memstore.Store[*driver.AuthPolicy]         // keyed by resource identifier
	resourcePolics  *memstore.Store[string]                     // keyed by resource ARN
	domainVerifs    *memstore.Store[*driver.DomainVerification] // keyed by verification ID
	tags            *memstore.Store[map[string]string]          // keyed by resource ARN
	opts            *config.Options
	mu              sync.Mutex // serializes read-modify-write on stored records
}

// New returns a VPC Lattice mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		serviceNetworks: memstore.New[*driver.ServiceNetwork](),
		services:        memstore.New[*driver.Service](),
		listeners:       memstore.New[*driver.Listener](),
		rules:           memstore.New[*driver.Rule](),
		targetGroups:    memstore.New[*driver.TargetGroup](),
		targets:         memstore.New[[]driver.RegisteredTarget](),
		snVpcAssocs:     memstore.New[*driver.SNVpcAssociation](),
		snSvcAssocs:     memstore.New[*driver.SNServiceAssociation](),
		snResAssocs:     memstore.New[*driver.SNResourceAssociation](),
		resourceConfigs: memstore.New[*driver.ResourceConfiguration](),
		resourceGws:     memstore.New[*driver.ResourceGateway](),
		accessLogSubs:   memstore.New[*driver.AccessLogSubscription](),
		authPolicies:    memstore.New[*driver.AuthPolicy](),
		resourcePolics:  memstore.New[string](),
		domainVerifs:    memstore.New[*driver.DomainVerification](),
		tags:            memstore.New[map[string]string](),
		opts:            opts,
	}
}

// now returns the current time (via the injectable clock) in RFC 3339 — the
// wire format the VPC Lattice REST-JSON deserializers parse for timestamps.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// arn builds a vpc-lattice ARN for the given resource path.
func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("vpc-lattice", m.opts.Region, m.opts.AccountID, resource)
}

// idFromIdentifier accepts either a bare ID or a full ARN and returns the ID
// (the segment after the last "/"). VPC Lattice APIs accept both forms.
func idFromIdentifier(identifier string) string {
	if i := strings.LastIndex(identifier, "/"); i >= 0 {
		return identifier[i+1:]
	}

	return identifier
}

// sortedValues returns a store's values sorted by key, each deep-copied via
// clone — the shared List implementation for every resource group.
func sortedValues[T any](all map[string]*T, clone func(*T) T) []T {
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	out := make([]T, 0, len(all))
	for _, id := range ids {
		out = append(out, clone(all[id]))
	}

	return out
}
