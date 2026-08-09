package sfn

import (
	"context"
	"net/http"

	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

func (h *Handler) redriveExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *redriveExecutionRequest) (any, error) {
		res, err := h.sfn.RedriveExecution(ctx, req.ExecutionArn)
		if err != nil {
			return nil, err
		}

		return redriveExecutionResponse{RedriveDate: epoch(res.RedriveDate)}, nil
	})
}

func (h *Handler) describeMapRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *mapRunArnRequest) (any, error) {
		run, err := h.sfn.DescribeMapRun(ctx, req.MapRunArn)
		if err != nil {
			return nil, err
		}

		return describeMapRunResponse{
			MapRunArn: run.ARN, ExecutionArn: run.ExecutionArn, Status: run.Status,
			MaxConcurrency: run.MaxConcurrency, ToleratedFailureCount: run.ToleratedFailureCount,
			ToleratedFailurePercentage: run.ToleratedFailurePercentage, RedriveCount: run.RedriveCount,
			StartDate: epoch(run.StartDate), StopDate: epoch(run.StopDate), RedriveDate: epoch(run.RedriveDate),
			ExecutionCounts: countsToWire(run.ExecutionCounts), ItemCounts: countsToWire(run.ItemCounts),
		}, nil
	})
}

func (h *Handler) listMapRuns(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listMapRunsRequest) (any, error) {
		runs, err := h.sfn.ListMapRuns(ctx, req.ExecutionArn)
		if err != nil {
			return nil, err
		}

		items := make([]mapRunListItem, 0, len(runs))

		for i := range runs {
			run := &runs[i]
			items = append(items, mapRunListItem{
				ExecutionArn: run.ExecutionArn, MapRunArn: run.ARN, StateMachineArn: run.StateMachineArn,
				StartDate: epoch(run.StartDate), StopDate: epoch(run.StopDate),
			})
		}

		return listMapRunsResponse{MapRuns: items}, nil
	})
}

func (h *Handler) updateMapRun(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateMapRunRequest) (any, error) {
		err := h.sfn.UpdateMapRun(ctx, sfndriver.UpdateMapRunInput{
			MapRunArn: req.MapRunArn, MaxConcurrency: req.MaxConcurrency,
			ToleratedFailureCount: req.ToleratedFailureCount, ToleratedFailurePercentage: req.ToleratedFailurePercentage,
		})
		if err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) testState(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *testStateRequest) (any, error) {
		res, err := h.sfn.TestState(ctx, sfndriver.TestStateInput{Definition: req.Definition, Input: req.Input})
		if err != nil {
			return nil, err
		}

		return testStateResponse{
			Output: res.Output, Status: res.Status, NextState: res.NextState,
			Error: res.Error, Cause: res.Cause,
		}, nil
	})
}

func (h *Handler) validateStateMachineDefinition(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *validateDefinitionRequest) (any, error) {
		res, err := h.sfn.ValidateStateMachineDefinition(ctx, req.Definition, req.Type)
		if err != nil {
			return nil, err
		}

		diags := make([]validationDiagnostic, 0, len(res.Diagnostics))

		for i := range res.Diagnostics {
			d := &res.Diagnostics[i]
			diags = append(diags, validationDiagnostic{
				Severity: d.Severity, Code: d.Code, Message: d.Message, Location: d.Location,
			})
		}

		return validateDefinitionResponse{Result: res.Result, Diagnostics: diags, Truncated: res.Truncated}, nil
	})
}
