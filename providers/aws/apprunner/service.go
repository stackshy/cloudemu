package apprunner

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// Network configuration defaults App Runner applies to a service.
const (
	egressTypeDefault    = "DEFAULT"
	egressTypeVPC        = "VPC"
	ipAddressTypeDefault = "IPV4"
	ipAddressTypeDual    = "DUAL_STACK"
	defaultConfigName    = "DefaultConfiguration"
)

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// serviceParts are the validated, defaulted configuration blocks of a service.
type serviceParts struct {
	instance      *driver.InstanceConfiguration
	health        *driver.HealthCheckConfiguration
	network       *driver.NetworkConfiguration
	observability *driver.ServiceObservabilityConfiguration
}

// resolveParts validates the supplied blocks and fills the documented defaults.
func (m *Mock) resolveParts(
	instance *driver.InstanceConfiguration, health *driver.HealthCheckConfiguration,
	network *driver.NetworkConfiguration, obs *driver.ServiceObservabilityConfiguration,
) (serviceParts, error) {
	var (
		parts serviceParts
		err   error
	)

	if parts.instance, err = resolveInstanceConfiguration(instance); err != nil {
		return parts, err
	}

	if parts.health, err = resolveHealthCheck(health); err != nil {
		return parts, err
	}

	if parts.network, err = m.resolveNetworkConfiguration(network); err != nil {
		return parts, err
	}

	parts.observability, err = m.resolveObservability(obs)

	return parts, err
}

// CreateService provisions a service with stable computed fields (id, arn, url,
// createdAt), records a CREATE_SERVICE operation and returns it with that
// operation's id. It is RUNNING at once by default; under async settling it
// reports OPERATION_IN_PROGRESS for a short window first. Names are unique per
// account and region, and every referenced configuration must exist.
func (m *Mock) CreateService(ctx context.Context, in *driver.CreateServiceInput) (*driver.ServiceResult, error) {
	if err := validateServiceName(in.ServiceName); err != nil {
		return nil, err
	}

	if err := validateSource(in.SourceConfiguration); err != nil {
		return nil, err
	}

	// refMu spans reference resolution and the insert, so a referenced
	// configuration cannot be deleted in between and leave the service pointing at
	// nothing.
	m.refMu.Lock()
	defer m.refMu.Unlock()

	parts, err := m.resolveParts(in.InstanceConfiguration, in.HealthCheckConfiguration, in.NetworkConfiguration,
		in.ObservabilityConfiguration)
	if err != nil {
		return nil, err
	}

	summary, err := m.resolveAutoScalingSummary(in.AutoScalingConfigurationArn)
	if err != nil {
		return nil, err
	}

	now := m.now()
	id := newID()
	arn := m.serviceARN(in.ServiceName, id)

	svc := driver.Service{
		ServiceName: in.ServiceName, ServiceID: id, ServiceArn: arn, ServiceURL: m.serviceURL(id),
		Status: driver.StatusRunning, CreatedAt: now, UpdatedAt: now,
		SourceConfiguration:             copySourceConfiguration(in.SourceConfiguration),
		InstanceConfiguration:           parts.instance,
		HealthCheckConfiguration:        parts.health,
		NetworkConfiguration:            parts.network,
		ObservabilityConfiguration:      parts.observability,
		EncryptionConfiguration:         copyEncryptionConfig(in.EncryptionConfiguration),
		AutoScalingConfigurationSummary: summary,
		Tags:                            copyTags(in.Tags),
	}

	appendOperation(&svc, driver.OpCreateService, now)

	if m.serviceNameTaken(in.ServiceName) {
		return nil, invalidRequest("a service named " + in.ServiceName + " already exists in this account and region")
	}

	m.services.Set(arn, svc)
	m.beginOperation(arn)
	m.createLogGroups(ctx, &svc)
	m.publishServiceMetrics(&svc)

	return m.serviceResult(&svc), nil
}

