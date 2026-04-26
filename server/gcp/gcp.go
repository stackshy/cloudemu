// Package gcp assembles CloudEmu's GCP-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package gcp

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/gcp/compute"
)

// Drivers bundles the driver interfaces the GCP server can expose. Leave a
// field nil to omit that service; the server returns 501 Not Implemented for
// any request that no registered handler matches.
type Drivers struct {
	Compute computedriver.Compute
}

// New returns a server that speaks GCP's REST JSON wire protocol for every
// non-nil driver in d. Routing is path-based on
//
//	/compute/v1/projects/{p}/zones/{z}/{type}/{name}
//
// so handlers can register independently — Compute Engine doesn't conflict
// with future GCS or Firestore handlers.
//

func New(d Drivers) *server.Server {
	srv := server.New()

	if d.Compute != nil {
		srv.Register(compute.New(d.Compute))
	}

	return srv
}
