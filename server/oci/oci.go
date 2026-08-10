// Package oci assembles CloudEmu's OCI-compatible HTTP server.
//
// New takes a Drivers bundle and returns a *server.Server preloaded with the
// handler for each non-nil driver. Consumers that want a single service can
// skip this package and register the handler directly on their own
// server.Server.
package oci

import (
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/oci/identity"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Drivers bundles the driver interfaces the OCI server can expose.
type Drivers struct {
	ObjectStorage     storagedriver.Bucket
	Compute           computedriver.Compute
	NoSQL             dbdriver.Database
	Functions         serverlessdriver.Serverless
	VCN               netdriver.Networking
	Monitoring        mondriver.Monitoring
	Identity          iamdriver.IAM
	DNS               dnsdriver.DNS
	LoadBalancer      lbdriver.LoadBalancer
	Queue             mqdriver.MessageQueue
	Cache             cachedriver.Cache
	Vault             secretsdriver.Secrets
	Logging           logdriver.Logging
	Notifications     notifdriver.Notification
	ContainerRegistry crdriver.ContainerRegistry
	Events            ebdriver.EventBus
	Database          rdbdriver.RelationalDB
	// K8sAPI is the shared in-memory Kubernetes data-plane API server, shared
	// with the AWS, Azure and GCP bundles so a kubeconfig issued by any
	// control plane reaches the same backend. Leave nil to disable it.
	K8sAPI *kubernetes.APIServer
	// ResourceDiscovery backs OCI Resource Search. Leave nil to omit it.
	ResourceDiscovery *resourcediscovery.Engine
	// WorkRequests backs the shared work request poller. New creates one when
	// it is nil, so handlers can always record asynchronous operations.
	WorkRequests *workrequest.Store

	TenancyOCID   string
	CompartmentID string
	Realm         string
	Region        string
}

// New returns a server that speaks OCI's REST JSON wire protocol for every
// non-nil driver in d.
//
//nolint:gocritic // Drivers is all interface fields; the bundle is passed by value like the other providers'.
func New(d Drivers) *server.Server {
	if d.WorkRequests == nil {
		d.WorkRequests = workrequest.New(config.NewOptions(d.identityOptions()...))
	}

	srv := server.New()

	// Registered first so it owns every work request poll uniformly, rather
	// than a service handler greedily claiming polls it did not create.
	srv.Register(workrequest.NewHandler(d.WorkRequests))

	if d.Identity != nil {
		srv.Register(identity.New(d.Identity, d.WorkRequests))
	}

	return srv
}

// identityOptions turns the bundle's identity fields into config options,
// skipping empty ones so they keep their defaults.
//
//nolint:gocritic // matches New's by-value Drivers.
func (d Drivers) identityOptions() []config.Option {
	var opts []config.Option

	if d.Region != "" {
		opts = append(opts, config.WithRegion(d.Region))
	}

	if d.Realm != "" {
		opts = append(opts, config.WithRealm(d.Realm))
	}

	if d.TenancyOCID != "" {
		opts = append(opts, config.WithTenancyOCID(d.TenancyOCID))
	}

	if d.CompartmentID != "" {
		opts = append(opts, config.WithCompartmentID(d.CompartmentID))
	}

	return opts
}