// serviceNameTaken reports whether a live service already has the name. The
// caller holds refMu.
func (m *Mock) serviceNameTaken(name string) bool {
	all := m.services.SortedValues()
	for i := range all {
		if all[i].ServiceName == name {
			return true
		}
	}

	return false
}

// resolveNetworkConfiguration validates the supplied network block and fills the
// defaults, while otherwise round-tripping the caller's block. A VPC egress
// needs an existing VPC connector.
func (m *Mock) resolveNetworkConfiguration(in *driver.NetworkConfiguration) (*driver.NetworkConfiguration, error) {
	out := copyNetworkConfiguration(in)
	if out == nil {
		out = &driver.NetworkConfiguration{}
	}

	if out.IPAddressType == "" {
		out.IPAddressType = ipAddressTypeDefault
	}

	if out.IPAddressType != ipAddressTypeDefault && out.IPAddressType != ipAddressTypeDual {
		return nil, invalidRequest("NetworkConfiguration.IpAddressType must be IPV4 or DUAL_STACK")
	}

	if out.EgressConfiguration == nil {
		out.EgressConfiguration = &driver.EgressConfiguration{EgressType: egressTypeDefault}
	}

	if err := m.checkEgress(out.EgressConfiguration); err != nil {
		return nil, err
	}

	// A service is public unless it says otherwise. A present IngressConfiguration
	// whose IsPubliclyAccessible is unset means false: the SDKs drop a false bool
	// from the request, so an empty block is how a private service arrives.
	public := out.IngressConfiguration == nil

	if out.IngressConfiguration == nil {
		out.IngressConfiguration = &driver.IngressConfiguration{}
	}

	if out.IngressConfiguration.IsPubliclyAccessible == nil {
		out.IngressConfiguration.IsPubliclyAccessible = &public
	}

	return out, nil
}

// checkEgress validates an egress block: DEFAULT or VPC, and a VPC egress must
// name a VPC connector that exists.
func (m *Mock) checkEgress(e *driver.EgressConfiguration) error {
	if e.EgressType == "" {
		e.EgressType = egressTypeDefault
	}

	switch e.EgressType {
	case egressTypeDefault:
		return nil
	case egressTypeVPC:
		if e.VpcConnectorArn == "" {
			return invalidRequest("EgressConfiguration.VpcConnectorArn is required for VPC egress")
		}

		if _, ok := m.vpcConnectors.Get(e.VpcConnectorArn); !ok {
			return invalidRequest("VPC connector " + e.VpcConnectorArn + " does not exist")
		}

		return nil
	default:
		return invalidRequest("EgressConfiguration.EgressType must be DEFAULT or VPC")
	}
}

// resolveObservability validates the observability block and requires a
// referenced configuration to exist. An omitted block stays nil (the Terraform
// provider's block is not Computed, so a default would plan a diff forever);
// inside a block that was sent, ObservabilityEnabled defaults to false.
func (m *Mock) resolveObservability(in *driver.ServiceObservabilityConfiguration) (*driver.ServiceObservabilityConfiguration, error) {
	out := copyObservabilityConfig(in)
	if out == nil {
		return nil, nil //nolint:nilnil // an omitted block is stored as absent
	}

	if out.ObservabilityEnabled == nil {
		disabled := false
		out.ObservabilityEnabled = &disabled
	}

	if out.ObservabilityConfigurationArn != "" {
		if _, ok := m.observability.Get(out.ObservabilityConfigurationArn); !ok {
			return nil, invalidRequest("observability configuration " + out.ObservabilityConfigurationArn + " does not exist")
		}
	}

	return out, nil
}

// resolveAutoScalingSummary builds the auto scaling summary reported on a
// service. An empty ARN selects the account's current default configuration; a
// full or partial ARN (name, or name/revision) must name an active configuration.
func (m *Mock) resolveAutoScalingSummary(arn string) (*driver.AutoScalingConfigurationSummary, error) {
	cfg, err := m.resolveAutoScaling(arn)
	if err != nil {
		return nil, err
	}

	return &driver.AutoScalingConfigurationSummary{
		AutoScalingConfigurationArn:      cfg.AutoScalingConfigurationArn,
		AutoScalingConfigurationName:     cfg.AutoScalingConfigurationName,
		AutoScalingConfigurationRevision: cfg.AutoScalingConfigurationRevision,
	}, nil
}

