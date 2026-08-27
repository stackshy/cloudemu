package eventbridge

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const (
	// putEventsAction is the only action a legacy PutPermission call may grant.
	putEventsAction = "events:PutEvents"
	// policyVersion is the IAM policy language version EventBridge stamps on a
	// bus resource policy.
	policyVersion = "2012-10-17"
)

// busNameOrDefault resolves an empty bus name to the default bus.
func busNameOrDefault(name string) string {
	if name == "" {
		return defaultBusName
	}

	return name
}

// PutPermission attaches a statement to an event bus's resource policy. Two
// mutually-exclusive forms are accepted: the legacy Action/Principal/StatementID
// trio (optionally with a Condition), which adds or replaces a single statement
// keyed by StatementID; or a full Policy JSON document, which replaces the whole
// policy. It mirrors the AWS EventBridge PutPermission API.
func (m *Mock) PutPermission(_ context.Context, busName string, in driver.PermissionInput) error {
	bd, ok := m.buses.Get(busNameOrDefault(busName))
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busNameOrDefault(busName))
	}

	if err := validatePutPermission(in); err != nil {
		return err
	}

	bd.mu.Lock()
	defer bd.mu.Unlock()

	if in.Policy != "" {
		stmts, err := parsePolicyStatements(in.Policy)
		if err != nil {
			return err
		}

		bd.policyStmts = stmts

		return nil
	}

	bd.policyStmts = upsertStatement(bd.policyStmts, buildStatement(in, bd.info.ARN))

	return nil
}

// validatePutPermission enforces the mutually-exclusive form rules and the
// legacy-form constraints of a PutPermission call.
func validatePutPermission(in driver.PermissionInput) error {
	legacy := in.Action != "" || in.Principal != "" || in.StatementID != ""
	hasPolicy := in.Policy != ""

	if legacy && hasPolicy {
		return errors.New(errors.InvalidArgument,
			"policy cannot be specified together with statementId, action, or principal")
	}

	if !legacy && !hasPolicy {
		return errors.New(errors.InvalidArgument,
			"either a policy or the statementId, action, and principal parameters must be specified")
	}

	if hasPolicy {
		return nil
	}

	return validateLegacyPermission(in)
}

// validateLegacyPermission checks the field constraints of the legacy
// Action/Principal/StatementID PutPermission form.
func validateLegacyPermission(in driver.PermissionInput) error {
	if in.Action != putEventsAction {
		return errors.Newf(errors.InvalidArgument, "action %q is not supported; only events:PutEvents is allowed", in.Action)
	}

	if in.StatementID == "" || in.Principal == "" {
		return errors.New(errors.InvalidArgument, "statementId and principal are required")
	}

	return nil
}

// RemovePermission removes a statement from an event bus's resource policy by
// StatementID, or clears the entire policy when removeAll is set. An unknown
// StatementID is reported as NotFound (ResourceNotFoundException on the wire).
func (m *Mock) RemovePermission(_ context.Context, busName, statementID string, removeAll bool) error {
	bd, ok := m.buses.Get(busNameOrDefault(busName))
	if !ok {
		return errors.Newf(errors.NotFound, "event bus %q not found", busNameOrDefault(busName))
	}

	bd.mu.Lock()
	defer bd.mu.Unlock()

	if removeAll {
		bd.policyStmts = nil

		return nil
	}

	if statementID == "" {
		return errors.New(errors.InvalidArgument, "statementId or removeAllPermissions must be specified")
	}

	idx := indexOfStatement(bd.policyStmts, statementID)
	if idx < 0 {
		return errors.Newf(errors.NotFound, "Statement with the id %q does not exist on the resource policy.", statementID)
	}

	bd.policyStmts = append(bd.policyStmts[:idx], bd.policyStmts[idx+1:]...)

	return nil
}

// buildStatement renders a legacy PutPermission trio into an IAM policy
// statement. A "*" principal grants everyone; a 12-digit account id is expanded
// to its root ARN, matching how EventBridge stores the statement.
func buildStatement(in driver.PermissionInput, busARN string) map[string]any {
	var principal any = "*"
	if in.Principal != "*" {
		principal = map[string]any{"AWS": "arn:aws:iam::" + in.Principal + ":root"}
	}

	stmt := map[string]any{
		"Sid":       in.StatementID,
		"Effect":    "Allow",
		"Principal": principal,
		"Action":    in.Action,
		"Resource":  busARN,
	}

	if in.Condition != nil {
		stmt["Condition"] = map[string]any{
			in.Condition.Type: map[string]any{in.Condition.Key: in.Condition.Value},
		}
	}

	return stmt
}

// upsertStatement replaces the statement with a matching Sid, or appends a new
// one, preserving insertion order.
func upsertStatement(stmts []map[string]any, stmt map[string]any) []map[string]any {
	sid, _ := stmt["Sid"].(string)
	if idx := indexOfStatement(stmts, sid); idx >= 0 {
		stmts[idx] = stmt

		return stmts
	}

	return append(stmts, stmt)
}

// indexOfStatement returns the position of the statement whose "Sid" equals sid,
// or -1 when none matches.
func indexOfStatement(stmts []map[string]any, sid string) int {
	for i, s := range stmts {
		if got, _ := s["Sid"].(string); got == sid {
			return i
		}
	}

	return -1
}

// parsePolicyStatements extracts the statement list from a full policy document.
// A single-object "Statement" is normalized to a one-element list.
func parsePolicyStatements(policy string) ([]map[string]any, error) {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return nil, errors.New(errors.InvalidArgument, "policy is not a valid JSON policy document")
	}

	if len(doc.Statement) == 0 {
		return nil, errors.New(errors.InvalidArgument, "policy must contain a statement")
	}

	var list []map[string]any
	if err := json.Unmarshal(doc.Statement, &list); err == nil {
		return list, nil
	}

	var single map[string]any
	if err := json.Unmarshal(doc.Statement, &single); err != nil {
		return nil, errors.New(errors.InvalidArgument, "policy statement is malformed")
	}

	return []map[string]any{single}, nil
}

// renderPolicy serializes the bus's statements into an IAM policy document
// string. It returns "" when there are no statements, so DescribeEventBus omits
// the Policy field for a bus without a resource policy.
func renderPolicy(stmts []map[string]any) string {
	if len(stmts) == 0 {
		return ""
	}

	out, err := json.Marshal(map[string]any{
		"Version":   policyVersion,
		"Statement": stmts,
	})
	if err != nil {
		return ""
	}

	return string(out)
}
