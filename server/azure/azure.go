// Package azure assembles CloudEmu's Azure-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package azure

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/disks"
	"github.com/stackshy/cloudemu/server/azure/images"
	"github.com/stackshy/cloudemu/server/azure/snapshots"
	"github.com/stackshy/cloudemu/server/azure/sshpublickeys"
	"github.com/stackshy/cloudemu/server/azure/virtualmachines"
)

// Drivers bundles the driver interfaces the Azure server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
//
// VirtualMachines / Disks / Snapshots / Images all delegate to the same
// compute driver — the driver's Volume*/Snapshot*/Image* methods back the
// corresponding resources.
type Drivers struct {
	VirtualMachines computedriver.Compute
	Disks           computedriver.Compute
	Snapshots       computedriver.Compute
	Images          computedriver.Compute
	SSHPublicKeys   computedriver.Compute
}

// New returns a server that speaks the Azure ARM JSON wire protocol for every
// non-nil driver in d. Routing is path-based on
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/...
//
// so handlers can register independently — virtualMachines doesn't conflict
// with future blob storage or networking handlers.
//
//nolint:gocritic // Drivers is all interface fields; by-value keeps the caller API ergonomic
func New(d Drivers) *server.Server {
	srv := server.New()

	// Register more-specific resource handlers first so their resourceType
	// match wins over virtualMachines (which also accepts the locations
	// sub-path used for async-operation polling).
	if d.Disks != nil {
		srv.Register(disks.New(d.Disks))
	}

	if d.Snapshots != nil {
		srv.Register(snapshots.New(d.Snapshots))
	}

	if d.Images != nil {
		srv.Register(images.New(d.Images))
	}

	if d.SSHPublicKeys != nil {
		srv.Register(sshpublickeys.New(d.SSHPublicKeys))
	}

	if d.VirtualMachines != nil {
		srv.Register(virtualmachines.New(d.VirtualMachines))
	}

	return srv
}