// appendOperation records a mutating operation on a service, minting a stable id
// and marking it SUCCEEDED (the emulator completes operations synchronously).
func appendOperation(svc *driver.Service, opType string, now time.Time) string {
	opID := idgen.UUID()
	svc.Operations = append(svc.Operations, driver.Operation{
		ID: opID, Type: opType, Status: driver.OpStatusSucceeded, TargetArn: svc.ServiceArn,
		StartedAt: now, EndedAt: now, UpdatedAt: now,
	})

	return opID
}

// beginOperation opens the transient OPERATION_IN_PROGRESS window of a service;
// it is a no-op unless async settling is enabled.
func (m *Mock) beginOperation(serviceArn string) {
	m.settling.Begin(serviceArn, driver.StatusOperationInProgress, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultClusterSettle))
}

// operationInProgress reports whether a service is inside its settle window.
func (m *Mock) operationInProgress(serviceArn string) bool {
	return !m.settling.Settled(serviceArn, m.opts.Clock.Now())
}

// viewService copies a service with its status and latest operation overlaid by
// the settle window.
func (m *Mock) viewService(svc *driver.Service) driver.Service {
	out := copyService(svc)

	if m.operationInProgress(svc.ServiceArn) && svc.Status != driver.StatusDeleted {
		out.Status = driver.StatusOperationInProgress

		if n := len(out.Operations); n > 0 {
			out.Operations[n-1].Status = driver.OpStatusInProgress
			out.Operations[n-1].EndedAt = time.Time{} // an operation in progress has not ended
		}
	}

	return out
}

// serviceResult pairs a view of a service with the id of its most recent
// operation, matching App Runner's {Service, OperationId} responses.
func (m *Mock) serviceResult(svc *driver.Service) *driver.ServiceResult {
	out := m.viewService(svc)
	opID := ""

	if n := len(svc.Operations); n > 0 {
		opID = svc.Operations[n-1].ID
	}

	return &driver.ServiceResult{Service: &out, OperationID: opID}
}

// requireIdle rejects an operation on a service that is still applying another
// one (InvalidStateException).
func (m *Mock) requireIdle(svc *driver.Service) error {
	if m.operationInProgress(svc.ServiceArn) {
		return invalidState("service %q is in state %s and cannot accept another operation", svc.ServiceArn,
			driver.StatusOperationInProgress)
	}

	return nil
}

// DescribeService returns the service by ARN, or a ResourceNotFoundException.
func (m *Mock) DescribeService(_ context.Context, serviceArn string) (*driver.Service, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	out := m.viewService(&svc)

	return &out, nil
}

// UpdateService replaces the members present in the request (validated like a
// create), advances UpdatedAt and records an UPDATE_SERVICE operation. The
// service reports OPERATION_IN_PROGRESS under async settling and is rejected
// with InvalidStateException while another operation is still applying.
func (m *Mock) UpdateService(_ context.Context, in *driver.UpdateServiceInput) (*driver.ServiceResult, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	upd, err := m.resolveUpdate(in)
	if err != nil {
		return nil, err
	}

	svc, ok := m.services.Get(in.ServiceArn)
	if !ok {
		return nil, notFound("service %q does not exist", in.ServiceArn)
	}

	if err := m.requireIdle(&svc); err != nil {
		return nil, err
	}

	applyServiceUpdate(&svc, in, &upd)

	now := m.now()
	svc.UpdatedAt = now
	appendOperation(&svc, driver.OpUpdateService, now)
	m.services.Set(in.ServiceArn, svc)
	m.beginOperation(in.ServiceArn)

	return m.serviceResult(&svc), nil
}

