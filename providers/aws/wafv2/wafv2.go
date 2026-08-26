// Package wafv2 provides an in-memory mock implementation of AWS WAFv2. It
// models WebACLs, IPSets, RuleGroups and RegexPatternSets partitioned by Scope
// (REGIONAL vs CLOUDFRONT), enforces optimistic-lock tokens on mutations, and
// stores rule/statement configuration verbatim so Get returns what Put wrote.
package wafv2

import (
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// Compile-time check that Mock implements driver.WAFV2.
var _ driver.WAFV2 = (*Mock)(nil)

const maxTags = 200

// Mock is an in-memory implementation of AWS WAFv2. Resources are keyed by the
// composite (scope,id) so REGIONAL and CLOUDFRONT namespaces never collide.
type Mock struct {
	webACLs  *memstore.Store[*webACLData]
	ipSets   *memstore.Store[*ipSetData]
	ruleGrps *memstore.Store[*ruleGroupData]
	regexes  *memstore.Store[*regexSetData]
	logCfgs  *memstore.Store[*loggingConfigData]

	assocMu sync.RWMutex
	// assoc maps a protected resource ARN to the web ACL ARN protecting it.
	assoc map[string]string

	policyMu sync.RWMutex
	// policies maps a rule-group ARN to its permission policy JSON.
	policies map[string]string

	apiKeyMu sync.RWMutex
	// apiKeys maps a composite (scope,apiKey) key to its stored summary.
	apiKeys map[string]driver.APIKeySummary

	// createMu serializes the name-uniqueness check and store insert across all
	// resource types so two concurrent same-(scope,name) creates can't both
	// succeed (the by-name scan and the Set below are otherwise not atomic).
	createMu sync.Mutex

	opts *config.Options
}

// validateScope allow-lists the WAFv2 Scope, rejecting any other value with
// WAFInvalidParameterException instead of silently storing it in an unreachable
// namespace.
func validateScope(scope string) error {
	switch scope {
	case driver.ScopeRegional, driver.ScopeCloudFront:
		return nil
	default:
		return invalidParameter("Scope must be REGIONAL or CLOUDFRONT, got %q", scope)
	}
}

type webACLData struct {
	acl driver.WebACL
	mu  sync.RWMutex
}

type ipSetData struct {
	set driver.IPSet
	mu  sync.RWMutex
}

type ruleGroupData struct {
	grp driver.RuleGroup
	mu  sync.RWMutex
}

type regexSetData struct {
	set driver.RegexPatternSet
	mu  sync.RWMutex
}

// New creates a new WAFv2 mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		webACLs:  memstore.New[*webACLData](),
		ipSets:   memstore.New[*ipSetData](),
		ruleGrps: memstore.New[*ruleGroupData](),
		regexes:  memstore.New[*regexSetData](),
		logCfgs:  memstore.New[*loggingConfigData](),
		assoc:    map[string]string{},
		policies: map[string]string{},
		apiKeys:  map[string]driver.APIKeySummary{},
		opts:     opts,
	}
}

// key composes the store key for a resource within a scope.
func key(scope, id string) string {
	return scope + "/" + id
}

// newLockToken returns a fresh optimistic-lock token.
func newLockToken() string {
	return idgen.GenerateID("")
}

// cloudFrontARNRegion is the region segment of a CLOUDFRONT-scope WAFv2 ARN.
// WAFv2 manages CloudFront-scoped resources in us-east-1, so their ARNs are
// always anchored there regardless of the client's configured region.
const cloudFrontARNRegion = "us-east-1"

// arn builds a WAFv2 ARN. The resource segment carries the lowercase scope word
// ("regional" for REGIONAL, "global" for CLOUDFRONT) followed by <kind>/<name>/
// <id>. REGIONAL ARNs use the configured region; CLOUDFRONT ARNs are anchored in
// us-east-1 (where WAFv2 manages CloudFront), never the literal word "global".
func (m *Mock) arn(scope, kind, name, id string) string {
	region := m.opts.Region
	scopePath := "regional"

	if scope == driver.ScopeCloudFront {
		region = cloudFrontARNRegion
		scopePath = "global"
	}

	return idgen.AWSARN("wafv2", region, m.opts.AccountID, scopePath+"/"+kind+"/"+name+"/"+id)
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

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}

	return append([]byte(nil), in...)
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}
