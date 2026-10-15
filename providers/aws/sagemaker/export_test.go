package sagemaker

// Test-only seams. Real SageMaker exposes no API to create these states (model
// failures come from the user's container, monitoring alerts from Model Dashboard
// violations), so they are not part of the emulator's production surface.

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// SetInvocationFailure makes every InvokeEndpoint call to the named endpoint
// fail as a model-container error with the given HTTP status (0 clears it).
// It is a test seam: real model failures come from the user's container.
func (m *Mock) SetInvocationFailure(endpoint string, status int) {
	m.invMu.Lock()
	defer m.invMu.Unlock()

	if status == 0 {
		delete(m.invFailures, endpoint)

		return
	}

	m.invFailures[endpoint] = status
}

// PutMonitoringAlert seeds a monitoring alert on a schedule. Real SageMaker
// exposes no API to create one (they come from Model Dashboard), so the emulator
// provides this seed hook for tests and fixtures.
func (m *Mock) PutMonitoringAlert(scheduleName, alertName string, datapointsToAlert, evaluationPeriod int) error {
	if err := validMonitoringName("monitoringAlertName", alertName); err != nil {
		return err
	}

	if err := validateAlertBounds(datapointsToAlert, evaluationPeriod); err != nil {
		return err
	}

	m.monMu.Lock()
	defer m.monMu.Unlock()

	if !m.monSchedules.Has(scheduleName) {
		return errors.Newf(errors.NotFound, "monitoring schedule %q not found", scheduleName)
	}

	now := m.now()
	m.monAlerts.Set(scheduleName+"\x00"+alertName, &driver.MonitoringAlert{
		ScheduleName: scheduleName, Name: alertName, Status: driver.AlertOK, DatapointsToAlert: datapointsToAlert,
		EvaluationPeriod: evaluationPeriod, DashboardIndicator: true, CreationTime: now, LastModifiedTime: now,
	})

	return nil
}

// SetMonitoringExecutionStatus records the outcome of one execution (for
// example Failed or CompletedWithViolations) so alert paths can be exercised.
// scheduledTime is the execution's ScheduledTime as listed.
func (m *Mock) SetMonitoringExecutionStatus(scheduleName, scheduledTime, status, failureReason string) error {
	switch status {
	case driver.MonitorExecCompleted, driver.MonitorExecCompletedViolations, driver.MonitorExecInProgress, driver.MonitorExecFailed,
		driver.MonitorExecPending, driver.MonitorExecStopping, driver.MonitorExecStopped:
	default:
		return errors.Newf(errors.InvalidArgument, "monitoring execution status %q is not a SageMaker status", status)
	}

	m.monMu.Lock()
	defer m.monMu.Unlock()

	for _, e := range m.allExecutions() { //nolint:gocritic // large value params mirror the driver interface
		if e.ScheduleName == scheduleName && e.ScheduledTime == scheduledTime {
			e.Status, e.FailureReason = status, failureReason
			m.monExecs.Set(execKey(scheduleName, parseTime(scheduledTime)), &e)

			return nil
		}
	}

	return errors.Newf(errors.NotFound, "execution of %q at %q not found", scheduleName, scheduledTime)
}
