package cloudformation

import (
	"context"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

const noEchoTemplate = `Parameters:
  Secret:
    Type: String
    NoEcho: "true"
  Plain:
    Type: String
    Default: visible
Resources:
  B:
    Type: Test::Bucket
    Properties:
      Name: !Sub "${Secret}-bucket"
Outputs:
  Name:
    Value: !Ref B
`

// NoEcho values are masked in every stack read, but !Ref still sees the real
// value.
func TestNoEchoParameterMasked(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	created, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "secret", TemplateBody: noEchoTemplate,
		Parameters: []cfn.Parameter{{Key: "Secret", Value: "hunter2"}},
	})
	requireNoError(t, err)

	if !store.items["hunter2-bucket"] {
		t.Fatalf("Ref must see the real value, got %v", store.items)
	}

	assertParams := func(s *cfn.Stack, from string) {
		t.Helper()

		got := map[string]string{}
		for _, p := range s.Parameters {
			got[p.Key] = p.Value
		}

		assertEqual(t, got["Secret"], maskedValue, from+": NoEcho value")
		assertEqual(t, got["Plain"], "visible", from+": plain value")
	}

	assertParams(created, "CreateStack")

	stacks, err := m.DescribeStacks(ctx, "secret")
	requireNoError(t, err)
	assertParams(&stacks[0], "DescribeStacks")

	all, err := m.DescribeStacks(ctx, "")
	requireNoError(t, err)
	assertParams(&all[0], "DescribeStacks all")

	updated, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "secret", TemplateBody: noEchoTemplate + "\n",
		Parameters: []cfn.Parameter{{Key: "Secret", Value: "hunter2"}},
	})
	requireNoError(t, err)
	assertParams(updated, "UpdateStack")

	for _, e := range updated.Events {
		if e.StatusReason == "hunter2" || e.PhysicalID == "hunter2" {
			t.Fatalf("an event leaks the NoEcho value: %+v", e)
		}
	}
}
