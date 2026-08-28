package cloudformation

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// backing is a stand-in service backend a fake provisioner writes into, so a
// test can assert a stack's resources really exist (and are gone after delete).
type backing struct {
	items map[string]bool
}

func newBacking() *backing { return &backing{items: map[string]bool{}} }

// recProv is a fake provisioner: it records the physical resource in a backing
// store and exposes an Arn attribute for Fn::GetAtt.
type recProv struct {
	store     *backing
	arnPrefix string
}

func (p recProv) Create(_ context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := cfn.PropString(req.Properties, "Name")
	if name == "" {
		name = req.StackName + "-" + req.LogicalID
	}

	p.store.items[name] = true

	return &cfn.ProvisionedResource{
		PhysicalID: name,
		Attributes: map[string]string{"Arn": p.arnPrefix + name},
	}, nil
}

func (p recProv) Delete(_ context.Context, physicalID string, _ map[string]any) error {
	delete(p.store.items, physicalID)
	return nil
}

// failProv always fails, to exercise rollback.
type failProv struct{}

func (failProv) Create(_ context.Context, _ cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	return nil, errBoom
}

func (failProv) Delete(_ context.Context, _ string, _ map[string]any) error { return nil }

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }

func newTestMock(store *backing) *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012")))
	m.SetRegistry(cfn.Registry{
		"Test::Bucket": recProv{store: store, arnPrefix: "arn:aws:s3:::"},
		"Test::Topic":  recProv{store: store, arnPrefix: "arn:aws:sns:us-east-1:123456789012:"},
		"Test::Boom":   failProv{},
	})

	return m
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T, msg string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

const twoResourceTemplate = `{
	"Resources":{
		"MyBucket":{"Type":"Test::Bucket","Properties":{"Name":"data-bucket"}},
		"MyTopic":{"Type":"Test::Topic","Properties":{"Name":{"Fn::Sub":"${MyBucket}-events"}}}
	},
	"Outputs":{
		"BucketRef":{"Value":{"Ref":"MyBucket"}},
		"BucketArn":{"Value":{"Fn::GetAtt":["MyBucket","Arn"]}}
	}
}`

func TestCreateStackProvisionsResourcesAndOutputs(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusCreateComplete, "stack status")

	// The resources really exist in the backing store, keyed by physical id.
	if !store.items["data-bucket"] {
		t.Fatal("bucket resource was not provisioned")
	}

	if !store.items["data-bucket-events"] {
		t.Fatal("topic resource (Fn::Sub name) was not provisioned")
	}

	outputs := map[string]string{}
	for _, o := range stack.Outputs {
		outputs[o.Key] = o.Value
	}

	assertEqual(t, outputs["BucketRef"], "data-bucket", "Ref output")
	assertEqual(t, outputs["BucketArn"], "arn:aws:s3:::data-bucket", "GetAtt output")

	// Resources and events are recorded.
	res, err := m.DescribeStackResources(ctx, "demo")
	requireNoError(t, err)
	assertEqual(t, len(res), 2, "resource count")

	events, err := m.DescribeStackEvents(ctx, "demo")
	requireNoError(t, err)

	if len(events) == 0 {
		t.Fatal("no stack events recorded")
	}
}

func TestCreateStackDependencyOrder(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	events, err := m.DescribeStackEvents(ctx, "demo")
	requireNoError(t, err)

	// Events are newest-first; the bucket's CREATE_COMPLETE must appear after
	// the topic's (the topic depends on the bucket, so it is created last, and
	// its completion event is therefore newer / earlier in the list).
	bucketDone, topicDone := -1, -1

	for i, e := range events {
		if e.Status != cfn.ResourceCreateComplete {
			continue
		}

		switch e.LogicalID {
		case "MyBucket":
			bucketDone = i
		case "MyTopic":
			topicDone = i
		}
	}

	if topicDone == -1 || bucketDone == -1 || topicDone >= bucketDone {
		t.Fatalf("expected topic to complete after bucket; bucketDone=%d topicDone=%d", bucketDone, topicDone)
	}
}

