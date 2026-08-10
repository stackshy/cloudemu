package sfn

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// TestState evaluates a single state definition. The emulator runs no Amazon
// States Language interpreter: a non-empty definition succeeds and echoes the
// input as output (an empty input echoes "{}"); an empty definition fails.
func (*Mock) TestState(_ context.Context, in driver.TestStateInput) (*driver.TestStateResult, error) {
	if strings.TrimSpace(in.Definition) == "" {
		return nil, invalidName("definition is required")
	}

	output := in.Input
	if strings.TrimSpace(output) == "" {
		output = emptyJSON
	}

	return &driver.TestStateResult{Output: output, Status: driver.TestStatusSucceeded}, nil
}

// ValidateStateMachineDefinition checks that a definition is non-empty, valid
// JSON. No semantic Amazon States Language validation is performed: valid JSON
// yields result OK with no diagnostics; anything else yields FAIL with a single
// error diagnostic.
func (*Mock) ValidateStateMachineDefinition(
	_ context.Context, definition, _ string,
) (*driver.ValidationResult, error) {
	if strings.TrimSpace(definition) == "" {
		return &driver.ValidationResult{
			Result: driver.ValidationResultFail,
			Diagnostics: []driver.ValidationDiagnostic{{
				Severity: driver.ValidationSeverityError, Code: "MISSING_DEFINITION",
				Message: "state machine definition is required",
			}},
		}, nil
	}

	if !json.Valid([]byte(definition)) {
		return &driver.ValidationResult{
			Result: driver.ValidationResultFail,
			Diagnostics: []driver.ValidationDiagnostic{{
				Severity: driver.ValidationSeverityError, Code: "SCHEMA_VALIDATION_FAILED",
				Message: "definition is not valid JSON",
			}},
		}, nil
	}

	return &driver.ValidationResult{Result: driver.ValidationResultOK}, nil
}
