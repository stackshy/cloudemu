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
	"github.com/stackshy/cloudemu/server/gcp/gcs"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers bundles the driver interfaces the GCP server can expose.
type Drivers struct {
	Compute computedriver.Compute
	Storage storagedriver.Bucket
}

// New returns a server that speaks GCP's REST JSON wire protocol for every
// non-nil driver in d.
//

func New(d Drivers) *server.Server {
	srv := server.New()

	if d.Compute != nil {
		srv.Register(compute.New(d.Compute))
	}

	if d.Storage != nil {
		srv.Register(gcs.New(d.Storage))
	}

	return srv
}
