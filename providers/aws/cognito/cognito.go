// Package cognito provides an in-memory mock of AWS Cognito user pools
// (cognito-idp): user pools, their app clients, hosted-UI domains, resource
// tagging, pool users with the admin user-management operations, groups, self
// sign-up with confirmation codes, and password sign-in that issues RS256
// tokens a real JWT library verifies against the pool's JWKS.
package cognito

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Compile-time checks that Mock implements driver.Cognito and the non-API
// interfaces the wire layer uses.
var (
	_ driver.Cognito        = (*Mock)(nil)
	_ driver.KeySetProvider = (*Mock)(nil)
	_ driver.CodeInspector  = (*Mock)(nil)
)

// clientKeySep separates the user-pool id and client id in the clients store.
const clientKeySep = "/"

// Mock is an in-memory implementation of the AWS Cognito user-pools control
// plane.
type Mock struct {
	// userPools is keyed by pool id; clients is keyed by "<poolID>/<clientID>";
	// domains is keyed by the domain string; users is keyed by
	// "<poolID>/<username>"; groups is keyed by "<poolID>/<groupName>"; logins
	// holds one record per sign-in, keyed by its origin_jti.
	userPools *memstore.Store[driver.UserPool]
	clients   *memstore.Store[driver.UserPoolClient]
	domains   *memstore.Store[driver.UserPoolDomain]
	users     *memstore.Store[userRecord]
	groups    *memstore.Store[driver.Group]
	logins    *memstore.Store[loginRecord]

	// mu serializes compound read-modify-write mutations (pool update, cascading
	// pool delete, user changes) that span more than one store operation.
	mu sync.Mutex

	// tagsMu guards the resource-tag side map, keyed by resource ARN.
	tagsMu sync.RWMutex
	tags   map[string]map[string]string

	// keysMu guards the per-pool signing keys, created on first use.
	keysMu sync.Mutex
	keys   map[string]*poolKeys

	// sessionsMu guards the challenge sessions. They last minutes, so they
	// are not persisted.
	sessionsMu sync.Mutex
	sessions   map[string]challengeSession

	opts *config.Options
}

// New creates a new Cognito mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		userPools: memstore.New[driver.UserPool](),
		clients:   memstore.New[driver.UserPoolClient](),
		domains:   memstore.New[driver.UserPoolDomain](),
		users:     memstore.New[userRecord](),
		groups:    memstore.New[driver.Group](),
		logins:    memstore.New[loginRecord](),
		tags:      map[string]map[string]string{},
		keys:      map[string]*poolKeys{},
		sessions:  map[string]challengeSession{},
		opts:      opts,
	}
}

func (m *Mock) now() time.Time { return m.opts.Clock.Now().UTC() }

// clientKey builds the composite key for a client within a user pool.
func clientKey(userPoolID, clientID string) string {
	return userPoolID + clientKeySep + clientID
}

// userPoolARN builds the ARN a pool's tags are keyed under, matching the ARN the
// Terraform AWS provider computes for the pool.
func (m *Mock) userPoolARN(id string) string {
	return idgen.AWSARN("cognito-idp", m.opts.Region, m.opts.AccountID, "userpool/"+id)
}
