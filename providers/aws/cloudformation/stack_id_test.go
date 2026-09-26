package cloudformation

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// Terraform and the CLI waiters pass the stack ID (its ARN) as StackName.
func TestStackNameAcceptsStackID(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	created, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "by-id", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	id := created.ID

	got, err := m.DescribeStacks(ctx, id)
	requireNoError(t, err)
	assertEqual(t, got[0].Name, "by-id", "describe by ID")

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: id, TemplateBody: yamlStackTemplate})
	requireNoError(t, err)

	res, err := m.DescribeStackResources(ctx, id)
	requireNoError(t, err)
	assertEqual(t, len(res), 2, "resources by ID")

	_, err = m.DescribeStacks(ctx, id+"x")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("an unknown ID must be NotFound, got %v", err)
	}

	requireNoError(t, m.DeleteStack(ctx, id))

	got, err = m.DescribeStacks(ctx, id)
	requireNoError(t, err)
	assertEqual(t, got[0].Status, cfn.StatusDeleteComplete, "a deleted stack is still found by ID")

	if _, err = m.DescribeStacks(ctx, "by-id"); !cerrors.IsNotFound(err) {
		t.Fatalf("a deleted stack is not found by name, got %v", err)
	}

	// A new stack with the same name does not answer to the old ID.
	_, err = m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "by-id", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	if _, err = m.DescribeStacks(ctx, id); !cerrors.IsNotFound(err) {
		t.Fatalf("an old ID must not match the new stack, got %v", err)
	}

	if _, err = m.DescribeStacks(ctx, "arn:aws:cloudformation:us-east-1:123456789012:changeSet/x/y"); !cerrors.IsNotFound(err) {
		t.Fatalf("a non-stack ARN must be NotFound, got %v", err)
	}
}
