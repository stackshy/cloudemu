package apigateway

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// apiExt holds the per-API resources beyond the core resource tree: authorizers,
// models, request validators and gateway response overrides. It lives inside
// apiData, so apiData.mu guards it.
type apiExt struct {
	authorizers map[string]*driver.Authorizer
	models      map[string]*driver.Model            // by name
	validators  map[string]*driver.RequestValidator // by id
	gwResponses map[string]*driver.GatewayResponse  // overrides by response type
	authCache   map[string]authDecision             // authorizer result cache
}

func newAPIExt() apiExt {
	return apiExt{
		authorizers: map[string]*driver.Authorizer{},
		models:      defaultModels(),
		validators:  map[string]*driver.RequestValidator{},
		gwResponses: map[string]*driver.GatewayResponse{},
		authCache:   map[string]authDecision{},
	}
}

// regionExt holds the account-and-region scoped resources that live outside any
// REST API. regionMu guards it (lock order: regionMu before apiData.mu).
type regionExt struct {
	keys     map[string]*driver.APIKey
	plans    map[string]*driver.UsagePlan
	planKeys map[string]map[string]bool                    // plan id -> attached key ids
	domains  map[string]*driver.DomainName                 // by domain name
	mappings map[string]map[string]*driver.BasePathMapping // domain -> base path
	vpcLinks map[string]*driver.VpcLink

	usageMu sync.Mutex
	usage   map[string]map[string]int64 // "plan|key" -> day -> requests
	buckets map[string]*tokenBucket     // "plan|key" or "stage|method" -> bucket
}

func newRegionExt() regionExt {
	return regionExt{
		keys:     map[string]*driver.APIKey{},
		plans:    map[string]*driver.UsagePlan{},
		planKeys: map[string]map[string]bool{},
		domains:  map[string]*driver.DomainName{},
		mappings: map[string]map[string]*driver.BasePathMapping{},
		vpcLinks: map[string]*driver.VpcLink{},
		usage:    map[string]map[string]int64{},
		buckets:  map[string]*tokenBucket{},
	}
}

// tokenBucket is a rate limiter on the mock's clock: it refills rate tokens per
// second up to burst.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// take consumes one token, reporting false when none is available.
func (b *tokenBucket) take(now time.Time, rate float64, burst int) bool {
	if b.last.IsZero() {
		b.tokens, b.last = float64(burst), now
	}

	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}

	b.last = now

	if b.tokens < 1 {
		return false
	}

	b.tokens--

	return true
}

func copyStrSlice(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string{}, in...)
}

func copyIntPointer(p *int) *int { return copyIntPtr(p) }
