package apprunner

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func (m *Mock) getService(arn string) (*serviceData, error) {
	if arn == "" {
		return nil, invalidRequest("ServiceArn is required")
	}

	sd, ok := m.services.Get(arn)
	if !ok {
		return nil, notFound("no App Runner service found for ARN %q", arn)
	}

	return sd, nil
}

// CreateService registers a new service. App Runner keys services by their
// server-minted ARN (duplicate names are allowed), so SetIfAbsent by ARN is the
// uniqueness invariant. The service comes up RUNNING immediately and records a
// CREATE_SERVICE operation.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.AppRunner interface (by-value input).
func (m *Mock) CreateService(_ context.Context, in driver.CreateServiceInput) (*driver.ServiceResult, error) {
	if in.ServiceName == "" {
		return nil, invalidRequest("ServiceName is required")
	}

	if len(in.SourceConfiguration) == 0 {
		return nil, invalidRequest("SourceConfiguration is required")
	}

	now := m.now()
	id := idgen.GenerateID("")
	arn := m.serviceArn(in.ServiceName, id)

	svc := driver.Service{
		ServiceArn: arn, ServiceID: id, ServiceName: in.ServiceName,
		ServiceURL: id + "." + m.opts.Region + ".awsapprunner.com",
		Status:     driver.ServiceStatusRunning, CreatedAt: now, UpdatedAt: now,
		AutoScalingConfigArn:       in.AutoScalingConfigArn,
		SourceConfiguration:        copyRaw(in.SourceConfiguration),
		InstanceConfiguration:      copyRaw(in.InstanceConfiguration),
		NetworkConfiguration:       copyRaw(in.NetworkConfiguration),
		HealthCheckConfiguration:   copyRaw(in.HealthCheckConfiguration),
		EncryptionConfiguration:    copyRaw(in.EncryptionConfiguration),
		ObservabilityConfiguration: copyRaw(in.ObservabilityConfiguration),
		Tags:                       copyTags(in.Tags),
	}
	m.attachDefaultASC(&svc)

	op := m.newOperation(arn, driver.OperationTypeCreateService, now)
	sd := &serviceData{
		svc: svc, ops: []driver.OperationSummary{op},
		domains: make(map[string]*driver.CustomDomain),
	}

	if !m.services.SetIfAbsent(arn, sd) {
		// ARNs are server-minted and unique; a collision is an internal fault.
		return nil, invalidRequest("service ARN collision for %q", arn)
	}

	out := copyService(&svc)

	return &driver.ServiceResult{Service: &out, OperationID: op.ID}, nil
}

