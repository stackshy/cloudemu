package topology

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	iamdriver "github.com/stackshy/cloudemu/iam/driver"
	lbdriver "github.com/stackshy/cloudemu/loadbalancer/driver"
	mqdriver "github.com/stackshy/cloudemu/messagequeue/driver"
	monitoringdriver "github.com/stackshy/cloudemu/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
)

// Engine evaluates network topology and connectivity between cloud resources.
type Engine struct {
	compute    computedriver.Compute
	networking netdriver.Networking
	dns        dnsdriver.DNS

	// Optional cross-service drivers.
	loadbalancer lbdriver.LoadBalancer
	serverless   serverlessdriver.Serverless
	messagequeue mqdriver.MessageQueue
	monitoring   monitoringdriver.Monitoring
	iam          iamdriver.IAM
	provider     string
}

// Option configures optional Engine capabilities.
type Option func(*Engine)

// WithLoadBalancer adds load balancer support to the topology engine.
func WithLoadBalancer(lb lbdriver.LoadBalancer) Option {
	return func(e *Engine) { e.loadbalancer = lb }
}

// WithServerless adds serverless function support to the topology engine.
func WithServerless(s serverlessdriver.Serverless) Option {
	return func(e *Engine) { e.serverless = s }
}

// WithMessageQueue adds message queue support to the topology engine.
func WithMessageQueue(mq mqdriver.MessageQueue) Option {
	return func(e *Engine) { e.messagequeue = mq }
}

// WithMonitoring adds monitoring support to the topology engine.
func WithMonitoring(m monitoringdriver.Monitoring) Option {
	return func(e *Engine) { e.monitoring = m }
}

// WithIAM adds IAM support to the topology engine.
func WithIAM(i iamdriver.IAM) Option {
	return func(e *Engine) { e.iam = i }
}

// WithProvider sets the provider name used in ResourceRef.Provider fields.
func WithProvider(name string) Option {
	return func(e *Engine) { e.provider = name }
}

// New creates a new topology Engine that reads state from the provided
// compute, networking, and DNS services. Additional drivers can be added
// via functional options.
func New(
	compute computedriver.Compute,
	networking netdriver.Networking,
	dns dnsdriver.DNS,
	opts ...Option,
) *Engine {
	e := &Engine{
		compute:    compute,
		networking: networking,
		dns:        dns,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}
