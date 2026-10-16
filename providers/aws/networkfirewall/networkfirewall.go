// Package networkfirewall provides an in-memory mock of AWS Network Firewall.
package networkfirewall

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const (
	ruleTypeStateful       = "STATEFUL"
	ruleTypeStateless      = "STATELESS"
	ruleTypeStatefulDomain = "STATEFUL_DOMAIN"

	statusReady    = "READY"
	statusDeleting = "DELETING"
	statusActive   = "ACTIVE"
)

var (
	_ nfdriver.NetworkFirewall         = (*Mock)(nil)
	_ nfdriver.FirewallSettings        = (*Mock)(nil)
	_ nfdriver.AvailabilityZones       = (*Mock)(nil)
	_ nfdriver.ResourcePolicies        = (*Mock)(nil)
	_ nfdriver.TLSInspection           = (*Mock)(nil)
	_ nfdriver.VPCEndpointAssociations = (*Mock)(nil)
	_ nfdriver.Metadata                = (*Mock)(nil)
	_ nfdriver.FlowOperations          = (*Mock)(nil)
	_ nfdriver.AnalysisReports         = (*Mock)(nil)
)

// Mock is the in-memory AWS Network Firewall implementation. Resources are
// keyed by name (unique per account/region, as in the real service); VPC
// endpoint associations by ARN, flow operations and analysis reports by id.
//
// The stores hand back shared pointers whose fields are mutated in place
// (associate/tag/protection/subnets) and logging/resource policies are plain
// maps, so every public method serializes on mu: mutators take Lock, readers
// take RLock, and everything handed out is a deep copy. That single lock also
// makes every create/delete (including its reference checks) atomic, so no
// concurrent pair can both pass a check.
type Mock struct {
	mu               sync.RWMutex
	firewalls        *memstore.Store[*nfdriver.Firewall]
	policies         *memstore.Store[*nfdriver.FirewallPolicy]
	ruleGroups       *memstore.Store[*nfdriver.RuleGroup]
	tlsConfigs       *memstore.Store[*nfdriver.TLSInspectionConfig]
	associations     *memstore.Store[*nfdriver.VPCEndpointAssociation]
	flowOperations   *memstore.Store[*nfdriver.FlowOperation]
	analysisReports  *memstore.Store[*nfdriver.AnalysisReport]
	logging          map[string]*nfdriver.LoggingConfiguration
	resourcePolicies map[string]string
	opts             *config.Options
}

// New creates a new Network Firewall mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		firewalls:        memstore.New[*nfdriver.Firewall](),
		policies:         memstore.New[*nfdriver.FirewallPolicy](),
		ruleGroups:       memstore.New[*nfdriver.RuleGroup](),
		tlsConfigs:       memstore.New[*nfdriver.TLSInspectionConfig](),
		associations:     memstore.New[*nfdriver.VPCEndpointAssociation](),
		flowOperations:   memstore.New[*nfdriver.FlowOperation](),
		analysisReports:  memstore.New[*nfdriver.AnalysisReport](),
		logging:          map[string]*nfdriver.LoggingConfiguration{},
		resourcePolicies: map[string]string{},
		opts:             opts,
	}
}

func (m *Mock) arn(kind, name string) string {
	return idgen.AWSARN("network-firewall", m.opts.Region, m.opts.AccountID, kind+"/"+name)
}

func (m *Mock) now() time.Time { return m.opts.Clock.Now() }

// newToken mints an update token (an RFC 4122 UUID, the shape AWS documents).
func newToken() string { return idgen.UUID() }

// arnKind returns the resource segment of a Network Firewall ARN
// ("firewall", "stateful-rulegroup", ...), or "" for anything else.
func arnKind(arn string) string {
	parts := strings.SplitN(arn, ":", 6) //nolint:mnd // an ARN has six colon-separated fields
	if len(parts) < 6 || parts[2] != "network-firewall" {
		return ""
	}

	kind, _, _ := strings.Cut(parts[5], "/")

	return kind
}

func copyTags(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	return append([]string(nil), s...)
}

func cloneRawJSON(r []byte) []byte {
	if len(r) == 0 {
		return nil
	}

	out := make([]byte, len(r))
	copy(out, r)

	return out
}

func cloneBool(b *bool) *bool {
	if b == nil {
		return nil
	}

	v := *b

	return &v
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
