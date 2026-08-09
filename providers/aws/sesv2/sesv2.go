// Package sesv2 provides an in-memory mock implementation of AWS SES v2 (Simple
// Email Service v2). Identities auto-verify to SUCCESS, SendEmail validates the
// from-identity and returns a generated MessageId, and sent messages are
// retained so tests can assert on them.
package sesv2

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// Compile-time check that Mock implements driver.SESV2.
var _ driver.SESV2 = (*Mock)(nil)

const (
	// defaultMax24HourSend is the sandbox 24-hour sending quota.
	defaultMax24HourSend = 200
	// defaultMaxSendRate is the sandbox per-second sending rate.
	defaultMaxSendRate = 1
	// maxTags is the SES cap on tags per resource.
	maxTags = 50
	// dkimTokenCount is the number of CNAME tokens Easy DKIM generates.
	dkimTokenCount = 3
)

// Mock is an in-memory implementation of AWS SES v2.
type Mock struct {
	identities *memstore.Store[*identityData]
	configSets *memstore.Store[*configSetData]
	templates  *memstore.Store[*templateData]
	suppressed *memstore.Store[driver.SuppressedDestination]

	contactLists *memstore.Store[*contactListData]
	cvTemplates  *memstore.Store[*driver.CustomVerificationEmailTemplate]
	ipPools      *memstore.Store[*driver.DedicatedIPPool]
	dedicatedIps *memstore.Store[*driver.DedicatedIP]
	testReports  *memstore.Store[*driver.DeliverabilityTestReport]
	importJobs   *memstore.Store[*driver.Job]
	exportJobs   *memstore.Store[*driver.Job]
	tenants      *memstore.Store[*tenantData]
	repEntities  *memstore.Store[*driver.ReputationEntity]
	endpoints    *memstore.Store[*driver.MultiRegionEndpoint]

	sentMu sync.RWMutex
	sent   []driver.SentMessage

	acctMu  sync.RWMutex
	account driver.Account

	dashMu            sync.RWMutex
	dashboardEnabled  bool
	vdmEnabled        bool
	autoWarmupEnabled bool

	opts *config.Options
}

type contactListData struct {
	cl       driver.ContactList
	contacts *memstore.Store[*driver.Contact]
	mu       sync.RWMutex
}

type tenantData struct {
	t         driver.Tenant
	resources *memstore.Store[driver.TenantResource]
	mu        sync.RWMutex
}

type identityData struct {
	id driver.Identity
	mu sync.RWMutex
}

type configSetData struct {
	cs driver.ConfigurationSet
	mu sync.RWMutex
}

type templateData struct {
	tpl driver.Template
	mu  sync.RWMutex
}

// New creates a new SES v2 mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		identities:   memstore.New[*identityData](),
		configSets:   memstore.New[*configSetData](),
		templates:    memstore.New[*templateData](),
		suppressed:   memstore.New[driver.SuppressedDestination](),
		contactLists: memstore.New[*contactListData](),
		cvTemplates:  memstore.New[*driver.CustomVerificationEmailTemplate](),
		ipPools:      memstore.New[*driver.DedicatedIPPool](),
		dedicatedIps: memstore.New[*driver.DedicatedIP](),
		testReports:  memstore.New[*driver.DeliverabilityTestReport](),
		importJobs:   memstore.New[*driver.Job](),
		exportJobs:   memstore.New[*driver.Job](),
		tenants:      memstore.New[*tenantData](),
		repEntities:  memstore.New[*driver.ReputationEntity](),
		endpoints:    memstore.New[*driver.MultiRegionEndpoint](),
		account: driver.Account{
			SendingEnabled:          true,
			ProductionAccessEnabled: false,
			Max24HourSend:           defaultMax24HourSend,
			MaxSendRate:             defaultMaxSendRate,
			SuppressedReasons:       []string{driver.SuppressionReasonBounce, driver.SuppressionReasonComplaint},
			EnforcementStatus:       "HEALTHY",
		},
		opts: opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// isDomainIdentity reports whether name is a bare domain (no local part).
func isDomainIdentity(name string) bool {
	return !strings.Contains(name, "@")
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

// mergeTags copies src into dst (creating dst if nil) and returns the result,
// enforcing the per-resource tag cap.
func mergeTags(dst, src map[string]string) (map[string]string, error) {
	if dst == nil {
		dst = make(map[string]string, len(src))
	}

	for k, v := range src {
		dst[k] = v
	}

	if len(dst) > maxTags {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "resource cannot have more than %d tags", maxTags)
	}

	return dst, nil
}

// dkimTokens returns deterministic-looking DKIM CNAME tokens for a domain.
func dkimTokens(name string) []string {
	toks := make([]string, 0, dkimTokenCount)
	for i := 0; i < dkimTokenCount; i++ {
		toks = append(toks, idgen.GenerateID("")+"."+name)
	}

	return toks
}
