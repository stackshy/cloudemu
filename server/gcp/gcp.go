// Package gcp assembles CloudEmu's GCP-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package gcp

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/gcp/compute"
	"github.com/stackshy/cloudemu/server/gcp/firestore"
	"github.com/stackshy/cloudemu/server/gcp/gcs"
	"github.com/stackshy/cloudemu/server/gcp/monitoring"
	"github.com/stackshy/cloudemu/server/gcp/networks"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers bundles the driver interfaces the GCP server can expose.
type Drivers struct {
	Compute    computedriver.Compute
	Storage    storagedriver.Bucket
	Firestore  dbdriver.Database
	Networking netdriver.Networking
	Monitoring mondriver.Monitoring
}

// New returns a server that speaks GCP's REST JSON wire protocol for every
// non-nil driver in d.
//
//nolint:gocritic // Drivers is all interface fields; by-value keeps the caller API ergonomic
func New(d Drivers) *server.Server {
	srv := server.New()

	if d.Compute != nil {
		srv.Register(compute.New(d.Compute))
	}

	if d.Networking != nil {
		srv.Register(networks.New(d.Networking))
	}

	if d.Storage != nil {
		srv.Register(gcs.New(d.Storage))
	}

	if d.Firestore != nil {
		srv.Register(firestore.New(d.Firestore))
	}

	if d.Monitoring != nil {
		srv.Register(monitoring.New(d.Monitoring))
	}

	return srv
}
