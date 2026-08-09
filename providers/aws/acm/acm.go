// Package acm provides an in-memory mock implementation of AWS Certificate
// Manager, issuing real self-signed X.509 certificates so GetCertificate and
// ExportCertificate return usable PEM material.
package acm

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

// Compile-time check that Mock implements driver.ACM.
var _ driver.ACM = (*Mock)(nil)

const defaultDaysBeforeExpiry = 45

// Mock is an in-memory implementation of AWS ACM.
type Mock struct {
	certs *memstore.Store[*certData]

	cfgMu     sync.RWMutex
	accountFg driver.AccountConfiguration

	opts *config.Options
}

// certData is a certificate plus its own lock.
type certData struct {
	cert driver.Certificate
	mu   sync.RWMutex
}

// New creates a new ACM mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		certs:     memstore.New[*certData](),
		accountFg: driver.AccountConfiguration{DaysBeforeExpiry: defaultDaysBeforeExpiry},
		opts:      opts,
	}
}

func (m *Mock) certARN() string {
	return idgen.AWSARN("acm", m.opts.Region, m.opts.AccountID, "certificate/"+idgen.GenerateID(""))
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

func (m *Mock) getCert(arn string) (*certData, error) {
	cd, ok := m.certs.Get(arn)
	if !ok {
		return nil, errNotFound(arn)
	}

	return cd, nil
}

func copyTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
