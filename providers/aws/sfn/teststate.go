package sfn

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/providers/aws/sfn/asl"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestState evaluates a single state definition through the interpreter: it runs
// the state's I/O pipeline and handler once (following a Choice to its selected
// branch without executing it) and returns the resulting Output/Status/NextState,
// or Error/Cause on failure. ctx is accepted for parity with the Task->Lambda
// seam a later PR threads through the same handlers.
func (m *Mock) TestState(_ context.Context, in driver.TestStateInput) (*driver.TestStateResult, error) {
	if strings.TrimSpace(in.Definition) == "" {
		return nil, invalidName("definition is required")
	}

	res, err := asl.TestOne(in.Definition, &asl.RunInput{Input: in.Input, StartTime: m.now()})
	if err != nil {
		return nil, invalidDefinition(err.Error())
	}

	return &driver.TestStateResult{
		Output: res.Output, Status: res.Status, NextState: res.NextState,
		Error: res.Error, Cause: res.Cause,
	}, nil
}

// ValidateStateMachineDefinition runs the ASL parser's structural validation:
// valid definitions yield result OK; a structural violation (bad JSON, missing
// StartAt, unknown state Type, dangling Next, Choice without Choices, unsupported
// fields on Wait/Choice/Succeed/Fail, QueryLanguage JSONata) yields FAIL with a
// single error diagnostic.
func (*Mock) ValidateStateMachineDefinition(
	_ context.Context, definition, _ string,
) (*driver.ValidationResult, error) {
	if strings.TrimSpace(definition) == "" {
		return validationFail("MISSING_DEFINITION", "state machine definition is required"), nil
	}

	if _, err := asl.Parse(definition); err != nil {
		return validationFail("SCHEMA_VALIDATION_FAILED", err.Error()), nil
	}

	return &driver.ValidationResult{Result: driver.ValidationResultOK}, nil
}

func validationFail(code, message string) *driver.ValidationResult {
	return &driver.ValidationResult{
		Result: driver.ValidationResultFail,
		Diagnostics: []driver.ValidationDiagnostic{{
			Severity: driver.ValidationSeverityError, Code: code, Message: message,
		}},
	}
}