func (m *Mock) DescribeService(_ context.Context, arn string) (*driver.Service, error) {
	sd, err := m.getService(arn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := copyService(&sd.svc)

	return &out, nil
}

// DeleteService moves the service to DELETED and records a DELETE operation. It
// is not removed from the store so DescribeService can still report the deleted
// service (App Runner returns deleted services from DescribeService).
func (m *Mock) DeleteService(_ context.Context, arn string) (*driver.ServiceResult, error) {
	sd, err := m.getService(arn)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.svc.Status == driver.ServiceStatusDeleted {
		return nil, invalidState("service %q is already deleted", arn)
	}

	now := m.now()
	sd.svc.Status = driver.ServiceStatusDeleted
	sd.svc.UpdatedAt = now
	sd.svc.DeletedAt = now
	op := m.newOperation(arn, driver.OperationTypeDeleteService, now)
	sd.ops = append(sd.ops, op)

	out := copyService(&sd.svc)

	return &driver.ServiceResult{Service: &out, OperationID: op.ID}, nil
}

// UpdateService mutates the supplied configuration blocks in place and records
// an UPDATE operation. Empty inputs leave the corresponding block unchanged.
//
//nolint:gocritic // hugeParam: signature is fixed by the driver.AppRunner interface (by-value input).
func (m *Mock) UpdateService(_ context.Context, in driver.UpdateServiceInput) (*driver.ServiceResult, error) {
	sd, err := m.getService(in.ServiceArn)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	applyUpdate(&sd.svc, &in)

	now := m.now()
	sd.svc.UpdatedAt = now
	op := m.newOperation(in.ServiceArn, driver.OperationTypeUpdateService, now)
	sd.ops = append(sd.ops, op)

	out := copyService(&sd.svc)

	return &driver.ServiceResult{Service: &out, OperationID: op.ID}, nil
}

func applyUpdate(svc *driver.Service, in *driver.UpdateServiceInput) {
	if len(in.SourceConfiguration) > 0 {
		svc.SourceConfiguration = copyRaw(in.SourceConfiguration)
	}

	if len(in.InstanceConfiguration) > 0 {
		svc.InstanceConfiguration = copyRaw(in.InstanceConfiguration)
	}

	if len(in.NetworkConfiguration) > 0 {
		svc.NetworkConfiguration = copyRaw(in.NetworkConfiguration)
	}

	if len(in.HealthCheckConfiguration) > 0 {
		svc.HealthCheckConfiguration = copyRaw(in.HealthCheckConfiguration)
	}

	if len(in.ObservabilityConfiguration) > 0 {
		svc.ObservabilityConfiguration = copyRaw(in.ObservabilityConfiguration)
	}

	if in.AutoScalingConfigArn != "" {
		svc.AutoScalingConfigArn = in.AutoScalingConfigArn
	}
}

func (m *Mock) ListServices(
	_ context.Context, nextToken string, maxResults int32,
) ([]driver.Service, string, error) {
	all := m.services.SortedValues()
	out := make([]driver.Service, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		out = append(out, copyService(&sd.svc))
		sd.mu.RUnlock()
	}

	page, token, err := paginate(out, nextToken, maxResults, func(s driver.Service) string { return s.ServiceArn })

	return page, token, err
}

// PauseService moves a RUNNING service to PAUSED. Any other state is an illegal
// transition (InvalidStateException).
func (m *Mock) PauseService(_ context.Context, arn string) (*driver.ServiceResult, error) {
	return m.transition(arn, driver.ServiceStatusRunning, driver.ServiceStatusPaused, driver.OperationTypePauseService)
}

// ResumeService moves a PAUSED service to RUNNING.
func (m *Mock) ResumeService(_ context.Context, arn string) (*driver.ServiceResult, error) {
	return m.transition(arn, driver.ServiceStatusPaused, driver.ServiceStatusRunning, driver.OperationTypeResumeService)
}

func (m *Mock) transition(arn, from, to, opType string) (*driver.ServiceResult, error) {
	sd, err := m.getService(arn)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.svc.Status != from {
		return nil, invalidState("service %q is %s; %s requires %s", arn, sd.svc.Status, opType, from)
	}

	now := m.now()
	sd.svc.Status = to
	sd.svc.UpdatedAt = now
	op := m.newOperation(arn, opType, now)
	sd.ops = append(sd.ops, op)

	out := copyService(&sd.svc)

	return &driver.ServiceResult{Service: &out, OperationID: op.ID}, nil
}

// StartDeployment records a deployment operation on a RUNNING service and
// returns its operation id.
func (m *Mock) StartDeployment(_ context.Context, arn string) (string, error) {
	sd, err := m.getService(arn)
	if err != nil {
		return "", err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	// StartDeployment does not model InvalidStateException (only RNF /
	// InvalidRequest / Internal), so an illegal state is an InvalidRequestException.
	if sd.svc.Status != driver.ServiceStatusRunning {
		return "", invalidRequest("service %q is %s; StartDeployment requires RUNNING", arn, sd.svc.Status)
	}

	op := m.newOperation(arn, driver.OperationTypeStartDeployment, m.now())
	sd.ops = append(sd.ops, op)

	return op.ID, nil
}

// newOperation builds a succeeded operation summary. Deployments complete
// deterministically in the emulator, so operations are recorded as SUCCEEDED.
func (*Mock) newOperation(targetArn, opType string, now time.Time) driver.OperationSummary {
	return driver.OperationSummary{
		ID: idgen.GenerateID(""), Type: opType, Status: driver.OperationStatusSucceeded,
		TargetArn: targetArn, StartedAt: now, EndedAt: now, UpdatedAt: now,
	}
}

func copyService(s *driver.Service) driver.Service {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.SourceConfiguration = copyRaw(s.SourceConfiguration)
	out.InstanceConfiguration = copyRaw(s.InstanceConfiguration)
	out.NetworkConfiguration = copyRaw(s.NetworkConfiguration)
	out.HealthCheckConfiguration = copyRaw(s.HealthCheckConfiguration)
	out.EncryptionConfiguration = copyRaw(s.EncryptionConfiguration)
	out.ObservabilityConfiguration = copyRaw(s.ObservabilityConfiguration)

	return out
}

// paginate returns a deterministic page of items starting after nextToken (an
// opaque key), plus the token to resume from (empty when exhausted). Items must
// already be sorted by key.
func paginate[T any](items []T, nextToken string, maxResults int32, key func(T) string,
) (page []T, next string, err error) {
	size := int(maxResults)
	if size <= 0 {
		size = defaultPageSize
	}

	start := 0
	if nextToken != "" {
		start = sort.Search(len(items), func(i int) bool { return key(items[i]) >= nextToken })
		if start >= len(items) || key(items[start]) != nextToken {
			return nil, "", invalidRequest("invalid NextToken %q", nextToken)
		}
	}

	end := start + size
	if end >= len(items) {
		return items[start:], "", nil
	}

	return items[start:end], key(items[end]), nil
}
