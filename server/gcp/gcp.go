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
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	rdbdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/gcp/cloudfunctions"
	"github.com/stackshy/cloudemu/server/gcp/cloudsql"
	"github.com/stackshy/cloudemu/server/gcp/compute"
	"github.com/stackshy/cloudemu/server/gcp/firestore"
	"github.com/stackshy/cloudemu/server/gcp/gcs"
	"github.com/stackshy/cloudemu/server/gcp/monitoring"
	"github.com/stackshy/cloudemu/server/gcp/networks"
	"github.com/stackshy/cloudemu/server/gcp/pubsub"
	sdrv "github.com/stackshy/cloudemu/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/storage/driver"
)

// Drivers bundles the driver interfaces the GCP server can expose.
type Drivers struct {
	Compute        computedriver.Compute
	Storage        storagedriver.Bucket
	Firestore      dbdriver.Database
	Networking     netdriver.Networking
	Monitoring     mondriver.Monitoring
	CloudFunctions sdrv.Serverless
	PubSub         mqdriver.MessageQueue
	CloudSQL       rdbdriver.RelationalDB
}

// New returns a server that speaks GCP's REST JSON wire protocol for every
// non-nil driver in d.
//
// GCS's Matches() also accepts /{bucket}/{object} for direct-media downloads,
// which is broad enough to swallow Firestore and Cloud Monitoring traffic if
// it registers first. Register more-specific handlers (compute, networks,
// firestore, monitoring) ahead of GCS so first-match-wins keeps each on the
// correct package.
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

	// CloudFunctions matches /v1/projects/{p}/locations/{l}/functions paths
	// before Firestore so the locations+functions guard wins over Firestore's
	// /v1/projects/ prefix match.
	if d.CloudFunctions != nil {
		srv.Register(cloudfunctions.New(d.CloudFunctions))
	}

	// PubSub matches /v1/projects/{p}/{topics|subscriptions}/...; register
	// before Firestore so its more-specific resource-type guard wins over
	// Firestore's permissive /v1/projects/ prefix.
	if d.PubSub != nil {
		srv.Register(pubsub.New(d.PubSub))
	}

	// Cloud SQL matches /v1/projects/{p}/{instances|operations}/...; same
	// /v1/projects/ space as Firestore, so register first.
	if d.CloudSQL != nil {
		srv.Register(cloudsql.New(d.CloudSQL))
	}

	if d.Firestore != nil {
		srv.Register(firestore.New(d.Firestore))
	}

	if d.Monitoring != nil {
		srv.Register(monitoring.New(d.Monitoring))
	}

	if d.Storage != nil {
		srv.Register(gcs.New(d.Storage))
	}

	return srv
}