// updateParts are the validated members of an UpdateService request; a nil
// member was not supplied.
type updateParts struct {
	instance      *driver.InstanceConfiguration
	health        *driver.HealthCheckConfiguration
	network       *driver.NetworkConfiguration
	observability *driver.ServiceObservabilityConfiguration
	autoScaling   *driver.AutoScalingConfigurationSummary
}

// resolveUpdate validates and defaults only the members the request supplies.
func (m *Mock) resolveUpdate(in *driver.UpdateServiceInput) (updateParts, error) {
	var (
		upd updateParts
		err error
	)

	if in.SourceConfiguration != nil {
		if verr := validateSource(in.SourceConfiguration); verr != nil {
			return upd, verr
		}
	}

	if upd.instance, upd.health, err = resolveUpdateSizing(in); err != nil {
		return upd, err
	}

	if in.NetworkConfiguration != nil {
		if upd.network, err = m.resolveNetworkConfiguration(in.NetworkConfiguration); err != nil {
			return upd, err
		}
	}

	if in.ObservabilityConfiguration != nil {
		if upd.observability, err = m.resolveObservability(in.ObservabilityConfiguration); err != nil {
			return upd, err
		}
	}

	if in.AutoScalingConfigurationArn != "" {
		upd.autoScaling, err = m.resolveAutoScalingSummary(in.AutoScalingConfigurationArn)
	}

	return upd, err
}

// resolveUpdateSizing validates the instance and health check members of an update.
func resolveUpdateSizing(in *driver.UpdateServiceInput) (
	instance *driver.InstanceConfiguration, health *driver.HealthCheckConfiguration, err error,
) {
	if in.InstanceConfiguration != nil {
		if instance, err = resolveInstanceConfiguration(in.InstanceConfiguration); err != nil {
			return nil, nil, err
		}
	}

	if in.HealthCheckConfiguration != nil {
		if health, err = resolveHealthCheck(in.HealthCheckConfiguration); err != nil {
			return nil, nil, err
		}
	}

	return instance, health, nil
}

// applyServiceUpdate overlays the supplied members of an update onto a service.
func applyServiceUpdate(svc *driver.Service, in *driver.UpdateServiceInput, upd *updateParts) {
	if in.SourceConfiguration != nil {
		svc.SourceConfiguration = copySourceConfiguration(in.SourceConfiguration)
	}

	if upd.instance != nil {
		svc.InstanceConfiguration = upd.instance
	}

	if upd.health != nil {
		svc.HealthCheckConfiguration = upd.health
	}

	if upd.network != nil {
		svc.NetworkConfiguration = upd.network
	}

	if upd.observability != nil {
		svc.ObservabilityConfiguration = upd.observability
	}

	if upd.autoScaling != nil {
		svc.AutoScalingConfigurationSummary = upd.autoScaling
	}
}

// DeleteService removes a service (and the custom domains and VPC ingress
// connections attached to it) and returns its identity with a DELETED status, so
// a subsequent describe returns ResourceNotFoundException and an IaC
// delete-waiter completes.
func (m *Mock) DeleteService(_ context.Context, serviceArn string) (*driver.ServiceResult, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	if err := m.requireIdle(&svc); err != nil {
		return nil, err
	}

	now := m.now()
	svc.Status = driver.StatusDeleted
	svc.DeletedAt = now
	svc.UpdatedAt = now
	appendOperation(&svc, driver.OpDeleteService, now)
	m.services.Delete(serviceArn)
	m.settling.Clear(serviceArn)
	m.cascadeService(serviceArn)

	return m.serviceResult(&svc), nil
}

// ListServices returns a deterministic page of services ordered by ARN.
func (m *Mock) ListServices(_ context.Context, page driver.Page) ([]*driver.Service, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	stored := m.services.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Service, 0, end-start)

	for i := start; i < end; i++ {
		s := m.viewService(&stored[i])
		out = append(out, &s)
	}

	return out, next, nil
}
