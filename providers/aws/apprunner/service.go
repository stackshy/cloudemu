package apprunner

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// Network configuration defaults App Runner applies to a service.
const (
	egressTypeDefault    = "DEFAULT"
	ipAddressTypeDefault = "IPV4"
	defaultConfigName    = "DefaultConfiguration"
)

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// CreateService provisions a service synchronously into the terminal RUNNING
// state with stable computed fields (id, arn, url, createdAt), records a
// CREATE_SERVICE operation, and returns it with that operation's id.
func (m *Mock) CreateService(_ context.Context, in *driver.CreateServiceInput) (*driver.ServiceResult, error) {
	if in.ServiceName == "" {
		return nil, invalidRequest("ServiceName is required")
	}

	if in.SourceConfiguration == nil {
		return nil, invalidRequest("SourceConfiguration is required")
	}

	now := m.now()
	id := newID()
	arn := m.serviceARN(in.ServiceName, id)

	svc := driver.Service{
		ServiceName:                     in.ServiceName,
		ServiceID:                       id,
		ServiceArn:                      arn,
		ServiceURL:                      m.serviceURL(id),
		Status:                          driver.StatusRunning,
		CreatedAt:                       now,
		UpdatedAt:                       now,
		SourceConfiguration:             copySourceConfiguration(in.SourceConfiguration),
		InstanceConfiguration:           copyInstanceConfiguration(in.InstanceConfiguration),
		HealthCheckConfiguration:        copyHealthCheck(in.HealthCheckConfiguration),
		NetworkConfiguration:            resolveNetworkConfiguration(in.NetworkConfiguration),
		ObservabilityConfiguration:      copyObservabilityConfig(in.ObservabilityConfiguration),
		EncryptionConfiguration:         copyEncryptionConfig(in.EncryptionConfiguration),
		AutoScalingConfigurationSummary: m.resolveAutoScalingSummary(in.AutoScalingConfigurationArn),
		Tags:                            copyTags(in.Tags),
	}

	appendOperation(&svc, driver.OpCreateService, now)
	m.services.Set(arn, svc)

	return serviceResult(&svc), nil
}

// resolveNetworkConfiguration fills the App Runner defaults for any unset member
// so reads are stable, while otherwise round-tripping the caller's block.
func resolveNetworkConfiguration(in *driver.NetworkConfiguration) *driver.NetworkConfiguration {
	out := copyNetworkConfiguration(in)
	if out == nil {
		out = &driver.NetworkConfiguration{}
	}

	if out.IPAddressType == "" {
		out.IPAddressType = ipAddressTypeDefault
	}

	if out.EgressConfiguration == nil {
		out.EgressConfiguration = &driver.EgressConfiguration{EgressType: egressTypeDefault}
	}

	if out.IngressConfiguration == nil {
		public := true
		out.IngressConfiguration = &driver.IngressConfiguration{IsPubliclyAccessible: &public}
	}

	return out
}

// resolveAutoScalingSummary builds the auto scaling summary reported on a
// service. A caller-supplied ARN is echoed; otherwise a stable default-config
// summary is minted so reads never drift.
func (m *Mock) resolveAutoScalingSummary(arn string) *driver.AutoScalingConfigurationSummary {
	if arn != "" {
		name, revision := autoScalingRefFromARN(arn)

		return &driver.AutoScalingConfigurationSummary{
			AutoScalingConfigurationArn:      arn,
			AutoScalingConfigurationName:     name,
			AutoScalingConfigurationRevision: revision,
		}
	}

	id := newID()

	return &driver.AutoScalingConfigurationSummary{
		AutoScalingConfigurationArn:      m.autoScalingARN(defaultConfigName, firstRevision, id),
		AutoScalingConfigurationName:     defaultConfigName,
		AutoScalingConfigurationRevision: firstRevision,
	}
}

// appendOperation records a mutating operation on a service, minting a stable id
// and marking it SUCCEEDED (the emulator completes operations synchronously).
func appendOperation(svc *driver.Service, opType string, now time.Time) string {
	opID := idgen.UUID()
	svc.Operations = append(svc.Operations, driver.Operation{
		ID:        opID,
		Type:      opType,
		Status:    driver.OpStatusSucceeded,
		TargetArn: svc.ServiceArn,
		StartedAt: now,
		EndedAt:   now,
		UpdatedAt: now,
	})

	return opID
}

// serviceResult pairs a copy of a service with the id of its most recent
// operation, matching App Runner's {Service, OperationId} responses.
func serviceResult(svc *driver.Service) *driver.ServiceResult {
	out := copyService(svc)
	opID := ""

	if n := len(svc.Operations); n > 0 {
		opID = svc.Operations[n-1].ID
	}

	return &driver.ServiceResult{Service: &out, OperationID: opID}
}

// DescribeService returns the service by ARN, or a ResourceNotFoundException.
func (m *Mock) DescribeService(_ context.Context, serviceArn string) (*driver.Service, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	out := copyService(&svc)

	return &out, nil
}

// UpdateService replaces the members present in the request, advances UpdatedAt,
// records an UPDATE_SERVICE operation and keeps the service RUNNING.
func (m *Mock) UpdateService(_ context.Context, in *driver.UpdateServiceInput) (*driver.ServiceResult, error) {
	svc, ok := m.services.Get(in.ServiceArn)
	if !ok {
		return nil, notFound("service %q does not exist", in.ServiceArn)
	}

	applyServiceUpdate(&svc, in)

	now := m.now()
	svc.UpdatedAt = now
	appendOperation(&svc, driver.OpUpdateService, now)
	m.services.Set(in.ServiceArn, svc)

	return serviceResult(&svc), nil
}

// applyServiceUpdate overlays the non-nil members of an update onto a service.
func applyServiceUpdate(svc *driver.Service, in *driver.UpdateServiceInput) {
	if in.SourceConfiguration != nil {
		svc.SourceConfiguration = copySourceConfiguration(in.SourceConfiguration)
	}

	if in.InstanceConfiguration != nil {
		svc.InstanceConfiguration = copyInstanceConfiguration(in.InstanceConfiguration)
	}

	if in.HealthCheckConfiguration != nil {
		svc.HealthCheckConfiguration = copyHealthCheck(in.HealthCheckConfiguration)
	}

	if in.NetworkConfiguration != nil {
		svc.NetworkConfiguration = resolveNetworkConfiguration(in.NetworkConfiguration)
	}

	if in.ObservabilityConfiguration != nil {
		svc.ObservabilityConfiguration = copyObservabilityConfig(in.ObservabilityConfiguration)
	}
}

// DeleteService removes a service and returns its identity with a DELETED
// status, so a subsequent describe returns ResourceNotFoundException and an IaC
// delete-waiter completes.
func (m *Mock) DeleteService(_ context.Context, serviceArn string) (*driver.ServiceResult, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	now := m.now()
	svc.Status = driver.StatusDeleted
	svc.DeletedAt = now
	svc.UpdatedAt = now
	appendOperation(&svc, driver.OpDeleteService, now)
	m.services.Delete(serviceArn)

	return serviceResult(&svc), nil
}

// ListServices returns a deterministic page of services ordered by ARN.
func (m *Mock) ListServices(_ context.Context, page driver.Page) ([]*driver.Service, string, error) {
	stored := m.services.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Service, 0, end-start)

	for i := start; i < end; i++ {
		s := copyService(&stored[i])
		out = append(out, &s)
	}

	return out, next, nil
}