func TestDeleteStackTearsDownResources(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	requireNoError(t, m.DeleteStack(ctx, "demo"))

	if len(store.items) != 0 {
		t.Fatalf("expected all resources removed, still have %v", store.items)
	}

	// DescribeStacks by name reports the stack as gone.
	if _, err := m.DescribeStacks(ctx, "demo"); err == nil {
		t.Fatal("expected DescribeStacks to fail for a deleted stack")
	}
}

func TestUpdateStackReplacesChangedResource(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	updated := `{
		"Resources":{
			"MyBucket":{"Type":"Test::Bucket","Properties":{"Name":"data-bucket"}},
			"MyTopic":{"Type":"Test::Topic","Properties":{"Name":"renamed-topic"}}
		}
	}`

	stack, err := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "demo", TemplateBody: updated})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusUpdateComplete, "update status")

	// The unchanged bucket is kept; the changed topic is replaced.
	if !store.items["data-bucket"] {
		t.Fatal("unchanged bucket should still exist")
	}

	if store.items["data-bucket-events"] {
		t.Fatal("old topic should have been deleted on replacement")
	}

	if !store.items["renamed-topic"] {
		t.Fatal("new topic should exist after update")
	}
}

func TestCreateStackUnsupportedTypeRollsBack(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	body := `{"Resources":{
		"Good":{"Type":"Test::Bucket","Properties":{"Name":"keep"}},
		"Bad":{"Type":"AWS::Unknown::Thing","Properties":{"Ref":"Good"}}
	}}`

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusRollbackComplete, "rollback status")

	// The resource created before the failure was rolled back (deleted).
	if store.items["keep"] {
		t.Fatal("expected rollback to delete the already-created resource")
	}
}

func TestCreateStackFailingResourceRollsBack(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	body := `{"Resources":{
		"Good":{"Type":"Test::Bucket","Properties":{"Name":"keep"}},
		"Bad":{"Type":"Test::Boom","Properties":{"Name":{"Ref":"Good"}}}
	}}`

	stack, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body})
	requireNoError(t, err)
	assertEqual(t, stack.Status, cfn.StatusRollbackComplete, "rollback status")

	if store.items["keep"] {
		t.Fatal("expected rollback to delete the already-created resource")
	}
}

func TestCreateStackMissingParameter(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	body := `{
		"Parameters":{"Name":{"Type":"String"}},
		"Resources":{"B":{"Type":"Test::Bucket","Properties":{"Name":{"Ref":"Name"}}}}
	}`

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body})
	if err == nil {
		t.Fatal("expected error for missing required parameter")
	}
}

func TestCreateStackParameterRef(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	body := `{
		"Parameters":{"BucketName":{"Type":"String","Default":"defaulted"}},
		"Resources":{"B":{"Type":"Test::Bucket","Properties":{"Name":{"Ref":"BucketName"}}}}
	}`

	// Supplied value overrides the default.
	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{
		StackName:    "demo",
		TemplateBody: body,
		Parameters:   []cfn.Parameter{{Key: "BucketName", Value: "provided"}},
	})
	requireNoError(t, err)

	if !store.items["provided"] {
		t.Fatal("parameter value was not used for the resource name")
	}
}

func TestDuplicateStackRejected(t *testing.T) {
	ctx := context.Background()
	m := newTestMock(newBacking())

	body := `{"Resources":{"B":{"Type":"Test::Bucket","Properties":{"Name":"b"}}}}`

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body})
	requireNoError(t, err)

	if _, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: body}); err == nil {
		t.Fatal("expected AlreadyExists for a duplicate active stack")
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newBacking()
	m := newTestMock(store)

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "demo", TemplateBody: twoResourceTemplate})
	requireNoError(t, err)

	data, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newTestMock(store)
	requireNoError(t, restored.Restore(ctx, data))

	stacks, err := restored.DescribeStacks(ctx, "demo")
	requireNoError(t, err)
	assertEqual(t, len(stacks), 1, "restored stack count")
	assertEqual(t, stacks[0].Status, cfn.StatusCreateComplete, "restored status")
	assertEqual(t, len(stacks[0].Outputs), 2, "restored outputs")

	// Template survives the round-trip.
	body, err := restored.GetTemplate(ctx, "demo")
	requireNoError(t, err)

	if body == "" {
		t.Fatal("restored template body is empty")
	}
}
