// Package transfer provides an in-memory mock implementation of AWS Transfer
// Family: the file-transfer control plane. It models servers (the
// SFTP/FTPS/FTP/AS2 endpoints), the users hosted on a SERVICE_MANAGED server,
// the SSH public keys imported for those users, and resource tagging.
//
// This is a control-plane emulator only — there is no real SFTP/FTPS data
// plane, so no files are transferred. A created server settles to State ONLINE
// synchronously (the Terraform aws_transfer_server resource treats state as
// Computed and does not wait), and StartServer/StopServer flip the state
// directly.
package transfer

import (
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// Compile-time check that Mock implements driver.Transfer.
var _ driver.Transfer = (*Mock)(nil)

// userKeySep separates the server id and user name in the users store key.
const userKeySep = "/"

// Mock is an in-memory implementation of AWS Transfer Family.
type Mock struct {
	// servers is keyed by ServerID ("s-<17hex>").
	servers *memstore.Store[driver.Server]
	// users is keyed by "<serverId>/<userName>".
	users *memstore.Store[driver.User]

	// mu serializes compound read-modify-write mutations that span more than one
	// store operation (server UserCount bookkeeping on user create/delete, and
	// server delete cascading to its users).
	mu sync.Mutex

	// tagsMu guards the resource-tag side map, keyed by resource ARN.
	tagsMu sync.RWMutex
	tags   map[string]map[string]string

	opts *config.Options
}

// New creates a new Transfer Family mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		servers: memstore.New[driver.Server](),
		users:   memstore.New[driver.User](),
		tags:    map[string]map[string]string{},
		opts:    opts,
	}
}

// userKey builds the composite key for a user within a server.
func userKey(serverID, userName string) string { return serverID + userKeySep + userName }

// serverARN builds the ARN for a server, matching the ARN the Terraform AWS
// provider computes.
func (m *Mock) serverARN(serverID string) string {
	return idgen.AWSARN("transfer", m.opts.Region, m.opts.AccountID, "server/"+serverID)
}

// userARN builds the ARN for a user hosted on a server.
func (m *Mock) userARN(serverID, userName string) string {
	return idgen.AWSARN("transfer", m.opts.Region, m.opts.AccountID, "user/"+serverID+"/"+userName)
}
