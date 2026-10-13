package driver

import (
	"context"
	"encoding/json"
)

// Flow validation severities (FlowValidationSeverity).
const (
	FlowValidationError   = "Error"
	FlowValidationWarning = "Warning"
)

// FlowValidation is one problem found in a flow definition. Type is a
// FlowValidationType value.
type FlowValidation struct {
	Message  string
	Severity string
	Type     string
}

// FlowValidator checks a flow definition without storing it.
type FlowValidator interface {
	ValidateFlowDefinition(ctx context.Context, definition json.RawMessage) ([]FlowValidation, error)
}
