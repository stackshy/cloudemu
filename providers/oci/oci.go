// Package oci provides OCI mock provider factories.
package oci

import (
	"github.com/stackshy/cloudemu/v2/config"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
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

// Provider holds all OCI mock services. Fields are driver interfaces rather
// than concrete mocks, so a service not yet implemented is simply nil.
type Provider struct {
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

	ResourceDiscovery *resourcediscovery.Engine

	// Identity this provider was created with, so callers (e.g. a standalone
	// server) can wire them without re-reading the options.
	TenancyOCID   string
	CompartmentID string
	Realm         string
	Region        string
}

// monitoringAware is implemented by mocks that push metrics into the
// monitoring service, discovered by type assertion since the portable driver
// interfaces do not declare it.
type monitoringAware interface {
	SetMonitoring(mon mondriver.Monitoring)
}

// New creates a new OCI provider with all mock services.
func New(opts ...config.Option) *Provider {
	o := config.NewOptions(opts...)
	p := &Provider{
		TenancyOCID:   o.TenancyOCID,
		CompartmentID: o.CompartmentID,
		Realm:         o.Realm,
		Region:        o.OCIRegion(),
	}

	p.wireMonitoring()
	p.wireDiscovery()

	return p
}

// wireMonitoring points every metric-producing mock at the monitoring service.
func (p *Provider) wireMonitoring() {
	if p.Monitoring == nil {
		return
	}

	for _, svc := range []any{
		p.ObjectStorage, p.Compute, p.NoSQL, p.Functions, p.Queue,
		p.Cache, p.Logging, p.Notifications, p.ContainerRegistry,
		p.Events, p.Database,
	} {
		if aware, ok := svc.(monitoringAware); ok {
			aware.SetMonitoring(p.Monitoring)
		}
	}
}

// wireDiscovery builds the resource discovery engine over the mock services.
func (p *Provider) wireDiscovery() {
	p.ResourceDiscovery = resourcediscovery.New(
		resourcediscovery.ProviderOCI, p.CompartmentID, p.Region,
		&resourcediscovery.Drivers{
			Compute:    p.Compute,
			Networking: p.VCN,
			Storage:    p.ObjectStorage,
			Database:   p.NoSQL,
			Serverless: p.Functions,
		},
	)
}
