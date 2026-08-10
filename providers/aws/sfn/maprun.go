package sfn

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// SeedMapRun creates a Map Run record for the given execution and returns its
// ARN. The emulator does not interpret Amazon States Language, so distributed-
// map runs are never produced by executing a state machine; this helper lets
// callers and tests populate a Map Run for DescribeMapRun / ListMapRuns /
// UpdateMapRun to operate on. It is not part of the SFN wire API.
//
//nolint:gocritic // run is passed by value: SeedMapRun takes ownership of the seed record and mutates a copy.
func (m *Mock) SeedMapRun(executionArn string, run driver.MapRun) (string, error) {
	ed, err := m.getExec(executionArn)
	if err != nil {
		return "", err
	}

	ed.mu.RLock()
	smArn := ed.exec.StateMachineArn
	smName := smNameFromARN(smArn)
	execName := ed.exec.Name
	ed.mu.RUnlock()

	arn := m.mapRunARN(smName, execName, idgen.GenerateID("mr-"))
	now := m.now()

	run.ARN = arn
	run.ExecutionArn = executionArn
	run.StateMachineArn = smArn

	if run.Status == "" {
		run.Status = driver.MapRunStatusSucceeded
	}

	if run.StartDate.IsZero() {
		run.StartDate = now
	}

	m.mapRuns.Set(arn, &mapRunData{run: run})

	return arn, nil
}

func (m *Mock) getMapRun(arn string) (*mapRunData, error) {
	if !validMapRunARN(arn) {
		return nil, invalidArn("%q is not a valid Map Run ARN", arn)
	}

	rd, ok := m.mapRuns.Get(arn)
	if !ok {
		return nil, mapRunNotFound(arn)
	}

	return rd, nil
}

func (m *Mock) DescribeMapRun(_ context.Context, mapRunArn string) (*driver.MapRun, error) {
	rd, err := m.getMapRun(mapRunArn)
	if err != nil {
		return nil, err
	}

	rd.mu.RLock()
	defer rd.mu.RUnlock()

	out := rd.run

	return &out, nil
}

func (m *Mock) ListMapRuns(_ context.Context, executionArn string) ([]driver.MapRun, error) {
	if _, err := m.getExec(executionArn); err != nil {
		return nil, err
	}

	all := m.mapRuns.SortedValues()
	out := make([]driver.MapRun, 0, len(all))

	for _, rd := range all {
		rd.mu.RLock()
		run := rd.run
		rd.mu.RUnlock()

		if run.ExecutionArn != executionArn {
			continue
		}

		out = append(out, run)
	}

	return out, nil
}

func (m *Mock) UpdateMapRun(_ context.Context, in driver.UpdateMapRunInput) error {
	rd, err := m.getMapRun(in.MapRunArn)
	if err != nil {
		return err
	}

	rd.mu.Lock()
	defer rd.mu.Unlock()

	if in.MaxConcurrency != nil {
		rd.run.MaxConcurrency = *in.MaxConcurrency
	}

	if in.ToleratedFailureCount != nil {
		rd.run.ToleratedFailureCount = *in.ToleratedFailureCount
	}

	if in.ToleratedFailurePercentage != nil {
		rd.run.ToleratedFailurePercentage = *in.ToleratedFailurePercentage
	}

	return nil
}
