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
	"github.com/stackshy/cloudemu/server/azure/virtualmachines"
)

// Drivers bundles the driver interfaces the Azure server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
type Drivers struct {
	VirtualMachines computedriver.Compute
}

// New returns a server that speaks the Azure ARM JSON wire protocol for every
// non-nil driver in d. Routing is path-based on
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/...
//
// so handlers can register independently — virtualMachines doesn't conflict
// with future blob storage or networking handlers.
//

func New(d Drivers) *server.Server {
	srv := server.New()

	if d.VirtualMachines != nil {
		srv.Register(virtualmachines.New(d.VirtualMachines))
	}

	return srv
}
