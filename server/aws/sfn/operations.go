package sfn

import (
	"context"
	"net/http"
	"strings"

	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

func (h *Handler) createStateMachine(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createStateMachineRequest) (any, error) {
		arn, versionArn, created, err := h.sfn.CreateStateMachine(ctx, sfndriver.CreateStateMachineInput{
			Name: req.Name, Definition: req.Definition, RoleArn: req.RoleArn, Type: req.Type,
			Description: req.Description, Publish: req.Publish, Tags: tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return createStateMachineResponse{
			StateMachineArn: arn, CreationDate: epoch(created), StateMachineVersionArn: versionArn,
		}, nil
	})
}

func (h *Handler) describeStateMachine(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *stateMachineArnRequest) (any, error) {
		sm, err := h.sfn.DescribeStateMachine(ctx, req.StateMachineArn)
		if err != nil {
			return nil, err
		}

		return describeStateMachineResponse{
			StateMachineArn: sm.ARN, Name: sm.Name, Definition: sm.Definition, RoleArn: sm.RoleArn,
			Type: sm.Type, Status: sm.Status, Description: sm.Description, RevisionID: sm.RevisionID,
			CreationDate: epoch(sm.CreationDate), Label: sm.Label,
			LoggingConfiguration: loggingConfigOrDefault(sm.LoggingConfigJSON),
			TracingConfiguration: tracingConfigOrDefault(sm.TracingConfigJSON),
		}, nil
	})
}

func (h *Handler) updateStateMachine(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateStateMachineRequest) (any, error) {
		res, err := h.sfn.UpdateStateMachine(ctx, sfndriver.UpdateStateMachineInput{
			ARN: req.StateMachineArn, Definition: req.Definition, RoleArn: req.RoleArn,
			Publish: req.Publish, VersionDesc: req.VersionDescription,
		})
		if err != nil {
			return nil, err
		}

		return updateStateMachineResponse{
			UpdateDate: epoch(res.UpdateDate), RevisionID: res.RevisionID,
			StateMachineVersionArn: res.StateMachineVersionArn,
		}, nil
	})
}

func (h *Handler) deleteStateMachine(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *stateMachineArnRequest) (any, error) {
		if err := h.sfn.DeleteStateMachine(ctx, req.StateMachineArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listStateMachines(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listStateMachinesRequest) (any, error) {
		machines, err := h.sfn.ListStateMachines(ctx)
		if err != nil {
			return nil, err
		}

		start, end, next := pageWindow(len(machines), req.NextToken, req.MaxResults)
		machines = machines[start:end]

		items := make([]stateMachineListItem, 0, len(machines))

		for i := range machines {
			sm := &machines[i]
			items = append(items, stateMachineListItem{
				StateMachineArn: sm.ARN, Name: sm.Name, Type: sm.Type, CreationDate: epoch(sm.CreationDate),
			})
		}

		return listStateMachinesResponse{StateMachines: items, NextToken: next}, nil
	})
}

func (h *Handler) startExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startExecutionRequest) (any, error) {
		exec, err := h.sfn.StartExecution(ctx, sfndriver.StartExecutionInput{
			StateMachineArn: req.StateMachineArn, Name: req.Name, Input: req.Input,
		})
		if err != nil {
			return nil, err
		}

		return startExecutionResponse{ExecutionArn: exec.ARN, StartDate: epoch(exec.StartDate)}, nil
	})
}

func (h *Handler) startSyncExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *startExecutionRequest) (any, error) {
		exec, err := h.sfn.StartSyncExecution(ctx, sfndriver.StartExecutionInput{
			StateMachineArn: req.StateMachineArn, Name: req.Name, Input: req.Input,
		})
		if err != nil {
			return nil, err
		}

		return startSyncExecutionResponse{
			ExecutionArn: exec.ARN, StateMachineArn: exec.StateMachineArn, Name: exec.Name,
			Status: exec.Status, StartDate: epoch(exec.StartDate), StopDate: epoch(exec.StopDate),
			Input: exec.Input, Output: exec.Output, Error: exec.Error, Cause: exec.Cause,
		}, nil
	})
}

func (h *Handler) describeExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *executionArnRequest) (any, error) {
		exec, err := h.sfn.DescribeExecution(ctx, req.ExecutionArn)
		if err != nil {
			return nil, err
		}

		return describeExecutionResponse{
			ExecutionArn: exec.ARN, StateMachineArn: exec.StateMachineArn, Name: exec.Name,
			Status: exec.Status, StartDate: epoch(exec.StartDate), StopDate: epoch(exec.StopDate),
			Input: exec.Input, Output: exec.Output, Error: exec.Error, Cause: exec.Cause,
		}, nil
	})
}

