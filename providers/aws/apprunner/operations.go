package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// PauseService moves a RUNNING service to PAUSED and records a PAUSE_SERVICE
// operation. Pausing a service that is not RUNNING is rejected with
// InvalidStateException; an unknown ARN yields ResourceNotFoundException.
func (m *Mock) PauseService(_ context.Context, serviceArn string) (*driver.ServiceResult, error) {
	return m.transition(serviceArn, driver.StatusRunning, driver.StatusPaused, driver.OpPauseService, "paused")
}

// ResumeService moves a PAUSED service back to RUNNING and records a
// RESUME_SERVICE operation. Resuming a service that is not PAUSED is rejected
// with InvalidStateException.
func (m *Mock) ResumeService(_ context.Context, serviceArn string) (*driver.ServiceResult, error) {
	return m.transition(serviceArn, driver.StatusPaused, driver.StatusRunning, driver.OpResumeService, "resumed")
}

// transition applies a guarded status change to a service: it requires the
// current status to equal from, sets it to to, records opType, and returns the
// updated service. A mismatch is an InvalidStateException.
func (m *Mock) transition(serviceArn, from, to, opType, verb string) (*driver.ServiceResult, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, notFound("service %q does not exist", serviceArn)
	}

	if svc.Status != from {
		return nil, invalidState("service %q is in state %s and cannot be %s", serviceArn, svc.Status, verb)
	}

	now := m.now()
	svc.Status = to
	svc.UpdatedAt = now
	appendOperation(&svc, opType, now)
	m.services.Set(serviceArn, svc)

	return serviceResult(&svc), nil
}

// StartDeployment starts a new deployment of a RUNNING service, records a
// START_DEPLOYMENT operation and returns its id. Starting a deployment on a
// service that is not RUNNING is rejected with InvalidStateException.
func (m *Mock) StartDeployment(_ context.Context, serviceArn string) (string, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return "", notFound("service %q does not exist", serviceArn)
	}

	if svc.Status != driver.StatusRunning {
		return "", invalidState("service %q is in state %s and cannot start a deployment", serviceArn, svc.Status)
	}

	now := m.now()
	svc.UpdatedAt = now
	opID := appendOperation(&svc, driver.OpStartDeployment, now)
	m.services.Set(serviceArn, svc)

	return opID, nil
}

// ListOperations returns a deterministic page of a service's operations, newest
// first. An unknown ARN yields ResourceNotFoundException.
func (m *Mock) ListOperations(
	_ context.Context, serviceArn string, page driver.Page,
) ([]driver.Operation, string, error) {
	svc, ok := m.services.Get(serviceArn)
	if !ok {
		return nil, "", notFound("service %q does not exist", serviceArn)
	}

	ordered := reverseOperations(svc.Operations)
	start, end, next := paginate(len(ordered), page)

	return ordered[start:end], next, nil
}

// reverseOperations returns a copy of ops in newest-first order.
func reverseOperations(ops []driver.Operation) []driver.Operation {
	out := make([]driver.Operation, len(ops))
	for i := range ops {
		out[len(ops)-1-i] = ops[i]
	}

	return out
}
