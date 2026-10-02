package cloudformation

import (
	"context"
	"regexp"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// TestRollbackReasonsOmitInternalCodePrefix covers that the stack- and
// resource-level status reasons recorded on a failed create carry only the
// human message, never the internal error-code name.
func TestRollbackReasonsOmitInternalCodePrefix(t *testing.T) {
	// codePrefix matches the internal canonical-code prefix cerrors.Error() adds.
	codePrefix := regexp.MustCompile(`^(NotFound|AlreadyExists|InvalidArgument|FailedPrecondition|PermissionDenied|` +
		`Throttled|Internal|Unimplemented|ResourceExhausted|Unavailable): `)

	ctx := context.Background()
	m := newTestMock(newBacking())

	body := `{"Resources":{"Bad":{"Type":"AWS::Unknown::Thing","Properties":{}}}}`

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusRollbackComplete, "rollback status")

	events, err := m.DescribeStackEvents(ctx, "demo")
	requireNoError(t, err)

	sawReason := false

	for _, ev := range events {
		if ev.StatusReason == "" {
			continue
		}

		sawReason = true

		if codePrefix.MatchString(ev.StatusReason) {
			t.Fatalf("event %s/%s reason %q leaks internal error-code prefix", ev.LogicalID, ev.Status, ev.StatusReason)
		}
	}

	if !sawReason {
		t.Fatal("expected a failure reason on the rollback events")
	}
}