func (h *Handler) stopExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *stopExecutionRequest) (any, error) {
		stopDate, err := h.sfn.StopExecution(ctx, req.ExecutionArn, req.Error, req.Cause)
		if err != nil {
			return nil, err
		}

		return stopExecutionResponse{StopDate: epoch(stopDate)}, nil
	})
}

func (h *Handler) listExecutions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listExecutionsRequest) (any, error) {
		execs, err := h.sfn.ListExecutions(ctx, req.StateMachineArn, req.StatusFilter)
		if err != nil {
			return nil, err
		}

		start, end, next := pageWindow(len(execs), req.NextToken, req.MaxResults)
		execs = execs[start:end]

		items := make([]executionListItem, 0, len(execs))

		for i := range execs {
			e := &execs[i]
			items = append(items, executionListItem{
				ExecutionArn: e.ARN, StateMachineArn: e.StateMachineArn, Name: e.Name,
				Status: e.Status, StartDate: epoch(e.StartDate), StopDate: epoch(e.StopDate),
			})
		}

		return listExecutionsResponse{Executions: items, NextToken: next}, nil
	})
}

func (h *Handler) getExecutionHistory(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *getExecutionHistoryRequest) (any, error) {
		events, err := h.sfn.GetExecutionHistory(ctx, req.ExecutionArn, req.ReverseOrder)
		if err != nil {
			return nil, err
		}

		return getExecutionHistoryResponse{Events: eventsToWire(events)}, nil
	})
}

func eventsToWire(events []sfndriver.HistoryEvent) []historyEvent {
	out := make([]historyEvent, 0, len(events))

	for i := range events {
		e := &events[i]
		he := historyEvent{
			ID: e.ID, PreviousEventID: e.PreviousEventID, Type: e.Type, Timestamp: epoch(e.Timestamp),
		}

		populateEventDetails(&he, e)

		out = append(out, he)
	}

	return out
}

// populateEventDetails fills the type-specific detail struct on a wire history
// event. The *StateEntered/*StateExited suffixes generalize across state types
// (Pass/Choice/Wait/Succeed/Task), while the execution- and Lambda-specific
// events map by exact Type.
func populateEventDetails(he *historyEvent, e *sfndriver.HistoryEvent) {
	populateExecutionDetails(he, e)
	populateLambdaDetails(he, e)

	if strings.HasSuffix(e.Type, "StateEntered") {
		he.StateEnteredDetails = &stateEnteredDetails{Name: e.StateName, Input: e.Input}
	}

	if strings.HasSuffix(e.Type, "StateExited") {
		he.StateExitedDetails = &stateExitedDetails{Name: e.StateName, Output: e.Output}
	}
}

func populateExecutionDetails(he *historyEvent, e *sfndriver.HistoryEvent) {
	switch e.Type {
	case "ExecutionStarted":
		he.ExecutionStartedDetails = &executionStartedDetails{Input: e.Input}
	case "ExecutionSucceeded":
		he.ExecutionSucceededDetails = &executionSucceededDetails{Output: e.Output}
	case "ExecutionFailed":
		he.ExecutionFailedDetails = &executionFailedDetails{Error: e.Error, Cause: e.Cause}
	case "ExecutionAborted":
		he.ExecutionAbortedDetails = &executionAbortedDetails{Error: e.Error, Cause: e.Cause}
	}
}

func populateLambdaDetails(he *historyEvent, e *sfndriver.HistoryEvent) {
	switch e.Type {
	case "LambdaFunctionScheduled":
		he.LambdaFunctionScheduledDetails = &lambdaFunctionScheduledDetails{Resource: e.Resource, Parameters: e.Input}
	case "LambdaFunctionStarted":
		he.LambdaFunctionStartedDetails = &lambdaFunctionStartedDetails{}
	case "LambdaFunctionSucceeded":
		he.LambdaFunctionSucceededDetails = &lambdaFunctionSucceededDetails{Output: e.Output}
	case "LambdaFunctionFailed":
		he.LambdaFunctionFailedDetails = &lambdaFunctionFailedDetails{Error: e.Error, Cause: e.Cause}
	}
}

func (h *Handler) describeStateMachineForExecution(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *executionArnRequest) (any, error) {
		sm, err := h.sfn.DescribeStateMachineForExecution(ctx, req.ExecutionArn)
		if err != nil {
			return nil, err
		}

		return describeStateMachineForExecutionResponse{
			StateMachineArn: sm.ARN, Name: sm.Name, Definition: sm.Definition, RoleArn: sm.RoleArn,
			UpdateDate: epoch(sm.CreationDate), RevisionID: sm.RevisionID,
		}, nil
	})
}
